package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/jobstatus"
)

// jobStatusProvider is the seam the admin-jobs handler depends on. It is
// satisfied by *jobstatus.Service and faked in tests.
type jobStatusProvider interface {
	Overview(ctx context.Context) (jobstatus.Overview, error)
}

type jobOperationsProvider interface {
	ListRuns(ctx context.Context, filter jobstatus.Filter, limit, offset int32) ([]jobstatus.Run, int64, int64, error)
	GetDetail(ctx context.Context, id uuid.UUID, limit, offset int32) (jobstatus.Detail, error)
	EventsAfter(ctx context.Context, cursor int64, filter jobstatus.Filter, limit int32) ([]jobstatus.Event, error)
	CursorBounds(ctx context.Context) (int64, int64, error)
}

const (
	defaultJobPageLimit = 50
	maxJobPageLimit     = 100
	jobSSEPollInterval  = time.Second
	jobSSEHeartbeat     = 10 * time.Second
	jobSSEMaxLifetime   = 2 * time.Minute
)

type jobRunsResponse struct {
	Runs        []jobstatus.Run `json:"runs"`
	Total       int64           `json:"total"`
	Limit       int             `json:"limit"`
	Offset      int             `json:"offset"`
	EventCursor string          `json:"event_cursor"`
}

type jobRunDetailResponse struct {
	Run          jobstatus.Run     `json:"run"`
	Events       []jobstatus.Event `json:"events"`
	EventsTotal  int64             `json:"events_total"`
	EventsLimit  int               `json:"events_limit"`
	EventsOffset int               `json:"events_offset"`
	EventCursor  string            `json:"event_cursor"`
}

// handleListJobs returns the durable-queue operations snapshot for the admin
// jobs page: per-queue {pending, running, done, failed, oldest_pending_age} plus
// a merged recent-failures list (id/error/attempts — no secrets). Admin-only.
func (s *Server) handleListJobs(c echo.Context) error {
	ov, err := s.jobStatusSvc.Overview(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, ov)
}

// handleListJobRuns returns individual sanitized executions. The existing
// /admin/jobs overview remains unchanged for backwards compatibility.
func (s *Server) handleListJobRuns(c echo.Context) error {
	filter, err := parseJobFilter(c, false)
	if err != nil {
		return err
	}
	limit := clampInt(queryInt(c, "limit", defaultJobPageLimit), 1, maxJobPageLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	runs, total, cursor, err := s.jobOperationsSvc.ListRuns(c.Request().Context(), filter, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	if runs == nil {
		runs = []jobstatus.Run{}
	}
	return c.JSON(http.StatusOK, jobRunsResponse{
		Runs: runs, Total: total, Limit: limit, Offset: offset,
		EventCursor: strconv.FormatInt(cursor, 10),
	})
}

func (s *Server) handleGetJobRun(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid job run id")
	}
	limit := clampInt(queryInt(c, "events_limit", defaultJobPageLimit), 1, maxJobPageLimit)
	offset := queryInt(c, "events_offset", 0)
	if offset < 0 {
		offset = 0
	}
	detail, err := s.jobOperationsSvc.GetDetail(c.Request().Context(), id, int32(limit), int32(offset))
	if errors.Is(err, jobstatus.ErrRunNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "job run not found")
	}
	if err != nil {
		return err
	}
	if detail.Events == nil {
		detail.Events = []jobstatus.Event{}
	}
	return c.JSON(http.StatusOK, jobRunDetailResponse{
		Run: detail.Run, Events: detail.Events, EventsTotal: detail.EventsTotal,
		EventsLimit: limit, EventsOffset: offset,
		EventCursor: strconv.FormatInt(detail.EventCursor, 10),
	})
}

// handleJobEvents streams durable job events and resumes strictly after an
// opaque decimal cursor. The stream is deliberately bounded, causing clients to
// reconnect and re-authenticate periodically. REST list/detail remains the
// polling/manual-refresh fallback.
func (s *Server) handleJobEvents(c echo.Context) error {
	filter, err := parseJobFilter(c, true)
	if err != nil {
		return err
	}
	after, err := parseEventCursor(c)
	if err != nil {
		return jobCursorError(c, http.StatusBadRequest, "invalid_event_cursor", "event cursor must be an unsigned decimal string")
	}
	_, maxCursor, err := s.jobOperationsSvc.CursorBounds(c.Request().Context())
	if err != nil {
		return err
	}
	if after > maxCursor {
		return jobCursorError(c, http.StatusBadRequest, "invalid_event_cursor", "event cursor is ahead of the current stream")
	}

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "streaming is not supported")
	}
	h := c.Response().Header()
	h.Set(echo.HeaderContentType, "text/event-stream")
	h.Set(echo.HeaderCacheControl, "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	h.Set(echo.HeaderConnection, "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(c.Response(), "retry: 2000\n\n")
	flusher.Flush()

	lifetime := jobSSEMaxLifetime
	if expires, ok := tokenExpiryFromContext(c); ok {
		if remaining := time.Until(expires); remaining < lifetime {
			lifetime = remaining
		}
	}
	if lifetime <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), lifetime)
	defer cancel()
	poll := time.NewTicker(jobSSEPollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(jobSSEHeartbeat)
	defer heartbeat.Stop()

	current := after
	writeAvailable := func() bool {
		events, qerr := s.jobOperationsSvc.EventsAfter(ctx, current, filter, 100)
		if qerr != nil {
			return false
		}
		for _, event := range events {
			data, merr := json.Marshal(event)
			if merr != nil {
				continue
			}
			if _, werr := fmt.Fprintf(c.Response(), "id: %s\nevent: job-event\ndata: %s\n\n", event.Cursor, data); werr != nil {
				return false
			}
			parsed, perr := strconv.ParseInt(event.Cursor, 10, 64)
			if perr == nil {
				current = parsed
			}
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		return true
	}
	if !writeAvailable() {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			if !writeAvailable() {
				return nil
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(c.Response(), ": heartbeat %d\n\n", time.Now().UTC().Unix()); err != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}

func parseJobFilter(c echo.Context, includeJobID bool) (jobstatus.Filter, error) {
	f := jobstatus.Filter{
		State:        strings.TrimSpace(c.QueryParam("state")),
		Type:         strings.TrimSpace(c.QueryParam("type")),
		Queue:        strings.TrimSpace(c.QueryParam("queue")),
		ResourceType: strings.TrimSpace(c.QueryParam("resource_type")),
		ResourceID:   strings.TrimSpace(c.QueryParam("resource_id")),
		WorkerID:     strings.TrimSpace(c.QueryParam("worker_id")),
	}
	if f.State != "" && !jobstatus.ValidRunState(f.State) {
		return f, echo.NewHTTPError(http.StatusBadRequest, "invalid job state")
	}
	for name, value := range map[string]string{
		"type": f.Type, "queue": f.Queue, "resource_type": f.ResourceType,
		"resource_id": f.ResourceID, "worker_id": f.WorkerID,
	} {
		if len(value) > 255 || strings.ContainsAny(value, "\r\n\x00") {
			return f, echo.NewHTTPError(http.StatusBadRequest, "invalid "+name+" filter")
		}
	}
	if raw := strings.TrimSpace(c.QueryParam("failure")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return f, echo.NewHTTPError(http.StatusBadRequest, "failure must be true or false")
		}
		f.Failure = &value
	}
	parseTime := func(name string) (*time.Time, error) {
		raw := strings.TrimSpace(c.QueryParam(name))
		if raw == "" {
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be RFC3339")
		}
		return &value, nil
	}
	var err error
	if f.CreatedAfter, err = parseTime("created_after"); err != nil {
		return f, err
	}
	if f.CreatedBefore, err = parseTime("created_before"); err != nil {
		return f, err
	}
	if f.CreatedAfter != nil && f.CreatedBefore != nil && f.CreatedAfter.After(*f.CreatedBefore) {
		return f, echo.NewHTTPError(http.StatusBadRequest, "created_after must not be after created_before")
	}
	if includeJobID {
		if raw := strings.TrimSpace(c.QueryParam("job_id")); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				return f, echo.NewHTTPError(http.StatusBadRequest, "invalid job_id")
			}
			f.JobID = &id
		}
	}
	return f, nil
}

func parseEventCursor(c echo.Context) (int64, error) {
	raw := strings.TrimSpace(c.Request().Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(c.QueryParam("after"))
	}
	if raw == "" {
		return 0, nil
	}
	if strings.HasPrefix(raw, "+") || strings.HasPrefix(raw, "-") {
		return 0, errors.New("signed cursor")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func jobCursorError(c echo.Context, status int, code, message string) error {
	return c.JSON(status, ErrorResponse{Error: ErrorBody{
		Code: code, Message: message,
		RequestID: c.Response().Header().Get(echo.HeaderXRequestID),
	}})
}

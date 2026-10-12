package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/video"
)

// The studio "My videos" surface (PeerTube 7.2/8.2/8.3 parity): an account-wide
// list with search and filters, and a bulk delete / set-privacy over the
// caller's OWN videos.

// maxBulkVideos caps one bulk request; each item runs the full single-video
// pipeline (federation, search outbox, storage, audit), so the cap bounds the
// work one request can queue.
const maxBulkVideos = 100

// maxMyVideosQueryLen caps ?q. Titles are capped at 200 characters, so a longer
// needle can never match.
// bulkRunTimeout bounds one detached bulk run (100 items of delete/update work).
const bulkRunTimeout = 5 * time.Minute

const maxMyVideosQueryLen = 200

// videoLifecycleStates is the videos.state set (migration 0006 + 0045 + the
// quarantine/transcoding states), as listed in the OpenAPI Video.state enum.
var videoLifecycleStates = map[string]bool{
	"draft": true, "processing": true, "scheduled": true, "quarantined": true,
	"transcoding": true, "published": true, "failed": true,
}

// handleListMyVideos serves GET /me/videos: every video on every channel the
// caller owns, filtered by q (title), privacy, state and channel_id. A
// channel_id that is not the caller's matches nothing — ownership is part of
// the query, so it can never return another account's rows.
func (s *Server) handleListMyVideos(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var fes []FieldError
	f := video.OwnerListFilter{
		Query:   strings.TrimSpace(c.QueryParam("q")),
		Privacy: c.QueryParam("privacy"),
		State:   c.QueryParam("state"),
	}
	if len([]rune(f.Query)) > maxMyVideosQueryLen {
		fes = append(fes, FieldError{Field: "q", Message: "must be at most 200 characters"})
	}
	if f.Privacy != "" && !validVideoPrivacy[f.Privacy] {
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private, password, internal"})
	}
	if f.State != "" && !videoLifecycleStates[f.State] {
		fes = append(fes, FieldError{Field: "state", Message: "unknown state"})
	}
	if raw := c.QueryParam("channel_id"); raw != "" {
		id, perr := uuid.Parse(raw)
		if perr != nil {
			fes = append(fes, FieldError{Field: "channel_id", Message: "must be a channel id"})
		} else {
			f.ChannelID = &id
		}
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.videosvc.ListByOwner(c.Request().Context(), userID, f, c.QueryParam("sort"), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoListResponse{Videos: views, pageMeta: page.meta(total)})
}

type bulkVideosRequest struct {
	VideoIDs []string `json:"video_ids"`
	Action   string   `json:"action"`
	Privacy  string   `json:"privacy"`
}

func (r bulkVideosRequest) Validate() []FieldError {
	var fes []FieldError
	switch {
	case len(r.VideoIDs) == 0:
		fes = append(fes, FieldError{Field: "video_ids", Message: "at least one video id is required"})
	case len(r.VideoIDs) > maxBulkVideos:
		fes = append(fes, FieldError{Field: "video_ids", Message: "at most 100 video ids per request"})
	default:
		seen := make(map[string]bool, len(r.VideoIDs))
		for _, raw := range r.VideoIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				fes = append(fes, FieldError{Field: "video_ids", Message: "every id must be a video id"})
				break
			}
			if seen[id.String()] {
				fes = append(fes, FieldError{Field: "video_ids", Message: "ids must be unique"})
				break
			}
			seen[id.String()] = true
		}
	}
	switch r.Action {
	case "delete":
	case "set_privacy":
		// The SAME rule the single PATCH applies to its privacy field.
		fes = append(fes, updateVideoRequest{Privacy: &r.Privacy}.Validate()...)
	default:
		fes = append(fes, FieldError{Field: "action", Message: "must be one of delete, set_privacy"})
	}
	return fes
}

type bulkVideoResult struct {
	VideoID string `json:"video_id"`
	OK      bool   `json:"ok"`
	Status  int    `json:"status"`
	Error   string `json:"error,omitempty"`
}

type bulkVideosResponse struct {
	Results   []bulkVideoResult `json:"results"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
}

// handleBulkMyVideos serves POST /me/videos/bulk. Two phases:
//
//  1. Ownership gate for the WHOLE request. Every id must be a video the caller
//     owns; otherwise the request answers 404 before anything is changed. A
//     foreign id and a nonexistent id answer identically, so the endpoint is no
//     existence oracle. Staff and collaborators get no extra reach here — bulk
//     is "my videos" only.
//  2. Each id then runs the SAME pipeline as the single-video route
//     (applyVideoDelete / applyVideoUpdate), so every side effect — federation
//     Delete/Update, the search outbox, storage cleanup, edge purge, audit —
//     fires per item. One item failing (e.g. privacy=password with no password
//     stored) is reported in its result and does not stop the others.
func (s *Server) handleBulkMyVideos(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in bulkVideosRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	ids := make([]uuid.UUID, 0, len(in.VideoIDs))
	for _, raw := range in.VideoIDs {
		id := uuid.MustParse(raw) // Validate() proved it parses
		v, gerr := s.videosvc.GetByID(ctx, id)
		if gerr != nil || v.OwnerID != userID {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		ids = append(ids, id)
	}
	// Once the gate has passed, the batch must not be cut in half by a client
	// that disconnects: a committed item whose hooks (federation Delete, search
	// suppress, IPFS unpin, storage cleanup) were dropped by a cancelled context
	// is an orphan nothing will repair. So the loop runs detached from the
	// request's cancellation, bounded by a timeout.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bulkRunTimeout)
	defer cancel()
	c.SetRequest(c.Request().WithContext(runCtx))
	out := bulkVideosResponse{Results: make([]bulkVideoResult, 0, len(ids))}
	for _, id := range ids {
		var ierr error
		switch in.Action {
		case "delete":
			ierr = s.applyVideoDelete(c, userID, role, id)
		case "set_privacy":
			p := in.Privacy
			_, ierr = s.applyVideoUpdate(c, userID, role, id, updateVideoRequest{Privacy: &p})
		}
		res := bulkVideoResult{VideoID: id.String(), OK: ierr == nil, Status: http.StatusOK}
		if ierr != nil {
			res.Status, res.Error = bulkItemError(ierr)
			if res.Status >= http.StatusInternalServerError {
				// The client only sees a generic message; the operator gets the
				// redacted cause, as the central error handler does for the
				// single routes. Never the request body, token or any address.
				s.logger.Error("bulk video item failed",
					"error", jobstatus.RedactDetail(ierr.Error()),
					"action", in.Action,
					"video_id", id.String(),
					"status", res.Status,
					"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
				)
			}
			out.Failed++
		} else {
			out.Succeeded++
		}
		out.Results = append(out.Results, res)
	}
	return c.JSON(http.StatusOK, out)
}

// bulkItemError flattens an already-mapped handler error into the per-item
// status and a client-safe message. Anything unrecognised is a 500 with a fixed
// message: a raw error could carry storage paths or SQL.
func bulkItemError(err error) (int, string) {
	var ve *ValidationError
	if errors.As(err, &ve) && len(ve.Fields) > 0 {
		return http.StatusUnprocessableEntity, ve.Fields[0].Message
	}
	var he *echo.HTTPError
	if errors.As(err, &he) {
		if msg, ok := he.Message.(string); ok {
			return he.Code, msg
		}
		return he.Code, http.StatusText(he.Code)
	}
	return http.StatusInternalServerError, "internal error"
}

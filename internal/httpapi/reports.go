package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
)

const maxReportReasonLen = 2000

// createReportRequest is the body for reporting a video or comment.
type createReportRequest struct {
	Reason string `json:"reason"`
}

func (r createReportRequest) Validate() []FieldError {
	reason := strings.TrimSpace(r.Reason)
	switch {
	case reason == "":
		return []FieldError{{Field: "reason", Message: "is required"}}
	case len(reason) > maxReportReasonLen:
		return []FieldError{{Field: "reason", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleReportVideo files a report against a public, published video. Behind
// requireAuth. A non-public/unpublished or unknown video is 404. Idempotent.
func (s *Server) handleReportVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	var in createReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.moderationsvc.ReportVideo(c.Request().Context(), userID, videoID, strings.TrimSpace(in.Reason)); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleReportComment files a report against a comment. Behind requireAuth. An
// unknown comment is 404. Idempotent.
func (s *Server) handleReportComment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	commentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	var in createReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.moderationsvc.ReportComment(c.Request().Context(), userID, commentID, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, moderation.ErrInvalidTarget) {
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// reportReporterView identifies who filed a report.
type reportReporterView struct {
	Username string `json:"username"`
}

// reportView is the moderation-queue projection of a report. Target context is
// type-dependent and omitted when not applicable.
type reportView struct {
	ID            string             `json:"id"`
	TargetType    string             `json:"target_type"`
	Reason        string             `json:"reason"`
	Status        string             `json:"status"`
	ModeratorNote string             `json:"moderator_note"`
	CreatedAt     time.Time          `json:"created_at"`
	ResolvedAt    *time.Time         `json:"resolved_at,omitempty"`
	Reporter      reportReporterView `json:"reporter"`
	VideoID       string             `json:"video_id,omitempty"`
	VideoTitle    string             `json:"video_title,omitempty"`
	CommentID     string             `json:"comment_id,omitempty"`
	CommentBody   string             `json:"comment_body,omitempty"`
}

func newReportView(it moderation.Item) reportView {
	return reportView{
		ID:            it.ID.String(),
		TargetType:    it.TargetType,
		Reason:        it.Reason,
		Status:        it.Status,
		ModeratorNote: it.ModeratorNote,
		CreatedAt:     it.CreatedAt,
		ResolvedAt:    it.ResolvedAt,
		Reporter:      reportReporterView{Username: it.ReporterUsername},
		VideoID:       it.VideoID,
		VideoTitle:    it.VideoTitle,
		CommentID:     it.CommentID,
		CommentBody:   it.CommentBody,
	}
}

// reportListResponse is the paginated moderation queue.
type reportListResponse struct {
	Reports []reportView `json:"reports"`
	Limit   int          `json:"limit"`
	Offset  int          `json:"offset"`
}

// handleListReports returns the moderation queue. Behind requireRole(admin,
// moderator). ?status=open returns only unresolved reports; pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListReports(c echo.Context) error {
	openOnly := c.QueryParam("status") == "open"
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.moderationsvc.List(c.Request().Context(), openOnly, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]reportView, 0, len(items))
	for _, it := range items {
		views = append(views, newReportView(it))
	}
	return c.JSON(http.StatusOK, reportListResponse{Reports: views, Limit: limit, Offset: offset})
}

// resolveReportRequest is the body for resolving a report.
type resolveReportRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (r resolveReportRequest) Validate() []FieldError {
	var fes []FieldError
	if r.Status != moderation.StatusAccepted && r.Status != moderation.StatusRejected {
		fes = append(fes, FieldError{Field: "status", Message: "must be 'accepted' or 'rejected'"})
	}
	if len(r.Note) > maxReportReasonLen {
		fes = append(fes, FieldError{Field: "note", Message: "must be at most 2000 characters"})
	}
	return fes
}

// handleResolveReport accepts/rejects a report with an internal note. Behind
// requireRole(admin, moderator). An unknown id is 404. Emits an audit event.
func (s *Server) handleResolveReport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "report not found")
	}
	var in resolveReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.moderationsvc.Resolve(c.Request().Context(), userID, id, in.Status, strings.TrimSpace(in.Note)); err != nil {
		if errors.Is(err, moderation.ErrNotFound) {
			s.audit(c, observability.ActionReportResolve, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "report not found")
		}
		return err
	}
	s.audit(c, observability.ActionReportResolve, observability.ResultSuccess, userID.String(), in.Status)
	return c.NoContent(http.StatusNoContent)
}

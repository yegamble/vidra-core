package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
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

// reportEmailAlertsAvailable is the EFFECTIVE report-email gate: the runtime
// setting AND an outbound mail path AND a configured operator contact address
// (an alert nobody can receive is not "on"). Mirrors contactFormAvailable.
func (s *Server) reportEmailAlertsAvailable() bool {
	return s.contactMailer != nil &&
		strings.TrimSpace(s.effectiveContactEmail()) != "" &&
		s.settingBool(instancesettings.KeyReportEmailAlertsEnabled, true)
}

// notifyStaffOfReport pushes both halves of the moderation queue's new-report
// signal for a genuinely new report (reportID != uuid.Nil — an idempotent
// repeat never notifies): the in-app new_report staff fan-out, and the
// operator email alert when reportEmailAlertsAvailable. Both are best-effort:
// a notification or mail failure must never fail the report itself.
func (s *Server) notifyStaffOfReport(c echo.Context, reportID uuid.UUID, targetType, reason string) {
	if reportID == uuid.Nil {
		return
	}
	ctx := c.Request().Context()
	if s.notifsvc != nil {
		if _, err := s.notifsvc.NotifyNewReport(ctx, reportID); err != nil {
			s.logger.WarnContext(ctx, "notify staff of new report failed", "error", err, "report_id", reportID)
		}
	}
	if s.reportEmailAlertsAvailable() {
		queueURL := ""
		if base := strings.TrimRight(s.cfg.PublicBaseURL, "/"); base != "" {
			queueURL = base + "/admin"
		}
		if err := s.contactMailer.SendNewReportAlert(ctx, strings.TrimSpace(s.effectiveContactEmail()),
			targetType, reason, queueURL); err != nil {
			// The mail package's error text never carries addresses or the body.
			s.logger.WarnContext(ctx, "report email alert failed", "error", err, "report_id", reportID)
		}
	}
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
	reportID, err := s.moderationsvc.ReportVideo(c.Request().Context(), userID, videoID, strings.TrimSpace(in.Reason))
	if err != nil {
		return err
	}
	s.notifyStaffOfReport(c, reportID, moderation.TargetVideo, strings.TrimSpace(in.Reason))
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
	reportID, err := s.moderationsvc.ReportComment(c.Request().Context(), userID, commentID, strings.TrimSpace(in.Reason))
	if err != nil {
		if errors.Is(err, moderation.ErrInvalidTarget) {
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		}
		return err
	}
	s.notifyStaffOfReport(c, reportID, moderation.TargetComment, strings.TrimSpace(in.Reason))
	return c.NoContent(http.StatusNoContent)
}

// handleReportAccount files a report against another user account. Behind
// requireAuth. Reporting yourself is 422; an unknown account is 404. Idempotent.
func (s *Server) handleReportAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "account not found")
	}
	var in createReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	reportID, err := s.moderationsvc.ReportAccount(c.Request().Context(), userID, targetID, strings.TrimSpace(in.Reason))
	if err != nil {
		switch {
		case errors.Is(err, moderation.ErrCannotReportSelf):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot report yourself")
		case errors.Is(err, moderation.ErrInvalidTarget):
			return echo.NewHTTPError(http.StatusNotFound, "account not found")
		}
		return err
	}
	s.notifyStaffOfReport(c, reportID, moderation.TargetAccount, strings.TrimSpace(in.Reason))
	return c.NoContent(http.StatusNoContent)
}

// reportReporterView identifies who filed a report.
type reportReporterView struct {
	Username string `json:"username"`
}

// reportView is the moderation-queue projection of a report. Target context is
// type-dependent and omitted when not applicable.
type reportView struct {
	ID               string             `json:"id"`
	TargetType       string             `json:"target_type"`
	Reason           string             `json:"reason"`
	Status           string             `json:"status"`
	ModeratorNote    string             `json:"moderator_note"`
	CreatedAt        time.Time          `json:"created_at"`
	ResolvedAt       *time.Time         `json:"resolved_at,omitempty"`
	Reporter         reportReporterView `json:"reporter"`
	VideoID          string             `json:"video_id,omitempty"`
	VideoTitle       string             `json:"video_title,omitempty"`
	CommentID        string             `json:"comment_id,omitempty"`
	CommentBody      string             `json:"comment_body,omitempty"`
	ReportedUserID   string             `json:"reported_user_id,omitempty"`
	ReportedUsername string             `json:"reported_username,omitempty"`
	// Remote-video target context (target_type='remote_video').
	RemoteVideoID     string `json:"remote_video_id,omitempty"`
	RemoteVideoTitle  string `json:"remote_video_title,omitempty"`
	RemoteVideoDomain string `json:"remote_video_domain,omitempty"`
}

func newReportView(it moderation.Item) reportView {
	return reportView{
		ID:                it.ID.String(),
		TargetType:        it.TargetType,
		Reason:            it.Reason,
		Status:            it.Status,
		ModeratorNote:     it.ModeratorNote,
		CreatedAt:         it.CreatedAt,
		ResolvedAt:        it.ResolvedAt,
		Reporter:          reportReporterView{Username: it.ReporterUsername},
		VideoID:           it.VideoID,
		VideoTitle:        it.VideoTitle,
		CommentID:         it.CommentID,
		CommentBody:       it.CommentBody,
		ReportedUserID:    it.ReportedUserID,
		ReportedUsername:  it.ReportedUsername,
		RemoteVideoID:     it.RemoteVideoID,
		RemoteVideoTitle:  it.RemoteVideoTitle,
		RemoteVideoDomain: it.RemoteVideoDomain,
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
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, err := s.moderationsvc.List(c.Request().Context(), openOnly, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]reportView, 0, len(items))
	for _, it := range items {
		views = append(views, newReportView(it))
	}
	return c.JSON(http.StatusOK, reportListResponse{Reports: views, Limit: page.Limit, Offset: page.Offset})
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
	ctx := c.Request().Context()
	reporterID, err := s.moderationsvc.Resolve(ctx, userID, id, in.Status, strings.TrimSpace(in.Note))
	if err != nil {
		if errors.Is(err, moderation.ErrNotFound) {
			s.audit(c, observability.ActionReportResolve, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "report not found")
		}
		return err
	}
	s.audit(c, observability.ActionReportResolve, observability.ResultSuccess, userID.String(), in.Status)
	// Tell the reporter their report was handled (best-effort; skipped when no
	// notifier is wired or the resolving moderator reported it themselves).
	if s.notifsvc != nil {
		if nerr := s.notifsvc.NotifyReportResolved(ctx, reporterID, userID, id); nerr != nil {
			s.logger.WarnContext(ctx, "notify report resolved failed", "error", nerr, "report_id", id)
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleDeleteReport hard-deletes a report row. Behind requireRole(admin) only —
// moderators resolve reports, admins can purge them. Idempotent like the other
// admin deletes (deleting an unknown id still succeeds); a malformed id is 404.
// Any notification referencing the report cascades away. Emits an audit event.
func (s *Server) handleDeleteReport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "report not found")
	}
	if err := s.moderationsvc.Delete(c.Request().Context(), id); err != nil {
		return err
	}
	s.audit(c, observability.ActionReportDelete, observability.ResultSuccess, userID.String(), "report="+id.String())
	return c.NoContent(http.StatusNoContent)
}

package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
)

// reportMessageView is one message in a report's thread. AuthorUsername is
// filled for staff viewers only: the reporter is never told which moderator
// wrote, the same rule report_resolved follows.
type reportMessageView struct {
	ID             string    `json:"id"`
	Body           string    `json:"body"`
	ByModerator    bool      `json:"by_moderator"`
	IsMine         bool      `json:"is_mine"`
	AuthorUsername string    `json:"author_username,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func newReportMessageView(m moderation.Message, viewerID uuid.UUID, staff bool) reportMessageView {
	v := reportMessageView{ID: m.ID.String(), Body: m.Body, ByModerator: m.ByModerator,
		IsMine: m.AuthorID == viewerID, CreatedAt: m.CreatedAt}
	if staff {
		v.AuthorUsername = m.AuthorUsername
	}
	return v
}

// reportMessageListResponse is a page of a report's thread, oldest first.
// CanReply is false for a report another instance filed: there is no local
// reporter to write to, so the thread is read-only.
type reportMessageListResponse struct {
	Messages []reportMessageView `json:"messages"`
	CanReply bool                `json:"can_reply"`
	pageMeta
}

// createReportMessageRequest is the body for posting to a report's thread.
type createReportMessageRequest struct {
	Body string `json:"body"`
}

func (r createReportMessageRequest) Validate() []FieldError {
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case utf8.RuneCountInString(body) > moderation.MaxMessageLen:
		return []FieldError{{Field: "body", Message: "must be at most 3000 characters"}}
	}
	return nil
}

// reportThreadError maps the thread's sentinel errors. Not-yours and
// no-such-report are the SAME 404, so the thread never confirms that a report
// exists to someone who may not see it.
func reportThreadError(err error) error {
	switch {
	case errors.Is(err, moderation.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "report not found")
	case errors.Is(err, moderation.ErrNoLocalReporter):
		return echo.NewHTTPError(http.StatusConflict, "this report was filed by another instance; there is no local reporter to message")
	}
	return err
}

// handleListReportMessages returns a page of a report's thread. Behind
// requireAuth; open to the report's local reporter and to admins/moderators.
func (s *Server) handleListReportMessages(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "report not found")
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	th, err := s.moderationsvc.Messages(c.Request().Context(), id, userID, isStaff(role), page.Limit32(), page.Offset32())
	if err != nil {
		return reportThreadError(err)
	}
	views := make([]reportMessageView, 0, len(th.Messages))
	for _, m := range th.Messages {
		views = append(views, newReportMessageView(m, userID, isStaff(role)))
	}
	return c.JSON(http.StatusOK, reportMessageListResponse{Messages: views, CanReply: th.CanReply, pageMeta: page.meta(th.Total)})
}

// handleCreateReportMessage posts to a report's thread and tells the other side
// (best-effort). Access is handleListReportMessages'; a remote Flag report is
// 409. A staff message is audited by id — never the body, which no log line
// carries either.
func (s *Server) handleCreateReportMessage(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "report not found")
	if err != nil {
		return err
	}
	var in createReportMessageRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	msg, err := s.moderationsvc.PostMessage(ctx, id, userID, isStaff(role), strings.TrimSpace(in.Body))
	if err != nil {
		return reportThreadError(err)
	}
	if msg.ByModerator {
		s.auditEvent(c, audit.Event{
			Action: observability.ActionReportMessage, Result: observability.ResultSuccess,
			ActorID: userID.String(), ResourceType: "report", ResourceID: id.String(), Reason: "message=" + msg.ID.String(),
		})
	}
	if s.notifsvc != nil {
		if _, nerr := s.notifsvc.NotifyReportMessage(ctx, msg.ID); nerr != nil {
			s.logger.WarnContext(ctx, "notify report message failed", "error", nerr, "report_id", id, "message_id", msg.ID)
		}
	}
	return c.JSON(http.StatusCreated, newReportMessageView(msg, userID, isStaff(role)))
}

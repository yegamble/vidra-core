package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
)

// auditLogEntryView is the admin projection of a durable audit-log row. It never
// carries secrets/PII; actor_username is resolved best-effort and omitted when
// the account is unknown/deleted/unauthenticated.
type auditLogEntryView struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	Result        string    `json:"result"`
	ActorID       string    `json:"actor_id,omitempty"`
	ActorUsername string    `json:"actor_username,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	RequestID     string    `json:"request_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
}

func newAuditLogEntryView(e audit.Entry) auditLogEntryView {
	return auditLogEntryView{
		ID:            e.ID.String(),
		Action:        e.Action,
		Result:        e.Result,
		ActorID:       e.ActorID,
		ActorUsername: e.ActorUsername,
		Reason:        e.Reason,
		RequestID:     e.RequestID,
		OccurredAt:    e.OccurredAt,
	}
}

// auditLogListResponse is the paginated audit view.
type auditLogListResponse struct {
	Entries []auditLogEntryView `json:"entries"`
	Limit   int                 `json:"limit"`
	Offset  int                 `json:"offset"`
}

// handleListAuditLog returns the durable security audit trail, newest first.
// Behind requireRole(admin). ?action filters by a specific action id; pagination
// via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListAuditLog(c echo.Context) error {
	action := c.QueryParam("action")
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	entries, err := s.auditLog.List(c.Request().Context(), action, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]auditLogEntryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, newAuditLogEntryView(e))
	}
	return c.JSON(http.StatusOK, auditLogListResponse{Entries: views, Limit: limit, Offset: offset})
}

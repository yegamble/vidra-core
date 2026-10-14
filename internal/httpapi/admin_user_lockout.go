package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// handleAdminClearLoginLockout drops an account's failed-login counters and
// locks (DELETE /admin/users/{id}/login-lockout). Behind requireRole(admin).
// 204 whether or not a lockout existed — the answer must not become a probe for
// "has someone been guessing this account" beyond what the admin can already
// read from the audit log — and 404 only for an unknown user. Audited.
func (s *Server) handleAdminClearLoginLockout(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
	}
	cleared, err := s.authsvc.ClearLoginLockout(c.Request().Context(), targetID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return err
	}
	outcome := "nothing_to_clear"
	if cleared {
		outcome = "cleared"
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionAdminLoginLockoutClear, Result: observability.ResultSuccess,
		ActorID: callerID.String(), Reason: "target=" + targetID.String(),
		ResourceType: auditResourceUser, ResourceID: targetID.String(),
		Metadata: []audit.MetadataField{{Key: "outcome", Value: outcome}},
	})
	return c.NoContent(http.StatusNoContent)
}

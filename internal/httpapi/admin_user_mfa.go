package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// Administrator removal of a user's second factor (A05 ruling 2). Before this
// there was no operator answer at all to "I lost my phone and my recovery
// codes": TOTP removal required the account's own password AND a session, and
// the account that needs help is precisely the one that can no longer sign in.
// The recovery of last resort was a database edit.
//
// The action removes protection; it never grants access. Nothing secret is read
// or returned — the response is 204, the secret and the recovery codes are
// DELETED rather than disclosed, and no admin route anywhere returns them — so
// an admin cannot use this to impersonate a user's second factor. What they can
// do is reduce the account to its password, which is why it re-verifies the
// ADMINISTRATOR's own password, is audited with the target named, mails the
// user, and signs the user's sessions out.

// adminRemoveUserMFARequest is the DELETE /admin/users/{id}/mfa body. The
// password is the CALLER's, not the target's: the target cannot supply theirs
// (that is the situation this exists for), so the acting admin is the party who
// must prove possession — the same re-authentication a self-service removal
// asks for, moved to the only person who can answer it.
type adminRemoveUserMFARequest struct {
	Password string `json:"password"`
}

func (r adminRemoveUserMFARequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

// handleAdminRemoveUserMFA removes a target account's second factor. Behind
// requireRole(admin). 204 on success; a wrong admin password is 403; an unknown
// target, or one with no two-factor configuration to remove, is 404. Removing
// your OWN second factor here is allowed and audited: an owner who loses their
// authenticator has nobody above them to ask.
func (s *Server) handleAdminRemoveUserMFA(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
	}
	var in adminRemoveUserMFARequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	err = s.authsvc.AdminRemoveTOTP(c.Request().Context(), callerID, in.Password, targetID, sessionIDFromContext(c))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			s.auditAdminUserRefusal(c, observability.ActionAdminMFAReset, callerID, targetID, "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrPasswordNotSet):
			// An admin who signs in only through OIDC/ATProto has no password to
			// re-verify, so they cannot take this action at all. Say so instead
			// of answering "incorrect password" they could never satisfy.
			s.auditAdminUserRefusal(c, observability.ActionAdminMFAReset, callerID, targetID, "password_not_set")
			return echo.NewHTTPError(http.StatusConflict,
				"your account has no password to confirm this with: set one through the password reset flow first")
		case errors.Is(err, auth.ErrMFANotEnabled):
			return echo.NewHTTPError(http.StatusNotFound, "this account has no two-factor authentication to remove")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		case errors.Is(err, auth.ErrMFAUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	// The structured envelope, not prose: mfa_enabled true -> false against the
	// target, so a consumer reads what changed without parsing a sentence.
	s.auditEvent(c, audit.Event{
		Action: observability.ActionAdminMFAReset, Result: observability.ResultSuccess,
		ActorID: callerID.String(), Reason: "target=" + targetID.String(),
		ResourceType: auditResourceUser, ResourceID: targetID.String(),
		Changes: []audit.Change{{Field: "mfa_enabled", Before: "true", After: "false"}},
	})
	return c.NoContent(http.StatusNoContent)
}

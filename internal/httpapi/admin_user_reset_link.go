package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// adminPasswordResetLinkRequest carries the CALLER's password, exactly as
// DELETE /admin/users/{id}/mfa does: the locked-out target cannot supply theirs.
type adminPasswordResetLinkRequest struct {
	Password string `json:"password"`
}

func (r adminPasswordResetLinkRequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

type adminPasswordResetLinkResponse struct {
	ResetURL  string    `json:"reset_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// resetLinkRefusals maps the service's refusals to the house statuses; each is
// audited as a failure with its code, like the neighbouring admin user routes.
var resetLinkRefusals = []struct {
	err    error
	status int
	code   string
	msg    string
}{
	{auth.ErrInvalidPassword, http.StatusForbidden, "invalid_password", "incorrect password"},
	{auth.ErrPasswordNotSet, http.StatusConflict, "password_not_set", "your account has no password to confirm this with: set one through the password reset flow first"},
	{auth.ErrResetTargetInactive, http.StatusConflict, "target_inactive", "that account is deactivated: reactivate it before issuing a reset link"},
	{auth.ErrResetTargetOwner, http.StatusForbidden, "target_owner", "the instance owner recovers through the host (`vidra owner reset`), not an admin link"},
	{auth.ErrResetTargetStaff, http.StatusForbidden, "target_staff", "only ordinary accounts can be given a reset link here; staff recover through the owner"},
}

// handleAdminPasswordResetLink mints a one-time reset link for a locked-out
// ordinary user: the recovery path when no SMTP is configured. Behind
// requireRole(admin) and the strict auth limiter (a password makes it a guessing
// surface).
//
// FAIL-CLOSED, like the host-side owner-recovery: the durable audit row is
// written BEFORE the link is returned, and a failed write withholds the link AND
// withdraws the token just minted, so it cannot be redeemed unseen. Neither the
// token nor the link is ever logged or audited; the response body is the only
// place it exists.
func (s *Server) handleAdminPasswordResetLink(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
	}
	var in adminPasswordResetLinkRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// A link on a guessed host looks redeemable and is not: refuse before minting.
	base := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/")
	if base == "" {
		return echo.NewHTTPError(http.StatusConflict, "PUBLIC_BASE_URL is not set, so there is no origin to build the link on")
	}
	ctx := c.Request().Context()
	// The service checks the caller's password BEFORE looking the target up, so a
	// caller without it learns nothing about which ids exist.
	link, err := s.authsvc.AdminIssueResetLink(ctx, callerID, in.Password, targetID)
	if errors.Is(err, auth.ErrAccountNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	for _, r := range resetLinkRefusals {
		if errors.Is(err, r.err) {
			s.auditAdminUserRefusal(c, observability.ActionAdminPasswordResetLink, callerID, targetID, r.code)
			return echo.NewHTTPError(r.status, r.msg)
		}
	}
	if err != nil {
		return err
	}
	auditErr := errors.New("no durable audit log is wired")
	if s.auditLog != nil {
		auditErr = s.auditEventErr(c, audit.Event{
			Action: observability.ActionAdminPasswordResetLink, Result: observability.ResultSuccess,
			ActorID: callerID.String(), Reason: "target=" + targetID.String(),
			ResourceType: auditResourceUser, ResourceID: targetID.String(),
		})
	}
	if auditErr != nil {
		if werr := s.authsvc.AdminWithdrawResetLink(ctx, targetID); werr != nil {
			s.logger.ErrorContext(ctx, "reset link withheld but its token could not be withdrawn", "error", werr, "target", targetID.String())
		}
		s.logger.ErrorContext(ctx, "reset link withheld: audit write failed", "error", auditErr, "target", targetID.String())
		return echo.NewHTTPError(http.StatusServiceUnavailable, "the action could not be written to the audit log, so no reset link was issued")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, adminPasswordResetLinkResponse{
		ResetURL:  base + "/reset-password/confirm?token=" + url.QueryEscape(link.Token),
		ExpiresAt: link.ExpiresAt,
	})
}

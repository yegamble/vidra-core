package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// adminCreateUserRequest is the POST /admin/users body. There is deliberately no
// account password: the new holder sets theirs through a one-time link. Password
// is the CALLING administrator's own, the same step-up as the reset-link route.
type adminCreateUserRequest struct {
	Username          string `json:"username"`
	Email             string `json:"email"`
	Role              string `json:"role"`
	StorageQuotaBytes *int64 `json:"storage_quota_bytes"`
	Password          string `json:"password"`
}

func (r adminCreateUserRequest) Validate() []FieldError {
	var fes []FieldError
	// Same username/email rules as signup (registerRequest.Validate).
	name := strings.TrimSpace(r.Username)
	switch {
	case name == "":
		fes = append(fes, FieldError{Field: "username", Message: "is required"})
	case len(name) < 3 || len(name) > 30:
		fes = append(fes, FieldError{Field: "username", Message: "must be 3–30 characters"})
	case usernameHasReservedChars(name):
		fes = append(fes, FieldError{Field: "username", Message: usernameReservedCharsMessage})
	}
	if !looksLikeEmail(r.Email) {
		fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
	}
	if !admin.ValidRole(r.Role) {
		fes = append(fes, FieldError{Field: "role", Message: "must be one of user, moderator, admin"})
	}
	if r.StorageQuotaBytes != nil && *r.StorageQuotaBytes < 0 {
		fes = append(fes, FieldError{Field: "storage_quota_bytes", Message: "must be a non-negative integer (bytes; 0 = unlimited) or null"})
	}
	if r.Password == "" {
		fes = append(fes, FieldError{Field: "password", Message: "is required"})
	}
	return fes
}

// adminCreateUserResponse: LinkEmailed true means the set-password link went to
// the new address and ResetURL is absent; otherwise ResetURL is the one-time
// link, shown once.
type adminCreateUserResponse struct {
	User        adminUserView `json:"user"`
	LinkEmailed bool          `json:"link_emailed"`
	ResetURL    string        `json:"reset_url,omitempty"`
	ExpiresAt   time.Time     `json:"expires_at"`
}

// handleAdminCreateUser creates an account on behalf of an administrator
// (PeerTube POST /users parity), for instances with registration closed.
// Behind the step-up chain (strict limiter + requireAuth + requireRole(admin)).
//
// Email is marked VERIFIED: the administrator is vouching for the address (the
// PeerTube convention, and the same effect as the existing email_verified admin
// override), and the registration verification hold would otherwise lock out an
// account that has no password yet.
//
// The link follows the reset-link route's posture: when no mail path exists it
// is returned once, no-store, and only after the durable audit row is written
// (fail-closed). When mail is configured the link is emailed instead and never
// appears in the response. The audit row is checked on BOTH paths, and a failure
// at any later step deactivates the new account rather than leave a password-less
// (possibly staff) account that nothing can give a password.
func (s *Server) handleAdminCreateUser(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in adminCreateUserRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	mailed := s.mailPathConfigured()
	base := strings.TrimRight(strings.TrimSpace(s.cfg.PublicBaseURL), "/")
	if !mailed && base == "" {
		return echo.NewHTTPError(http.StatusConflict, "PUBLIC_BASE_URL is not set, so there is no origin to build the link on")
	}
	user, err := s.authsvc.AdminCreateAccount(ctx, callerID, in.Password, auth.AdminCreateAccountInput{
		Username: in.Username, Email: in.Email, Role: in.Role,
	})
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		s.audit(c, observability.ActionAdminUserCreate, observability.ResultFailure, callerID.String(), "invalid_password")
		return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
	case errors.Is(err, auth.ErrPasswordNotSet):
		s.audit(c, observability.ActionAdminUserCreate, observability.ResultFailure, callerID.String(), "password_not_set")
		return echo.NewHTTPError(http.StatusConflict, "your account has no password to confirm this with: set one through the password reset flow first")
	case errors.Is(err, auth.ErrOwnerClaimRequired):
		return &OwnerClaimRequiredError{}
	case errors.Is(err, auth.ErrHandleReserved):
		return &HandleReservedError{}
	case errors.Is(err, auth.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
	case err != nil:
		return err
	}
	s.invalidateUserCount()
	// Audited the moment the account exists, so a later failure never leaves an
	// unrecorded account. Neither the address nor any credential is recorded.
	auditErr := errors.New("no durable audit log is wired")
	if s.auditLog != nil {
		auditErr = s.auditEventErr(c, audit.Event{
			Action: observability.ActionAdminUserCreate, Result: observability.ResultSuccess,
			ActorID: callerID.String(), Reason: "target=" + user.ID.String() + " role=" + in.Role,
			ResourceType: auditResourceUser, ResourceID: user.ID.String(),
		})
	}
	// Every failure from here leaves NO usable account: a password-less staff
	// account would be unreachable (the reset-link route refuses staff) while
	// still counting as an active admin. So the account is deactivated instead.
	discard := func(reason string, cause error) {
		if derr := s.authsvc.AdminDiscardAccount(ctx, user.ID); derr != nil {
			s.logger.ErrorContext(ctx, "refused admin-created account could not be deactivated", "error", derr, "target", user.ID.String())
		}
		s.logger.ErrorContext(ctx, "admin account creation rolled back: "+reason, "error", cause, "target", user.ID.String())
	}
	// Checked BEFORE any delivery path, emailed or returned: no durable record,
	// no account.
	if auditErr != nil {
		discard("audit write failed", auditErr)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "the action could not be written to the audit log, so the account was not created")
	}
	// Quota and the admin-vouched verification ride the existing admin edit path.
	verified := true
	upd := admin.UpdateUserInput{EmailVerified: &verified}
	if in.StorageQuotaBytes != nil {
		upd.SetStorageQuota, upd.StorageQuotaBytes = true, in.StorageQuotaBytes
	}
	updated, err := s.adminsvc.UpdateUser(ctx, callerID, user.ID, upd)
	if err != nil {
		discard("update failed", err)
		return err
	}
	user = updated
	link, err := s.authsvc.AdminIssueSetPasswordLink(ctx, user.ID)
	if err != nil {
		discard("link issuance failed", err)
		return err
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	resp := adminCreateUserResponse{User: newAdminUserView(user, 0, false), ExpiresAt: link.ExpiresAt}
	if mailed {
		merr := s.authsvc.AdminSendSetPasswordLink(ctx, user.Email, link.Token)
		if merr == nil {
			resp.LinkEmailed = true
			return c.JSON(http.StatusCreated, resp)
		}
		if base == "" {
			// No relay and no origin to hand the link over on: nothing to give.
			discard("set-password mail failed", merr)
			return echo.NewHTTPError(http.StatusBadGateway, "the set-password email could not be sent, so the account was not created")
		}
		s.logger.WarnContext(ctx, "set-password mail failed; returning the link instead", "target", user.ID.String())
	}
	resp.ResetURL = base + "/reset-password/confirm?token=" + url.QueryEscape(link.Token)
	return c.JSON(http.StatusCreated, resp)
}

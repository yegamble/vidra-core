package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// audit emits a security audit event for the current request, pulling the
// request ID from the response headers. See internal/observability and
// .ralph/specs/observability.md. actorID/reason may be empty.
func (s *Server) audit(c echo.Context, action, result, actorID, reason string) {
	ctx := c.Request().Context()
	reqID := c.Response().Header().Get(echo.HeaderXRequestID)
	observability.Audit(ctx, s.logger, observability.AuditEvent{
		Action:    action,
		Result:    result,
		ActorID:   actorID,
		RequestID: reqID,
		Reason:    reason,
	})
	// Persist the durable audit trail best-effort when wired; a failure never
	// blocks the request (the slog line above is still emitted).
	if s.auditLog != nil {
		if err := s.auditLog.Record(ctx, audit.Event{
			Action:    action,
			Result:    result,
			ActorID:   actorID,
			Reason:    reason,
			RequestID: reqID,
		}); err != nil {
			s.logger.WarnContext(ctx, "audit log persist failed", "error", err, "action", action)
		}
	}
}

// registerRequest is the POST /api/v1/auth/register body.
type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Note is an optional message to moderators, used only when the instance
	// requires registration approval; ignored otherwise.
	Note string `json:"note,omitempty"`
	// CookieMode opts the new session into cookie mode: the refresh token is
	// set as an httpOnly vidra_refresh cookie and omitted from the body.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

// maxRegistrationNoteLen bounds the optional applicant note.
const maxRegistrationNoteLen = 2000

// bcrypt silently truncates input beyond 72 bytes, so we cap password length to
// avoid a surprising security cliff.
const maxPasswordLen = 72

func (r registerRequest) Validate() []FieldError {
	var fes []FieldError
	name := strings.TrimSpace(r.Username)
	switch {
	case name == "":
		fes = append(fes, FieldError{Field: "username", Message: "is required"})
	case len(name) < 3 || len(name) > 30:
		fes = append(fes, FieldError{Field: "username", Message: "must be 3–30 characters"})
	}
	if !looksLikeEmail(r.Email) {
		fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
	}
	switch {
	case len(r.Password) < 8:
		fes = append(fes, FieldError{Field: "password", Message: "must be at least 8 characters"})
	case len(r.Password) > maxPasswordLen:
		fes = append(fes, FieldError{Field: "password", Message: "must be at most 72 characters"})
	}
	if len(r.Note) > maxRegistrationNoteLen {
		fes = append(fes, FieldError{Field: "note", Message: "must be at most 2000 characters"})
	}
	return fes
}

// loginRequest is the POST /api/v1/auth/login body.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// CookieMode opts the new session into cookie mode: the refresh token is
	// set as an httpOnly vidra_refresh cookie and omitted from the body.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

func (r loginRequest) Validate() []FieldError {
	var fes []FieldError
	if !looksLikeEmail(r.Email) {
		fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
	}
	if r.Password == "" {
		fes = append(fes, FieldError{Field: "password", Message: "is required"})
	}
	return fes
}

// looksLikeEmail is a deliberately lax structural check: exactly one "@" with
// non-empty local and domain parts and a dot in the domain. Real deliverability
// is proven by the verification flow, not by a regex.
func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}

// userView is the public projection of a user — never includes the password hash.
type userView struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name"`
	Bio           string    `json:"bio"`
	CreatedAt     time.Time `json:"created_at"`
}

func newUserView(u sqlcgen.User) userView {
	return userView{
		ID:            u.ID.String(),
		Username:      u.Username,
		Email:         u.Email,
		Role:          u.Role,
		EmailVerified: u.EmailVerified,
		DisplayName:   u.DisplayName,
		Bio:           u.Bio,
		CreatedAt:     u.CreatedAt,
	}
}

// authResponse is returned by register and login. RefreshToken is omitted in
// cookie mode, where the httpOnly vidra_refresh cookie is the sole carrier.
type authResponse struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	User         userView `json:"user"`
}

func (s *Server) authResponse(status int, c echo.Context, user sqlcgen.User, tokens auth.Tokens, cookieMode bool) error {
	resp := authResponse{
		Token:        tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.authTTL.Seconds()),
		User:         newUserView(user),
	}
	if cookieMode {
		// The cookie is the sole refresh-token carrier in cookie mode: set the
		// rotated token httpOnly and keep the raw value out of the JSON body so
		// it never has to touch JavaScript-accessible storage.
		s.setRefreshCookie(c, tokens.RefreshToken)
		resp.RefreshToken = ""
	}
	return c.JSON(status, resp)
}

// handleRegister creates an account and returns it with an access + refresh token.
func (s *Server) handleRegister(c echo.Context) error {
	if !s.cfg.RegistrationEnabled {
		return echo.NewHTTPError(http.StatusForbidden, "registration is disabled on this instance")
	}
	var in registerRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	regInput := auth.RegisterInput{Username: in.Username, Email: in.Email, Password: in.Password}

	// When the instance requires approval, signup files a pending request instead
	// of creating an account + session. The response reveals no token.
	if s.cfg.RegistrationRequireApproval {
		if _, err := s.authsvc.RequestRegistration(c.Request().Context(), regInput, in.Note); err != nil {
			if errors.Is(err, auth.ErrConflict) {
				return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
			}
			return err
		}
		s.audit(c, observability.ActionRegistrationRequest, observability.ResultSuccess, "", "pending_approval")
		return c.JSON(http.StatusAccepted, map[string]string{"status": "pending"})
	}

	user, tokens, err := s.authsvc.Register(c.Request().Context(), regInput, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	s.audit(c, observability.ActionRegister, observability.ResultSuccess, user.ID.String(), "")
	cookieMode := in.CookieMode || refreshCookieToken(c) != ""
	return s.authResponse(http.StatusCreated, c, user, tokens, cookieMode)
}

// handleMe returns the authenticated account. It runs behind requireAuth, so the
// principal is always present; it reloads the user so the response reflects the
// current database state (role, email_verified, …) rather than stale token claims.
func (s *Server) handleMe(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	user, err := s.authsvc.UserByID(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	return c.JSON(http.StatusOK, newUserView(user))
}

// updateProfileRequest is the PATCH /api/v1/auth/me body. Fields are optional;
// only those present are changed. Identity fields (username, email) are not
// editable here.
type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Bio         *string `json:"bio"`
}

func (r updateProfileRequest) Validate() []FieldError {
	var fes []FieldError
	if r.DisplayName == nil && r.Bio == nil {
		return []FieldError{{Field: "display_name", Message: "at least one of display_name, bio is required"}}
	}
	if r.DisplayName != nil && len(strings.TrimSpace(*r.DisplayName)) > 50 {
		fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
	}
	if r.Bio != nil && len(*r.Bio) > 1000 {
		fes = append(fes, FieldError{Field: "bio", Message: "must be at most 1000 characters"})
	}
	return fes
}

// handleUpdateMe updates the authenticated account's profile (display name, bio).
func (s *Server) handleUpdateMe(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in updateProfileRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, err := s.authsvc.UpdateProfile(c.Request().Context(), userID, auth.ProfileInput{
		DisplayName: in.DisplayName,
		Bio:         in.Bio,
	})
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	return c.JSON(http.StatusOK, newUserView(user))
}

// handleLogin verifies credentials and returns an access + refresh token.
func (s *Server) handleLogin(c echo.Context) error {
	var in loginRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, tokens, err := s.authsvc.Login(c.Request().Context(), auth.LoginInput{
		Email:    in.Email,
		Password: in.Password,
	}, c.Request().UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			// No actor_id: the account is unknown or the password was wrong, and
			// we must not leak which (or the attempted email — it is PII).
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "invalid_credentials")
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, auth.ErrAccountDisabled):
			// Login returns an empty user on this path, so no actor_id is available.
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "account_disabled")
			return echo.NewHTTPError(http.StatusForbidden, "account is disabled")
		}
		return err
	}
	s.audit(c, observability.ActionLogin, observability.ResultSuccess, user.ID.String(), "")
	cookieMode := in.CookieMode || refreshCookieToken(c) != ""
	return s.authResponse(http.StatusOK, c, user, tokens, cookieMode)
}

// refreshRequest is the POST /api/v1/auth/refresh and /logout body. The token
// may be omitted when the request carries the vidra_refresh cookie (cookie
// mode); presence of one or the other is enforced in the handler after cookie
// resolution.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	// CookieMode opts the rotated session into cookie mode even when the token
	// arrived in the body. A cookie-supplied token implies cookie mode.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

// errRefreshTokenRequired is the 422 returned when neither the body nor the
// vidra_refresh cookie supplies a refresh token.
func errRefreshTokenRequired() error {
	return &ValidationError{Fields: []FieldError{
		{Field: "refresh_token", Message: "is required (in the body or the vidra_refresh cookie)"},
	}}
}

// handleRefresh rotates a refresh token, returning a new access + refresh pair.
// The token comes from the body or, when absent, from the vidra_refresh cookie;
// in cookie mode rotation re-sets the cookie and the body omits the raw token.
func (s *Server) handleRefresh(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	raw, fromCookie := resolveRefreshToken(c, in.RefreshToken)
	if raw == "" {
		return errRefreshTokenRequired()
	}
	cookieMode := in.CookieMode || fromCookie
	user, tokens, err := s.authsvc.Refresh(c.Request().Context(), raw, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidRefresh) {
			// A dead cookie would be re-presented on every future attempt (each
			// one tripping reuse detection), so clear it alongside the 401.
			if fromCookie {
				s.clearRefreshCookie(c)
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired refresh token")
		}
		return err
	}
	return s.authResponse(http.StatusOK, c, user, tokens, cookieMode)
}

// handleLogoutAll revokes every active session for the authenticated user
// ("sign out everywhere"). It runs behind requireAuth, so the principal is
// always present, and returns 204.
func (s *Server) handleLogoutAll(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.authsvc.LogoutAll(c.Request().Context(), userID); err != nil {
		return err
	}
	// Clear the cookie-mode session cookie too (harmless when none was set).
	s.clearRefreshCookie(c)
	s.audit(c, observability.ActionLogoutAll, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// handleLogout revokes the session for the presented refresh token — from the
// body or, when absent, from the vidra_refresh cookie. It is idempotent and
// always returns 204, never revealing whether the token existed. The response
// clears the cookie so a browser is always left signed out.
func (s *Server) handleLogout(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	raw, _ := resolveRefreshToken(c, in.RefreshToken)
	if raw == "" {
		return errRefreshTokenRequired()
	}
	if err := s.authsvc.Logout(c.Request().Context(), raw); err != nil {
		return err
	}
	// Clear the cookie-mode session cookie too (harmless when none was set).
	s.clearRefreshCookie(c)
	// Idempotent: no actor_id, since the token may be unknown/already-revoked.
	s.audit(c, observability.ActionLogout, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// passwordResetRequest is the POST /api/v1/auth/password-reset body.
type passwordResetRequest struct {
	Email string `json:"email"`
}

func (r passwordResetRequest) Validate() []FieldError {
	if !looksLikeEmail(r.Email) {
		return []FieldError{{Field: "email", Message: "must be a valid email"}}
	}
	return nil
}

// handleRequestPasswordReset starts the password-reset flow. It always returns
// 202 Accepted — it never reveals whether the email belongs to an account, so it
// cannot be used to enumerate registered users. A matching, active account is
// issued a single-use reset token delivered by the mailer adapter.
func (s *Server) handleRequestPasswordReset(c echo.Context) error {
	var in passwordResetRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.RequestPasswordReset(c.Request().Context(), in.Email); err != nil {
		return err
	}
	// No actor_id/email: enumeration-safe, so the event records only that a reset
	// was requested, not for whom.
	s.audit(c, observability.ActionPasswordResetRequest, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusAccepted)
}

// passwordResetConfirmRequest is the POST /api/v1/auth/password-reset/confirm body.
type passwordResetConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (r passwordResetConfirmRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Token) == "" {
		fes = append(fes, FieldError{Field: "token", Message: "is required"})
	}
	switch {
	case len(r.Password) < 8:
		fes = append(fes, FieldError{Field: "password", Message: "must be at least 8 characters"})
	case len(r.Password) > maxPasswordLen:
		fes = append(fes, FieldError{Field: "password", Message: "must be at most 72 characters"})
	}
	return fes
}

// handleConfirmPasswordReset completes the reset: a valid token sets the new
// password and signs the account out everywhere (all sessions revoked). An
// unknown, used, or expired token is a 400; the token is never echoed back.
func (s *Server) handleConfirmPasswordReset(c echo.Context) error {
	var in passwordResetConfirmRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.ResetPassword(c.Request().Context(), in.Token, in.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidResetToken) {
			s.audit(c, observability.ActionPasswordResetComplete, observability.ResultFailure, "", "invalid_token")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired reset token")
		}
		return err
	}
	s.audit(c, observability.ActionPasswordResetComplete, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// handleRequestEmailVerification issues a verification message for the
// authenticated account. It runs behind requireAuth, so the principal is always
// present. It always returns 202, and is a no-op for an already-verified account.
func (s *Server) handleRequestEmailVerification(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.authsvc.RequestEmailVerification(c.Request().Context(), userID); err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionEmailVerifyRequest, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusAccepted)
}

// emailVerificationConfirmRequest is the POST /api/v1/auth/verify-email/confirm body.
type emailVerificationConfirmRequest struct {
	Token string `json:"token"`
}

func (r emailVerificationConfirmRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Token) == "" {
		return []FieldError{{Field: "token", Message: "is required"}}
	}
	return nil
}

// handleConfirmEmailVerification marks the account's email verified given a valid
// token. It is public — the user may follow the link while logged out. 204 on
// success; an unknown, used, or expired token is a 400.
func (s *Server) handleConfirmEmailVerification(c echo.Context) error {
	var in emailVerificationConfirmRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.VerifyEmail(c.Request().Context(), in.Token); err != nil {
		if errors.Is(err, auth.ErrInvalidVerificationToken) {
			s.audit(c, observability.ActionEmailVerifyConfirm, observability.ResultFailure, "", "invalid_token")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired verification token")
		}
		return err
	}
	s.audit(c, observability.ActionEmailVerifyConfirm, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// deactivateAccountRequest is the POST /api/v1/auth/me/deactivate body. The
// current password is required to confirm a sensitive self-service action (so a
// stolen access token alone cannot disable the account).
type deactivateAccountRequest struct {
	Password string `json:"password"`
}

func (r deactivateAccountRequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

// handleDeactivateAccount disables the authenticated account after confirming
// its password, signing it out everywhere. Behind requireAuth. 204 on success;
// a wrong password is 403. Deactivation is reversible by an administrator; hard
// deletion is a separate, policy-gated flow.
func (s *Server) handleDeactivateAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in deactivateAccountRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.DeactivateAccount(c.Request().Context(), userID, in.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			s.audit(c, observability.ActionAccountDeactivate, observability.ResultFailure, userID.String(), "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionAccountDeactivate, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

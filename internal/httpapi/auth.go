package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// registerRequest is the POST /api/v1/auth/register body.
type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

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
	return fes
}

// loginRequest is the POST /api/v1/auth/login body.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
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

// authResponse is returned by register and login.
type authResponse struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	User         userView `json:"user"`
}

func (s *Server) authResponse(status int, c echo.Context, user sqlcgen.User, tokens auth.Tokens) error {
	return c.JSON(status, authResponse{
		Token:        tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.authTTL.Seconds()),
		User:         newUserView(user),
	})
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
	user, tokens, err := s.authsvc.Register(c.Request().Context(), auth.RegisterInput{
		Username: in.Username,
		Email:    in.Email,
		Password: in.Password,
	}, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	return s.authResponse(http.StatusCreated, c, user, tokens)
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
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, auth.ErrAccountDisabled):
			return echo.NewHTTPError(http.StatusForbidden, "account is disabled")
		}
		return err
	}
	return s.authResponse(http.StatusOK, c, user, tokens)
}

// refreshRequest is the POST /api/v1/auth/refresh and /logout body.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (r refreshRequest) Validate() []FieldError {
	if strings.TrimSpace(r.RefreshToken) == "" {
		return []FieldError{{Field: "refresh_token", Message: "is required"}}
	}
	return nil
}

// handleRefresh rotates a refresh token, returning a new access + refresh pair.
func (s *Server) handleRefresh(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, tokens, err := s.authsvc.Refresh(c.Request().Context(), in.RefreshToken, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidRefresh) {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired refresh token")
		}
		return err
	}
	return s.authResponse(http.StatusOK, c, user, tokens)
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
	return c.NoContent(http.StatusNoContent)
}

// handleLogout revokes the session for the presented refresh token. It is
// idempotent and always returns 204, never revealing whether the token existed.
func (s *Server) handleLogout(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.Logout(c.Request().Context(), in.RefreshToken); err != nil {
		return err
	}
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
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired reset token")
		}
		return err
	}
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
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired verification token")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

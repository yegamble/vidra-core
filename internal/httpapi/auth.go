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
	CreatedAt     time.Time `json:"created_at"`
}

func newUserView(u sqlcgen.User) userView {
	return userView{
		ID:            u.ID.String(),
		Username:      u.Username,
		Email:         u.Email,
		Role:          u.Role,
		EmailVerified: u.EmailVerified,
		CreatedAt:     u.CreatedAt,
	}
}

// authResponse is returned by register and login.
type authResponse struct {
	Token     string   `json:"token"`
	TokenType string   `json:"token_type"`
	ExpiresIn int      `json:"expires_in"`
	User      userView `json:"user"`
}

func (s *Server) authResponse(status int, c echo.Context, user sqlcgen.User, token string) error {
	return c.JSON(status, authResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresIn: int(s.authTTL.Seconds()),
		User:      newUserView(user),
	})
}

// handleRegister creates an account and returns it with an access token.
func (s *Server) handleRegister(c echo.Context) error {
	var in registerRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, token, err := s.authsvc.Register(c.Request().Context(), auth.RegisterInput{
		Username: in.Username,
		Email:    in.Email,
		Password: in.Password,
	})
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	return s.authResponse(http.StatusCreated, c, user, token)
}

// handleLogin verifies credentials and returns an access token.
func (s *Server) handleLogin(c echo.Context) error {
	var in loginRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, token, err := s.authsvc.Login(c.Request().Context(), auth.LoginInput{
		Email:    in.Email,
		Password: in.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, auth.ErrAccountDisabled):
			return echo.NewHTTPError(http.StatusForbidden, "account is disabled")
		}
		return err
	}
	return s.authResponse(http.StatusOK, c, user, token)
}

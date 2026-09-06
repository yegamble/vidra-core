package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// emailChangeRequest is the POST /api/v1/auth/me/email-change body. The current
// password is required for the same reason the password change requires it: a
// stolen access token must not be able to move the address an account is
// recovered through, which is the whole account.
type emailChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewEmail        string `json:"new_email"`
}

func (r emailChangeRequest) Validate() []FieldError {
	var fes []FieldError
	if r.CurrentPassword == "" {
		fes = append(fes, FieldError{Field: "current_password", Message: "is required"})
	}
	if !looksLikeEmail(r.NewEmail) {
		fes = append(fes, FieldError{Field: "new_email", Message: "must be a valid email"})
	}
	return fes
}

// emailChangeConfirmRequest is the POST /api/v1/auth/me/email-change/confirm body.
type emailChangeConfirmRequest struct {
	Token string `json:"token"`
}

func (r emailChangeConfirmRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Token) == "" {
		return []FieldError{{Field: "token", Message: "is required"}}
	}
	return nil
}

// emailChangeStateView is the readable pending state. It is returned to the
// account's OWN authenticated caller and nobody else, so it may name the
// requested address — that is what lets the UI say "we sent a link to X" and
// what lets a user notice a change they did not ask for.
type emailChangeStateView struct {
	Pending     bool       `json:"pending"`
	NewEmail    string     `json:"new_email,omitempty"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func pendingView(p auth.PendingEmailChange) emailChangeStateView {
	requested, expires := p.RequestedAt, p.ExpiresAt
	return emailChangeStateView{
		Pending: true, NewEmail: p.NewEmail,
		RequestedAt: &requested, ExpiresAt: &expires,
	}
}

// emailChangeConfirmedView is the confirmation's answer: the address the account
// now has, so the landing page can state it without a second round trip.
type emailChangeConfirmedView struct {
	Email string `json:"email"`
}

// emailChangeError maps the service's sentinels onto the shipped status codes.
// The two 409s are deliberately distinct in wording: "no password" points at
// the flow that can set one, "already in use" is exactly what registration
// discloses today for a taken address.
func emailChangeError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
	case errors.Is(err, auth.ErrPasswordNotSet):
		return echo.NewHTTPError(http.StatusConflict,
			"this account has no password: use the password reset flow to set one")
	case errors.Is(err, auth.ErrEmailUnchanged):
		return echo.NewHTTPError(http.StatusUnprocessableEntity,
			"that is already the address on this account")
	case errors.Is(err, auth.ErrEmailTaken):
		return echo.NewHTTPError(http.StatusConflict,
			"that email address is already in use on this instance")
	case errors.Is(err, auth.ErrNoPendingEmailChange):
		return echo.NewHTTPError(http.StatusNotFound, "no email change is pending")
	case errors.Is(err, auth.ErrInvalidEmailChangeToken):
		return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired email change token")
	case errors.Is(err, auth.ErrAccountNotFound):
		return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
	}
	return err
}

// handleRequestEmailChange starts the two-step change: it re-verifies the
// current password and mails a single-use token to the NEW address. 202, and
// the account's live address is unchanged until that token is confirmed. Behind
// requireAuth AND the strict auth limiter — supplying a current password makes
// it a guessing surface exactly like login, and the limiter runs first, so the
// budget is spent whether or not the caller is authenticated.
func (s *Server) handleRequestEmailChange(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in emailChangeRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	pending, err := s.authsvc.RequestEmailChange(c.Request().Context(), userID, in.CurrentPassword, in.NewEmail)
	if err != nil {
		s.audit(c, observability.ActionEmailChangeRequest, observability.ResultFailure, userID.String(), emailChangeReason(err))
		return emailChangeError(err)
	}
	s.audit(c, observability.ActionEmailChangeRequest, observability.ResultSuccess, userID.String(), "")
	return c.JSON(http.StatusAccepted, pendingView(pending))
}

// handleResendEmailChange re-sends the confirmation for the address already
// pending, superseding the previous token. It takes no body: the pending
// request already names the address, and re-asking for the password would only
// stop the user finishing a change they have already authorized.
func (s *Server) handleResendEmailChange(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	pending, err := s.authsvc.ResendEmailChange(c.Request().Context(), userID)
	if err != nil {
		return emailChangeError(err)
	}
	s.audit(c, observability.ActionEmailChangeRequest, observability.ResultSuccess, userID.String(), "resend")
	return c.JSON(http.StatusAccepted, pendingView(pending))
}

// handleGetEmailChange reads the pending state. 200 always: "nothing pending"
// is a state, not an error, and the settings card renders it either way.
func (s *Server) handleGetEmailChange(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	pending, err := s.authsvc.PendingEmailChangeFor(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, auth.ErrNoPendingEmailChange) {
			return c.JSON(http.StatusOK, emailChangeStateView{Pending: false})
		}
		return emailChangeError(err)
	}
	return c.JSON(http.StatusOK, pendingView(pending))
}

// handleCancelEmailChange drops the pending request, killing its token. 404
// when nothing was pending rather than a success that changed nothing.
func (s *Server) handleCancelEmailChange(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.authsvc.CancelEmailChange(c.Request().Context(), userID); err != nil {
		return emailChangeError(err)
	}
	s.audit(c, observability.ActionEmailChangeCancel, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// handleConfirmEmailChange consumes the token delivered to the new address and
// moves the account onto it, atomically. It runs behind requireAuth: the token
// proves possession of the mailbox, the session proves whose account it is, and
// requiring both means a token that leaks out of a mailbox — the one place it is
// guaranteed to sit in plaintext — cannot move anybody's address on its own.
// Another account's token is refused exactly like an unknown one: 400.
func (s *Server) handleConfirmEmailChange(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in emailChangeConfirmRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	_, newEmail, err := s.authsvc.ConfirmEmailChange(c.Request().Context(), userID, in.Token)
	if err != nil {
		s.audit(c, observability.ActionEmailChangeConfirm, observability.ResultFailure, userID.String(), emailChangeReason(err))
		return emailChangeError(err)
	}
	s.audit(c, observability.ActionEmailChangeConfirm, observability.ResultSuccess, userID.String(), "")
	return c.JSON(http.StatusOK, emailChangeConfirmedView{Email: newEmail})
}

// emailChangeReason is the audit reason for a refusal. It names the RULE, never
// either address — the audit trail keeps email addresses out by policy.
func emailChangeReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		return "invalid_password"
	case errors.Is(err, auth.ErrPasswordNotSet):
		return "password_not_set"
	case errors.Is(err, auth.ErrEmailUnchanged):
		return "email_unchanged"
	case errors.Is(err, auth.ErrEmailTaken):
		return "email_taken"
	case errors.Is(err, auth.ErrInvalidEmailChangeToken):
		return "invalid_token"
	}
	return "error"
}

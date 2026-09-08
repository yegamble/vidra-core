package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// TOTP two-factor authentication endpoints (fix_plan P4). Enrollment, status,
// and disable run behind requireAuth; the challenge is the second half of an
// MFA login and is public but auth-rate-limited (it is a credential endpoint).
// The TOTP secret, otpauth:// URI, recovery codes, and submitted codes are
// never logged — audit events carry only actor_id and a safe reason.

// totpEnrollmentResponse is returned exactly once when enrollment starts. The
// secret/URI are never retrievable again — re-POST to restart with a fresh one.
type totpEnrollmentResponse struct {
	Secret     string `json:"secret"`
	OtpauthURI string `json:"otpauth_uri"`
}

// handleBeginTOTPEnrollment generates a TOTP secret for the authenticated
// account (pending — login is unaffected until verified). 409 when MFA is
// already enabled.
func (s *Server) handleBeginTOTPEnrollment(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	enr, err := s.authsvc.BeginTOTPEnrollment(c.Request().Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrMFAAlreadyEnabled):
			return echo.NewHTTPError(http.StatusConflict, "two-factor authentication is already enabled")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		case errors.Is(err, auth.ErrMFAUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	return c.JSON(http.StatusOK, totpEnrollmentResponse{Secret: enr.Secret, OtpauthURI: enr.URI})
}

// totpVerifyRequest is the POST /api/v1/auth/mfa/totp/verify body.
type totpVerifyRequest struct {
	Code string `json:"code"`
}

func (r totpVerifyRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Code) == "" {
		return []FieldError{{Field: "code", Message: "is required"}}
	}
	return nil
}

// recoveryCodesResponse carries the single-use recovery codes, shown exactly
// once when MFA is enabled.
type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// handleVerifyTOTPEnrollment confirms a pending enrollment with the first
// valid TOTP code: MFA flips on and the 10 single-use recovery codes are
// returned ONCE. 400 on a wrong code or no pending enrollment; 409 when
// already enabled.
func (s *Server) handleVerifyTOTPEnrollment(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in totpVerifyRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	codes, err := s.authsvc.VerifyTOTPEnrollment(c.Request().Context(), userID, in.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidMFACode):
			s.audit(c, observability.ActionMFAEnable, observability.ResultFailure, userID.String(), "invalid_code")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid code")
		case errors.Is(err, auth.ErrMFASecretUndecryptable):
			// The same fault reached through the enrollment half: a KEK that
			// changed between starting an enrollment and confirming it. Same
			// treatment — the wrong-code answer for this endpoint, and the one
			// operator line — for the same reason.
			s.logMFASecretUndecryptable(c, err)
			s.audit(c, observability.ActionMFAEnable, observability.ResultFailure, userID.String(), "secret_undecryptable")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid code")
		case errors.Is(err, auth.ErrMFANotEnrolled):
			s.audit(c, observability.ActionMFAEnable, observability.ResultFailure, userID.String(), "not_enrolled")
			return echo.NewHTTPError(http.StatusBadRequest, "no TOTP enrollment in progress")
		case errors.Is(err, auth.ErrMFAAlreadyEnabled):
			return echo.NewHTTPError(http.StatusConflict, "two-factor authentication is already enabled")
		case errors.Is(err, auth.ErrMFAUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	s.audit(c, observability.ActionMFAEnable, observability.ResultSuccess, userID.String(), "")
	return c.JSON(http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

// disableTOTPRequest is the DELETE /api/v1/auth/mfa/totp body. The current
// password re-confirms the action so a stolen access token alone cannot strip
// two-factor protection.
type disableTOTPRequest struct {
	Password string `json:"password"`
}

func (r disableTOTPRequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

// handleDisableTOTP turns MFA off (dropping the secret and every recovery
// code) after password re-authentication, and signs every OTHER session out —
// access tokens included, because access tokens are session-bound — then mails
// the account the notice. Wrong password → 403; nothing to disable → 404.
func (s *Server) handleDisableTOTP(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in disableTOTPRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.DisableTOTP(c.Request().Context(), userID, in.Password, sessionIDFromContext(c)); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			s.audit(c, observability.ActionMFADisable, observability.ResultFailure, userID.String(), "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrMFANotEnabled):
			return echo.NewHTTPError(http.StatusNotFound, "two-factor authentication is not enabled")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		case errors.Is(err, auth.ErrMFAUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	s.audit(c, observability.ActionMFADisable, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// mfaStatusResponse is the GET /api/v1/auth/mfa body.
type mfaStatusResponse struct {
	Enabled                bool  `json:"enabled"`
	RecoveryCodesRemaining int64 `json:"recovery_codes_remaining"`
}

// handleGetMFAStatus reports the authenticated account's two-factor state. A
// pending (unverified) enrollment reports as disabled.
func (s *Server) handleGetMFAStatus(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	st, err := s.authsvc.GetMFAStatus(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, auth.ErrMFAUnavailable) {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	return c.JSON(http.StatusOK, mfaStatusResponse{
		Enabled:                st.Enabled,
		RecoveryCodesRemaining: st.RecoveryCodesRemaining,
	})
}

// mfaChallengeRequest is the POST /api/v1/auth/mfa/challenge body: the
// mfa_token from the login response plus a 6-digit TOTP code or a recovery
// code. cookie_mode matches login (the resulting session's refresh token is
// delivered as the httpOnly vidra_refresh cookie).
type mfaChallengeRequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
	// CookieMode opts the new session into cookie mode, like login/register.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

func (r mfaChallengeRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.MFAToken) == "" {
		fes = append(fes, FieldError{Field: "mfa_token", Message: "is required"})
	}
	if strings.TrimSpace(r.Code) == "" {
		fes = append(fes, FieldError{Field: "code", Message: "is required"})
	}
	return fes
}

// handleMFAChallenge completes an MFA login: a valid mfa_token plus a TOTP or
// recovery code (marked used — single-use) yields the full auth response. A
// tampered/expired/repurposed token and a wrong code are both 401.
func (s *Server) handleMFAChallenge(c echo.Context) error {
	var in mfaChallengeRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, tokens, method, err := s.authsvc.CompleteMFAChallenge(c.Request().Context(), in.MFAToken, in.Code, c.Request().UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidMFAToken):
			// No actor_id: the token did not resolve to a trusted principal.
			s.audit(c, observability.ActionMFAChallenge, observability.ResultFailure, "", "invalid_mfa_token")
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired mfa token")
		case errors.Is(err, auth.ErrInvalidMFACode):
			s.audit(c, observability.ActionMFAChallenge, observability.ResultFailure, "", "invalid_code")
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid code")
		case errors.Is(err, auth.ErrMFASecretUndecryptable):
			// A37-1. The stored secret cannot be opened with this process's KEK,
			// so the code can never be checked — and the answer is the wrong-code
			// answer, verbatim, on purpose: a distinguishable status or message
			// would let an unauthenticated caller enumerate which accounts an
			// instance can no longer verify, and it would be a 500 an operator
			// reads as "an unexpected error occurred" rather than as the
			// configuration fault it is. The whole signal goes to the log line
			// below, which names the account and the class.
			actor := s.logMFASecretUndecryptable(c, err)
			s.audit(c, observability.ActionMFAChallenge, observability.ResultFailure, actor, "secret_undecryptable")
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid code")
		case errors.Is(err, auth.ErrMFAUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "two-factor authentication is not configured on this server")
		}
		return err
	}
	// The reason records which factor completed the login (totp/recovery_code)
	// — never the code itself.
	s.audit(c, observability.ActionMFAChallenge, observability.ResultSuccess, user.ID.String(), string(method))
	cookieMode := in.CookieMode || refreshCookieToken(c) != ""
	return s.authResponse(http.StatusOK, c, user, tokens, cookieMode)
}

// logMFASecretUndecryptable writes the ONE line an operator gets when a stored
// TOTP secret cannot be opened with the configured KEK, and returns the account
// id for the audit row.
//
// It is deliberately loud — error level, a sentence that names the CONSEQUENCE
// and the usual cause, the same discipline the /admin/system probes use —
// because it is the only place this fault surfaces at all: the client's 401 is
// indistinguishable from a wrong code by design, so an operator who never reads
// this line has no way to learn that the second factor is broken instance-wide.
//
// It carries the failure class, the account and the cipher's own message. Never
// the ciphertext, never the key, never the plaintext secret.
func (s *Server) logMFASecretUndecryptable(c echo.Context, err error) string {
	attrs := []any{"failure", "mfa_secret_undecryptable"}
	var ue *auth.MFASecretUndecryptableError
	actor := ""
	if errors.As(err, &ue) {
		actor = ue.UserID.String()
		attrs = append(attrs, "user_id", actor, "cause", ue.Cause)
	}
	s.logger.ErrorContext(c.Request().Context(),
		"a stored TOTP secret could not be decrypted with the configured MFA_KEY_KEK, so this account's second factor can never verify and its challenge is being refused like a wrong code. The usual cause is a database restored without the config archive that carries the KEK, or with a different one. Recovery codes are hashed rather than sealed and still work; an admin can also reset the account's second factor.",
		attrs...)
	return actor
}

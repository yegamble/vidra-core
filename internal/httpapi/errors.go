package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
)

// ErrorResponse is the single, consistent JSON error envelope returned by every
// endpoint. The frontend and any API consumer can rely on this shape for all
// non-2xx responses. It is documented in api/openapi.yaml as ErrorResponse.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the machine-readable code, a human-readable message, and the
// request ID so a user can quote it when reporting a problem.
type ErrorBody struct {
	// Code is a stable, snake_case identifier for the error class.
	Code string `json:"code"`
	// Message is a human-readable description. It never leaks internal detail
	// for 5xx errors.
	Message string `json:"message"`
	// RequestID correlates the response with server logs. Omitted when unknown.
	RequestID string `json:"request_id,omitempty"`
	// Fields lists field-level validation problems. Present only on validation
	// failures (422 unprocessable_entity).
	Fields []FieldError `json:"fields,omitempty"`
}

// httpErrorHandler is Echo's central error handler. It converts any error
// (including *echo.HTTPError raised by handlers and middleware) into the
// consistent ErrorResponse envelope, logs 5xx errors, and never exposes raw
// internal error text to clients on a 500.
func (s *Server) httpErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	status := http.StatusInternalServerError
	message := "an unexpected error occurred"
	// code, when set here, is a known-safe override that survives the 5xx
	// message-scrubbing below (e.g. a request timeout).
	code := ""
	// fields carries field-level validation errors when present.
	var fields []FieldError

	var he *echo.HTTPError
	var ve *ValidationError
	var qe *QuotaExceededError
	var dqe *DailyQuotaExceededError
	var evr *EmailVerificationRequiredError
	var ocr *OwnerClaimRequiredError
	var oci *OwnerClaimInvalidError
	var fd *FeatureDisabledError
	var id *IPFSDisabledError
	var pr *PasswordRequiredError
	var tma *TooManyActiveUploadsError
	var cfd *ContactFormDisabledError
	var rc *ReplaceConflictError
	var su *SearchUnavailableError
	var ale *ATProtoLoginError
	var mtf *MailTestFailedError
	switch {
	case errors.As(err, &mtf):
		status = http.StatusBadGateway
		message = "the mail relay refused the test message. Check the SMTP host, port and credentials, then the relay's own logs — the server log for this request has its exact answer"
		code = "mail_test_failed"
	case errors.As(err, &ale):
		status = ale.Status
		message = ale.Message
		code = ale.Code
	case errors.As(err, &su):
		status = http.StatusServiceUnavailable
		message = "search is temporarily unavailable"
		code = "search_unavailable"
	case errors.As(err, &rc):
		status = http.StatusConflict
		message = rc.Reason
		if message == "" {
			message = "the video's file cannot be replaced right now"
		}
		code = "replace_conflict"
	case errors.As(err, &cfd):
		status = http.StatusConflict
		message = "the contact form is not available on this instance"
		code = "contact_form_disabled"
	case errors.As(err, &ve):
		status = http.StatusUnprocessableEntity
		message = "validation failed"
		code = "unprocessable_entity"
		fields = ve.Fields
	case errors.As(err, &pr):
		status = http.StatusUnauthorized
		message = "this video is password protected"
		code = "password_required"
	case errors.As(err, &qe):
		status = http.StatusUnprocessableEntity
		message = "storing this file would exceed your storage quota"
		code = "quota_exceeded"
	case errors.As(err, &dqe):
		status = http.StatusUnprocessableEntity
		message = "storing this file would exceed your daily upload quota; try again later"
		code = "daily_quota_exceeded"
	case errors.As(err, &evr):
		status = http.StatusForbidden
		message = "verify your email address to sign in; check your inbox for the verification link"
		code = "email_verification_required"
	case errors.As(err, &ocr):
		status = http.StatusForbidden
		message = "this instance is awaiting its owner; sign-ups open once first-run setup is complete"
		code = "owner_claim_required"
	case errors.As(err, &oci):
		status = http.StatusForbidden
		message = "invalid or already-used setup token"
		code = "owner_claim_invalid"
	case errors.As(err, &fd):
		status = http.StatusForbidden
		message = "this feature is disabled on this instance"
		code = "feature_disabled"
	case errors.As(err, &tma):
		status = http.StatusTooManyRequests
		message = "you have too many uploads in progress; finish or cancel one and try again"
		code = "too_many_active_uploads"
	case errors.As(err, &id):
		status = http.StatusServiceUnavailable
		message = "IPFS mirroring is not enabled on this instance"
		code = "ipfs_disabled"
	case errors.As(err, &he):
		status = he.Code
		if he.Message != nil {
			message = fmt.Sprintf("%v", he.Message)
		}
		if he.Internal != nil {
			err = he.Internal
		}
		// 501 Not Implemented is a documented, client-safe response (e.g. a
		// donation network without a signing path). Give it a stable code so
		// its message survives the generic 5xx message-scrubbing below.
		if status == http.StatusNotImplemented {
			code = "not_implemented"
		}
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusServiceUnavailable
		message = "the request timed out"
		code = "request_timeout"
	}

	reqID := c.Response().Header().Get(echo.HeaderXRequestID)

	// 5xx errors are operator-facing: log them with context and never leak a
	// handler-provided message to the client — unless we already chose a
	// known-safe code/message above.
	if status >= http.StatusInternalServerError {
		s.logger.Error("request failed",
			"error", err,
			"method", c.Request().Method,
			"path", c.Path(),
			"status", status,
			"request_id", reqID,
		)
		if code == "" {
			message = "an unexpected error occurred"
		}
	}

	if code == "" {
		code = codeForStatus(status)
	}

	resp := ErrorResponse{Error: ErrorBody{
		Code:      code,
		Message:   message,
		RequestID: reqID,
		Fields:    fields,
	}}

	var writeErr error
	if c.Request().Method == http.MethodHead {
		writeErr = c.NoContent(status)
	} else {
		writeErr = c.JSON(status, resp)
	}
	if writeErr != nil {
		s.logger.Error("failed to write error response", "error", writeErr, "request_id", reqID)
	}
}

// QuotaExceededError renders as 422 with the stable code "quota_exceeded":
// storing the incoming file would push the caller past their effective storage
// quota (per-user override, else INSTANCE_DEFAULT_QUOTA_BYTES). Raised by the
// upload/import handlers; never raised when the effective quota is unlimited.
type QuotaExceededError struct{}

func (e *QuotaExceededError) Error() string { return "storage quota exceeded" }

// DailyQuotaExceededError renders as 422 with the stable code
// "daily_quota_exceeded": storing the incoming file would push the caller past
// the instance's rolling-24h daily upload quota
// (default_user_daily_quota_bytes, config-parity W7). Distinct from
// quota_exceeded so the studio can tell "free up space" apart from "try again
// later". Never raised while no daily quota is configured.
type DailyQuotaExceededError struct{}

func (e *DailyQuotaExceededError) Error() string { return "daily upload quota exceeded" }

// EmailVerificationRequiredError renders as 403 with the stable code
// "email_verification_required": the credentials are valid, but the account
// was created while the registration email-verification gate was active and
// its email is still unverified (config-parity W7) — the session is withheld
// until the verification link is followed.
type EmailVerificationRequiredError struct{}

func (e *EmailVerificationRequiredError) Error() string { return "email verification required" }

// OwnerClaimRequiredError renders as 403 with the stable code
// "owner_claim_required": the instance is awaiting its owner (empty users
// table + an unclaimed owner-claim token), so every normal signup path refuses
// until POST /setup/claim-owner redeems the boot-logged token for the admin
// account.
type OwnerClaimRequiredError struct{}

func (e *OwnerClaimRequiredError) Error() string { return "owner claim required" }

// OwnerClaimInvalidError renders as 403 with the stable code
// "owner_claim_invalid": the presented owner-claim token is wrong, already
// redeemed, or no claim is pending — one non-probing answer for all three.
type OwnerClaimInvalidError struct{}

func (e *OwnerClaimInvalidError) Error() string { return "invalid owner-claim token" }

// FeatureDisabledError renders as 403 with the stable code "feature_disabled":
// the instance operator has turned off this feature via the admin
// instance-settings overlay (uploads/imports/live/comments/downloads). Feature names the
// toggle that is off, for logging/diagnostics; the client-facing message is
// generic.
type FeatureDisabledError struct{ Feature string }

func (e *FeatureDisabledError) Error() string { return "feature disabled: " + e.Feature }

// TooManyActiveUploadsError renders as 429 with the stable code
// "too_many_active_uploads": the caller already holds
// UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER active resumable upload sessions
// (UPLOAD-10 / W2.C3 batch guard). It is a backpressure signal, not a failure —
// a batch-upload client queues and retries once an in-flight session completes
// or is cancelled (either frees a slot). Max is the configured cap, for
// diagnostics/logging; the client-facing message is generic.
type TooManyActiveUploadsError struct{ Max int }

func (e *TooManyActiveUploadsError) Error() string {
	return "too many active upload sessions"
}

// PasswordRequiredError renders as 401 with the stable code "password_required":
// the requested video is privacy=password and the caller presented neither owner/
// moderator authority nor a valid playback token (CORE-17 / W1.C2). It is the
// deliberate exception to the 404-for-invisible rule so the watch/embed page can
// render an unlock prompt. It carries no secret (no token, no hash, no password).
type PasswordRequiredError struct{}

func (e *PasswordRequiredError) Error() string { return "password required" }

// ReplaceConflictError renders as 409 with the stable code "replace_conflict"
// (video file replacement, config-parity W14): the target video is not in a
// replaceable state — not published, a transcode of its current source is
// still pending/running, or another replacement is already in flight. Reason
// is a client-safe explanation shown to the caller.
type ReplaceConflictError struct{ Reason string }

func (e *ReplaceConflictError) Error() string { return "replace conflict: " + e.Reason }

// ContactFormDisabledError renders as 409 with the stable code
// "contact_form_disabled": POST /instance/contact was called while the form's
// EFFECTIVE availability is false — the admin toggle is off, no contact email
// is configured, or the deployment has no outbound mail path.
type ContactFormDisabledError struct{}

func (e *ContactFormDisabledError) Error() string { return "contact form disabled" }

// MailTestFailedError renders as 502 with the stable code "mail_test_failed":
// the admin mail probe was refused by the relay. It carries NO cause on purpose
// — the relay's rejection routinely quotes the recipient address back, and the
// central handler logs whatever it is given. The handler logs the real error
// itself, to the operator's server log, and returns this bare marker so nothing
// with an address in it can reach a response body or a log line twice.
type MailTestFailedError struct{}

func (e *MailTestFailedError) Error() string { return "mail test failed" }

// SearchUnavailableError renders as 503 with the stable code "search_unavailable"
// (search-service W4): a search-history read/delete could not reach vidra-search
// (the service is disabled on this instance or currently unreachable). The
// search-history endpoints surface this honestly rather than silently pretending
// the history is empty or the delete succeeded.
type SearchUnavailableError struct{}

func (e *SearchUnavailableError) Error() string { return "search service unavailable" }

// ATProtoLoginError renders an ATProto identity-login failure with a stable,
// snake_case code at the given status (503 atproto_disabled, 422 invalid_handle,
// 502 atproto_resolution_failed / atproto_upstream). It carries only safe machine
// codes — never an upstream body, token, PKCE verifier, or DPoP key.
type ATProtoLoginError struct {
	Status  int
	Code    string
	Message string
}

func (e *ATProtoLoginError) Error() string { return e.Code }

// IPFSDisabledError renders as 503 with the stable code "ipfs_disabled": the
// hybrid IPFS media mirror (fix_plan P19) is off (IPFS_ENABLED=false) on this
// instance, so the admin IPFS endpoints have nothing to report. Mirrors the
// PeerTube-import "not configured" 503 pattern.
type IPFSDisabledError struct{}

func (e *IPFSDisabledError) Error() string { return "ipfs mirroring is not enabled" }

// codeForStatus maps an HTTP status to a stable, snake_case error code. Unknown
// statuses fall back to a generic code derived from the class.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_entity_too_large"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusNotImplemented:
		return "not_implemented"
	case http.StatusInternalServerError:
		return "internal_error"
	}
	switch {
	case status >= 500:
		return "server_error"
	case status >= 400:
		return "client_error"
	default:
		return "error"
	}
}

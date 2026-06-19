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
	switch {
	case errors.As(err, &ve):
		status = http.StatusUnprocessableEntity
		message = "validation failed"
		code = "unprocessable_entity"
		fields = ve.Fields
	case errors.As(err, &he):
		status = he.Code
		if he.Message != nil {
			message = fmt.Sprintf("%v", he.Message)
		}
		if he.Internal != nil {
			err = he.Internal
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

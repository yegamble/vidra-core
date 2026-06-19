package httpapi

import (
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

	if he, ok := err.(*echo.HTTPError); ok {
		status = he.Code
		if he.Message != nil {
			message = fmt.Sprintf("%v", he.Message)
		}
		if he.Internal != nil {
			err = he.Internal
		}
	}

	reqID := c.Response().Header().Get(echo.HeaderXRequestID)

	// 5xx errors are operator-facing: log them with context and do not leak the
	// underlying message to the client.
	if status >= http.StatusInternalServerError {
		s.logger.Error("request failed",
			"error", err,
			"method", c.Request().Method,
			"path", c.Path(),
			"status", status,
			"request_id", reqID,
		)
		message = "an unexpected error occurred"
	}

	resp := ErrorResponse{Error: ErrorBody{
		Code:      codeForStatus(status),
		Message:   message,
		RequestID: reqID,
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

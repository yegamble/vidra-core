package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"athena/internal/domain"
)

type Response struct {
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Success bool        `json:"success"`
	Meta    *Meta       `json:"meta,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

type Meta struct {
	Total    int64 `json:"total"`
	Limit    int   `json:"limit"`
	Offset   int   `json:"offset"`
	Page     int   `json:"page,omitempty"`
	PageSize int   `json:"pageSize,omitempty"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := Response{
		Data:    data,
		Success: statusCode < 400,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log error but don't return as headers are already sent
		_ = err
	}
}

func WriteError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorInfo := &ErrorInfo{
		Message: err.Error(),
	}

	if domainErr, ok := err.(domain.DomainError); ok {
		errorInfo.Code = domainErr.Code
		errorInfo.Details = domainErr.Details
	}

	response := Response{
		Error:   errorInfo,
		Success: false,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log error but don't return as headers are already sent
		_ = err
	}
}

func WriteJSONWithMeta(w http.ResponseWriter, statusCode int, data interface{}, meta *Meta) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := Response{
		Data:    data,
		Success: statusCode < 400,
		Meta:    meta,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log error but don't return as headers are already sent
		_ = err
	}
}

var (
	// domainErrorCodeToStatus maps domain error codes to HTTP status codes
	domainErrorCodeToStatus = map[string]int{
		"IPFS_UPLOAD_FAILED":  http.StatusServiceUnavailable,
		"IPFS_PIN_FAILED":     http.StatusServiceUnavailable,
		"IPFS_NOT_CONFIGURED": http.StatusServiceUnavailable,
		"STORAGE_ERROR":       http.StatusInternalServerError,
		"FILE_ERROR":          http.StatusInternalServerError,
		"INTERNAL_ERROR":      http.StatusInternalServerError,
		"DB_ERROR":            http.StatusInternalServerError,
		"INVALID_PATH":        http.StatusBadRequest,
		"BAD_REQUEST":         http.StatusBadRequest,
		"UNAUTHORIZED":        http.StatusUnauthorized,
	}

	// errorToStatusMappings groups errors by their HTTP status code
	errorToStatusMappings = []struct {
		status int
		errors []error
	}{
		{
			status: http.StatusNotFound,
			errors: []error{domain.ErrNotFound, domain.ErrUserNotFound, domain.ErrVideoNotFound, domain.ErrMessageNotFound, domain.ErrConversationNotFound},
		},
		{
			status: http.StatusUnauthorized,
			errors: []error{domain.ErrUnauthorized, domain.ErrInvalidCredentials, domain.ErrInvalidToken, domain.ErrTokenExpired},
		},
		{
			status: http.StatusBadRequest,
			errors: []error{domain.ErrValidation, domain.ErrBadRequest, domain.ErrInvalidFormat, domain.ErrInvalidChunk, domain.ErrCannotMessageSelf, domain.ErrMessageTooLong, domain.ErrInvalidMessageType},
		},
		{
			status: http.StatusConflict,
			errors: []error{domain.ErrConflict, domain.ErrUserAlreadyExists},
		},
		{
			status: http.StatusTooManyRequests,
			errors: []error{domain.ErrTooManyRequests},
		},
		{
			status: http.StatusServiceUnavailable,
			errors: []error{domain.ErrServiceUnavailable, domain.ErrIPFSUnavailable},
		},
		{
			status: http.StatusForbidden,
			errors: []error{domain.ErrForbidden},
		},
		{
			status: http.StatusRequestEntityTooLarge,
			errors: []error{domain.ErrFileTooLarge},
		},
	}
)

// mapDomainErrorCode maps a domain error code to HTTP status
func mapDomainErrorCode(code string) (int, bool) {
	status, ok := domainErrorCodeToStatus[code]
	return status, ok
}

// checkErrorMapping checks if the error matches any in the mapping list
func checkErrorMapping(err error, mapping struct {
	status int
	errors []error
}) (int, bool) {
	for _, e := range mapping.errors {
		if errors.Is(err, e) {
			return mapping.status, true
		}
	}
	return 0, false
}

func MapDomainErrorToHTTP(err error) int {
	// Check for domain error with code (handle both value and pointer types)
	if domainErr, ok := err.(domain.DomainError); ok {
		if status, ok := mapDomainErrorCode(domainErr.Code); ok {
			return status
		}
	}
	if domainErr, ok := err.(*domain.DomainError); ok {
		if status, ok := mapDomainErrorCode(domainErr.Code); ok {
			return status
		}
	}

	// Check error mappings
	for _, mapping := range errorToStatusMappings {
		if status, ok := checkErrorMapping(err, mapping); ok {
			return status
		}
	}

	return http.StatusInternalServerError
}

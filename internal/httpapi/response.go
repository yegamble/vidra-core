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
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
	Page   int   `json:"page,omitempty"`
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

func MapDomainErrorToHTTP(err error) int {
	// Check for specific error codes in domain errors (handle both value and pointer types)
	if domainErr, ok := err.(domain.DomainError); ok {
		switch domainErr.Code {
		case "IPFS_UPLOAD_FAILED", "IPFS_PIN_FAILED", "IPFS_NOT_CONFIGURED":
			return http.StatusServiceUnavailable
		case "STORAGE_ERROR", "FILE_ERROR", "INTERNAL_ERROR", "DB_ERROR":
			return http.StatusInternalServerError
		case "INVALID_PATH", "BAD_REQUEST":
			return http.StatusBadRequest
		case "UNAUTHORIZED":
			return http.StatusUnauthorized
		}
	}
	if domainErr, ok := err.(*domain.DomainError); ok {
		switch domainErr.Code {
		case "IPFS_UPLOAD_FAILED", "IPFS_PIN_FAILED", "IPFS_NOT_CONFIGURED":
			return http.StatusServiceUnavailable
		case "STORAGE_ERROR", "FILE_ERROR", "INTERNAL_ERROR", "DB_ERROR":
			return http.StatusInternalServerError
		case "INVALID_PATH", "BAD_REQUEST":
			return http.StatusBadRequest
		case "UNAUTHORIZED":
			return http.StatusUnauthorized
		}
	}

	// Use errors.Is to correctly handle wrapped errors
	notFound := []error{domain.ErrNotFound, domain.ErrUserNotFound, domain.ErrVideoNotFound, domain.ErrMessageNotFound, domain.ErrConversationNotFound}
	for _, e := range notFound {
		if errors.Is(err, e) {
			return http.StatusNotFound
		}
	}

	unauthorized := []error{domain.ErrUnauthorized, domain.ErrInvalidCredentials, domain.ErrInvalidToken, domain.ErrTokenExpired}
	for _, e := range unauthorized {
		if errors.Is(err, e) {
			return http.StatusUnauthorized
		}
	}

	if errors.Is(err, domain.ErrForbidden) {
		return http.StatusForbidden
	}

	badReq := []error{domain.ErrValidation, domain.ErrBadRequest, domain.ErrInvalidFormat, domain.ErrInvalidChunk, domain.ErrCannotMessageSelf, domain.ErrMessageTooLong, domain.ErrInvalidMessageType}
	for _, e := range badReq {
		if errors.Is(err, e) {
			return http.StatusBadRequest
		}
	}

	if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrUserAlreadyExists) {
		return http.StatusConflict
	}
	if errors.Is(err, domain.ErrTooManyRequests) {
		return http.StatusTooManyRequests
	}
	if errors.Is(err, domain.ErrServiceUnavailable) || errors.Is(err, domain.ErrIPFSUnavailable) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, domain.ErrFileTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

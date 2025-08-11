package httpapi

import (
	"encoding/json"
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
	Total  int64 `json:"total,omitempty"`
	Limit  int   `json:"limit,omitempty"`
	Offset int   `json:"offset,omitempty"`
	Page   int   `json:"page,omitempty"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := Response{
		Data:    data,
		Success: statusCode < 400,
	}

	json.NewEncoder(w).Encode(response)
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

	json.NewEncoder(w).Encode(response)
}

func WriteJSONWithMeta(w http.ResponseWriter, statusCode int, data interface{}, meta *Meta) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := Response{
		Data:    data,
		Success: statusCode < 400,
		Meta:    meta,
	}

	json.NewEncoder(w).Encode(response)
}

func MapDomainErrorToHTTP(err error) int {
	switch err {
	case domain.ErrNotFound, domain.ErrUserNotFound, domain.ErrVideoNotFound:
		return http.StatusNotFound
	case domain.ErrUnauthorized, domain.ErrInvalidCredentials, domain.ErrInvalidToken, domain.ErrTokenExpired:
		return http.StatusUnauthorized
	case domain.ErrForbidden:
		return http.StatusForbidden
	case domain.ErrValidation, domain.ErrBadRequest, domain.ErrInvalidFormat, domain.ErrInvalidChunk:
		return http.StatusBadRequest
	case domain.ErrConflict, domain.ErrUserAlreadyExists:
		return http.StatusConflict
	case domain.ErrTooManyRequests:
		return http.StatusTooManyRequests
	case domain.ErrServiceUnavailable, domain.ErrIPFSUnavailable:
		return http.StatusServiceUnavailable
	case domain.ErrFileTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}
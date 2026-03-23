package shared

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		data           interface{}
		expectedStatus int
		expectedData   interface{}
	}{
		{
			name:           "success response with data",
			statusCode:     http.StatusOK,
			data:           map[string]string{"key": "value"},
			expectedStatus: http.StatusOK,
			expectedData:   map[string]interface{}{"key": "value"},
		},
		{
			name:           "created response",
			statusCode:     http.StatusCreated,
			data:           map[string]int{"id": 123},
			expectedStatus: http.StatusCreated,
			expectedData:   map[string]interface{}{"id": float64(123)},
		},
		{
			name:           "nil data",
			statusCode:     http.StatusOK,
			data:           nil,
			expectedStatus: http.StatusOK,
			expectedData:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteJSON(rec, tt.statusCode, tt.data)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var response Response
			err := json.NewDecoder(rec.Body).Decode(&response)
			require.NoError(t, err)

			assert.True(t, response.Success)
			assert.Nil(t, response.Error)
			assert.Equal(t, tt.expectedData, response.Data)
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name               string
		statusCode         int
		err                error
		expectedStatus     int
		expectedSuccess    bool
		expectedErrMessage string
		expectedErrCode    string
		expectedDetails    string
	}{
		{
			name:               "standard error",
			statusCode:         http.StatusBadRequest,
			err:                errors.New("validation failed"),
			expectedStatus:     http.StatusBadRequest,
			expectedSuccess:    false,
			expectedErrMessage: "validation failed",
			expectedErrCode:    "",
			expectedDetails:    "",
		},
		{
			name:               "domain error with code",
			statusCode:         http.StatusNotFound,
			err:                domain.NewDomainError("RESOURCE_NOT_FOUND", "Resource not found"),
			expectedStatus:     http.StatusNotFound,
			expectedSuccess:    false,
			expectedErrMessage: "RESOURCE_NOT_FOUND: Resource not found",
			expectedErrCode:    "RESOURCE_NOT_FOUND",
			expectedDetails:    "",
		},
		{
			name:               "domain error with details",
			statusCode:         http.StatusInternalServerError,
			err:                domain.NewDomainErrorWithDetails("DB_ERROR", "Database error", "Connection timeout"),
			expectedStatus:     http.StatusInternalServerError,
			expectedSuccess:    false,
			expectedErrMessage: "DB_ERROR: Database error (Connection timeout)",
			expectedErrCode:    "DB_ERROR",
			expectedDetails:    "Connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, tt.statusCode, tt.err)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var response Response
			err := json.NewDecoder(rec.Body).Decode(&response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSuccess, response.Success)
			assert.Nil(t, response.Data)
			require.NotNil(t, response.Error)
			assert.Equal(t, tt.expectedErrMessage, response.Error.Message)
			assert.Equal(t, tt.expectedErrCode, response.Error.Code)
			assert.Equal(t, tt.expectedDetails, response.Error.Details)
		})
	}
}

func TestWriteJSONWithMeta(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		data           interface{}
		meta           *Meta
		expectedStatus int
		expectedData   interface{}
		expectedMeta   *Meta
	}{
		{
			name:       "response with pagination meta",
			statusCode: http.StatusOK,
			data:       []string{"item1", "item2"},
			meta: &Meta{
				Total:    100,
				Limit:    10,
				Offset:   20,
				Page:     3,
				PageSize: 10,
			},
			expectedStatus: http.StatusOK,
			expectedData:   []interface{}{"item1", "item2"},
			expectedMeta: &Meta{
				Total:    100,
				Limit:    10,
				Offset:   20,
				Page:     3,
				PageSize: 10,
			},
		},
		{
			name:           "nil meta",
			statusCode:     http.StatusOK,
			data:           map[string]string{"key": "value"},
			meta:           nil,
			expectedStatus: http.StatusOK,
			expectedData:   map[string]interface{}{"key": "value"},
			expectedMeta:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteJSONWithMeta(rec, tt.statusCode, tt.data, tt.meta)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var response Response
			err := json.NewDecoder(rec.Body).Decode(&response)
			require.NoError(t, err)

			assert.True(t, response.Success)
			assert.Nil(t, response.Error)
			assert.Equal(t, tt.expectedData, response.Data)
			assert.Equal(t, tt.expectedMeta, response.Meta)
		})
	}
}

func TestMapDomainErrorToHTTP(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{
			name:           "ErrNotFound -> 404",
			err:            domain.ErrNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "ErrUserNotFound -> 404",
			err:            domain.ErrUserNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "ErrVideoNotFound -> 404",
			err:            domain.ErrVideoNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "ErrUnauthorized -> 401",
			err:            domain.ErrUnauthorized,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "ErrInvalidCredentials -> 401",
			err:            domain.ErrInvalidCredentials,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "ErrInvalidToken -> 401",
			err:            domain.ErrInvalidToken,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "ErrTokenExpired -> 401",
			err:            domain.ErrTokenExpired,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "ErrValidation -> 400",
			err:            domain.ErrValidation,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "ErrBadRequest -> 400",
			err:            domain.ErrBadRequest,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "ErrInvalidFormat -> 400",
			err:            domain.ErrInvalidFormat,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "ErrConflict -> 409",
			err:            domain.ErrConflict,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "ErrUserAlreadyExists -> 409",
			err:            domain.ErrUserAlreadyExists,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "ErrForbidden -> 403",
			err:            domain.ErrForbidden,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "ErrTooManyRequests -> 429",
			err:            domain.ErrTooManyRequests,
			expectedStatus: http.StatusTooManyRequests,
		},
		{
			name:           "ErrServiceUnavailable -> 503",
			err:            domain.ErrServiceUnavailable,
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "ErrIPFSUnavailable -> 503",
			err:            domain.ErrIPFSUnavailable,
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "ErrFileTooLarge -> 413",
			err:            domain.ErrFileTooLarge,
			expectedStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:           "DomainError with IPFS_UPLOAD_FAILED code -> 503",
			err:            domain.NewDomainError("IPFS_UPLOAD_FAILED", "IPFS upload failed"),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "DomainError with IPFS_PIN_FAILED code -> 503",
			err:            domain.NewDomainError("IPFS_PIN_FAILED", "IPFS pin failed"),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "DomainError with IPFS_NOT_CONFIGURED code -> 503",
			err:            domain.NewDomainError("IPFS_NOT_CONFIGURED", "IPFS not configured"),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "DomainError with STORAGE_ERROR code -> 500",
			err:            domain.NewDomainError("STORAGE_ERROR", "Storage error"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "DomainError with DB_ERROR code -> 500",
			err:            domain.NewDomainError("DB_ERROR", "Database error"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "DomainError with INVALID_PATH code -> 400",
			err:            domain.NewDomainError("INVALID_PATH", "Invalid path"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DomainError with BAD_REQUEST code -> 400",
			err:            domain.NewDomainError("BAD_REQUEST", "Bad request"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DomainError with UNAUTHORIZED code -> 401",
			err:            domain.NewDomainError("UNAUTHORIZED", "Unauthorized"),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "unknown error -> 500",
			err:            errors.New("unknown error"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "DomainError pointer with code",
			err:            &domain.DomainError{Code: "DB_ERROR", Message: "Database error"},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := MapDomainErrorToHTTP(tt.err)
			assert.Equal(t, tt.expectedStatus, status)
		})
	}
}

func TestMapDomainErrorCode(t *testing.T) {
	tests := []struct {
		name           string
		code           string
		expectedStatus int
		expectedOK     bool
	}{
		{
			name:           "IPFS_UPLOAD_FAILED",
			code:           "IPFS_UPLOAD_FAILED",
			expectedStatus: http.StatusServiceUnavailable,
			expectedOK:     true,
		},
		{
			name:           "DB_ERROR",
			code:           "DB_ERROR",
			expectedStatus: http.StatusInternalServerError,
			expectedOK:     true,
		},
		{
			name:           "INVALID_PATH",
			code:           "INVALID_PATH",
			expectedStatus: http.StatusBadRequest,
			expectedOK:     true,
		},
		{
			name:           "UNKNOWN_CODE",
			code:           "UNKNOWN_CODE",
			expectedStatus: 0,
			expectedOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ok := mapDomainErrorCode(tt.code)
			assert.Equal(t, tt.expectedStatus, status)
			assert.Equal(t, tt.expectedOK, ok)
		})
	}
}

func TestCheckErrorMapping(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		mapping struct {
			status int
			errors []error
		}
		expectedStatus int
		expectedOK     bool
	}{
		{
			name: "error matches in mapping",
			err:  domain.ErrNotFound,
			mapping: struct {
				status int
				errors []error
			}{
				status: http.StatusNotFound,
				errors: []error{domain.ErrNotFound, domain.ErrUserNotFound},
			},
			expectedStatus: http.StatusNotFound,
			expectedOK:     true,
		},
		{
			name: "error not in mapping",
			err:  domain.ErrUnauthorized,
			mapping: struct {
				status int
				errors []error
			}{
				status: http.StatusNotFound,
				errors: []error{domain.ErrNotFound, domain.ErrUserNotFound},
			},
			expectedStatus: 0,
			expectedOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ok := checkErrorMapping(tt.err, tt.mapping)
			assert.Equal(t, tt.expectedStatus, status)
			assert.Equal(t, tt.expectedOK, ok)
		})
	}
}

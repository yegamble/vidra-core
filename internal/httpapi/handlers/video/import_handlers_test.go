package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	importuc "athena/internal/usecase/import"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestImportHandlers_CreateImport_Success(t *testing.T) {
	mockService := new(MockImportService)
	mockValidator := new(MockURLValidator)
	handlers := NewImportHandlers(mockService, mockValidator)

	now := time.Now()
	expectedImport := &domain.VideoImport{
		ID:            "import-123",
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		Status:        domain.ImportStatusPending,
		TargetPrivacy: "private",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	mockValidator.On("ValidateVideoURL", "https://youtube.com/watch?v=test").Return(nil)
	mockService.On("ImportVideo", mock.Anything, mock.MatchedBy(func(req *importuc.ImportRequest) bool {
		return req.UserID == "user-123" && req.SourceURL == "https://youtube.com/watch?v=test"
	})).Return(expectedImport, nil)

	reqBody := CreateImportRequest{
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp ImportResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, "import-123", resp.ID)
	assert.Equal(t, "https://youtube.com/watch?v=test", resp.SourceURL)
	assert.Equal(t, "pending", resp.Status)

	mockService.AssertExpectations(t)
	mockValidator.AssertExpectations(t)
}

func TestImportHandlers_CreateImport_QuotaExceeded(t *testing.T) {
	mockService := new(MockImportService)
	mockValidator := new(MockURLValidator)
	handlers := NewImportHandlers(mockService, mockValidator)

	mockValidator.On("ValidateVideoURL", "https://youtube.com/watch?v=test").Return(nil)
	mockService.On("ImportVideo", mock.Anything, mock.Anything).Return(nil, domain.ErrImportQuotaExceeded)

	reqBody := CreateImportRequest{
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var resp ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Contains(t, resp.Message, "quota exceeded")

	mockService.AssertExpectations(t)
	mockValidator.AssertExpectations(t)
}

func TestImportHandlers_CreateImport_RateLimited(t *testing.T) {
	mockService := new(MockImportService)
	mockValidator := new(MockURLValidator)
	handlers := NewImportHandlers(mockService, mockValidator)

	mockValidator.On("ValidateVideoURL", "https://youtube.com/watch?v=test").Return(nil)
	mockService.On("ImportVideo", mock.Anything, mock.Anything).Return(nil, domain.ErrImportRateLimited)

	reqBody := CreateImportRequest{
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var resp ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Contains(t, resp.Message, "concurrent imports")

	mockService.AssertExpectations(t)
	mockValidator.AssertExpectations(t)
}

func TestImportHandlers_CreateImport_UnsupportedURL(t *testing.T) {
	mockService := new(MockImportService)
	mockValidator := new(MockURLValidator)
	handlers := NewImportHandlers(mockService, mockValidator)

	mockValidator.On("ValidateVideoURL", "https://unsupported.com/video").Return(nil)
	mockService.On("ImportVideo", mock.Anything, mock.Anything).Return(nil, domain.ErrImportUnsupportedURL)

	reqBody := CreateImportRequest{
		SourceURL:     "https://unsupported.com/video",
		TargetPrivacy: "private",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Contains(t, resp.Message, "unsupported")

	mockService.AssertExpectations(t)
	mockValidator.AssertExpectations(t)
}

func TestImportHandlers_CreateImport_MissingSourceURL(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	reqBody := CreateImportRequest{
		TargetPrivacy: "private",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Contains(t, resp.Message, "source_url is required")
}

func TestImportHandlers_CreateImport_InvalidJSON(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader([]byte("invalid json")))
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.CreateImport(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestImportHandlers_GetImport_Success(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	now := time.Now()
	expectedImport := &domain.VideoImport{
		ID:            "import-123",
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		Status:        domain.ImportStatusDownloading,
		Progress:      45,
		TargetPrivacy: "private",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	mockService.On("GetImport", mock.Anything, "import-123", "user-123").Return(expectedImport, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/imports/import-123", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))

	// Use chi router context for URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "import-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetImport(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ImportResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, "import-123", resp.ID)
	assert.Equal(t, "downloading", resp.Status)
	assert.Equal(t, 45, resp.Progress)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_GetImport_NotFound(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	mockService.On("GetImport", mock.Anything, "nonexistent", "user-123").Return(nil, domain.ErrImportNotFound)

	req := httptest.NewRequest("GET", "/api/v1/videos/imports/nonexistent", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.GetImport(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_ListImports_Success(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	now := time.Now()
	expectedImports := []*domain.VideoImport{
		{
			ID:            "import-1",
			UserID:        "user-123",
			SourceURL:     "https://youtube.com/watch?v=1",
			Status:        domain.ImportStatusCompleted,
			Progress:      100,
			TargetPrivacy: "private",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "import-2",
			UserID:        "user-123",
			SourceURL:     "https://youtube.com/watch?v=2",
			Status:        domain.ImportStatusDownloading,
			Progress:      50,
			TargetPrivacy: "private",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}

	mockService.On("ListUserImports", mock.Anything, "user-123", 20, 0).Return(expectedImports, 42, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/imports", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.ListImports(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ImportListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Len(t, resp.Imports, 2)
	assert.Equal(t, 42, resp.TotalCount)
	assert.Equal(t, 20, resp.Limit)
	assert.Equal(t, 0, resp.Offset)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_ListImports_WithPagination(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	now := time.Now()
	expectedImports := []*domain.VideoImport{
		{
			ID:            "import-3",
			UserID:        "user-123",
			SourceURL:     "https://youtube.com/watch?v=3",
			Status:        domain.ImportStatusPending,
			TargetPrivacy: "private",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}

	mockService.On("ListUserImports", mock.Anything, "user-123", 10, 20).Return(expectedImports, 42, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/imports?limit=10&offset=20", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.ListImports(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ImportListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Len(t, resp.Imports, 1)
	assert.Equal(t, 42, resp.TotalCount)
	assert.Equal(t, 10, resp.Limit)
	assert.Equal(t, 20, resp.Offset)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_ListImports_Error(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	mockService.On("ListUserImports", mock.Anything, "user-123", 20, 0).Return(nil, 0, errors.New("database error"))

	req := httptest.NewRequest("GET", "/api/v1/videos/imports", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))
	w := httptest.NewRecorder()

	handlers.ListImports(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_CancelImport_Success(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	mockService.On("CancelImport", mock.Anything, "import-123", "user-123").Return(nil)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/imports/import-123", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "import-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.CancelImport(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_CancelImport_NotFound(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	mockService.On("CancelImport", mock.Anything, "nonexistent", "user-123").Return(domain.ErrImportNotFound)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/imports/nonexistent", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.CancelImport(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	mockService.AssertExpectations(t)
}

func TestImportHandlers_CancelImport_Error(t *testing.T) {
	mockService := new(MockImportService)
	handlers := NewImportHandlers(mockService)

	mockService.On("CancelImport", mock.Anything, "import-123", "user-123").Return(errors.New("cancellation failed"))

	req := httptest.NewRequest("DELETE", "/api/v1/videos/imports/import-123", nil)
	req = req.WithContext(context.WithValue(req.Context(), "user_id", "user-123"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "import-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.CancelImport(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockService.AssertExpectations(t)
}

func TestParsePagination(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		expectedLimit  int
		expectedOffset int
	}{
		{
			name:           "default values",
			url:            "/api/v1/videos/imports",
			expectedLimit:  20,
			expectedOffset: 0,
		},
		{
			name:           "custom limit and offset",
			url:            "/api/v1/videos/imports?limit=50&offset=100",
			expectedLimit:  50,
			expectedOffset: 100,
		},
		{
			name:           "limit exceeds max",
			url:            "/api/v1/videos/imports?limit=200",
			expectedLimit:  20, // Should default to 20
			expectedOffset: 0,
		},
		{
			name:           "negative offset",
			url:            "/api/v1/videos/imports?offset=-10",
			expectedLimit:  20,
			expectedOffset: 0, // Should default to 0
		},
		{
			name:           "invalid limit",
			url:            "/api/v1/videos/imports?limit=invalid",
			expectedLimit:  20,
			expectedOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			limit, offset := parsePagination(req)
			assert.Equal(t, tt.expectedLimit, limit)
			assert.Equal(t, tt.expectedOffset, offset)
		})
	}
}

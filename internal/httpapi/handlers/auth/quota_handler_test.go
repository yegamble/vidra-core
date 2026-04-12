package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/httpapi/handlers/auth"
	"vidra-core/internal/middleware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockQuotaVideoRepo satisfies the auth.QuotaVideoRepository interface.
type mockQuotaVideoRepo struct {
	quotaUsed int64
	err       error
}

func (m *mockQuotaVideoRepo) GetVideoQuotaUsed(ctx context.Context, userID string) (int64, error) {
	return m.quotaUsed, m.err
}

func (m *mockQuotaVideoRepo) AppendOutputPath(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func TestGetVideoQuotaUsed_ReturnsQuota(t *testing.T) {
	repo := &mockQuotaVideoRepo{quotaUsed: 123456789}
	handler := auth.GetVideoQuotaUsedHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/video-quota-used", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-1")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp["success"].(bool))
	data := resp["data"].(map[string]interface{})
	assert.Contains(t, data, "videoQuotaUsed")
	assert.Contains(t, data, "videoQuotaUsedDaily")
}

func TestGetVideoQuotaUsed_ZeroWhenNoVideos(t *testing.T) {
	repo := &mockQuotaVideoRepo{quotaUsed: 0}
	handler := auth.GetVideoQuotaUsedHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/video-quota-used", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-1")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp["success"].(bool))
	data := resp["data"].(map[string]interface{})
	// Should be numeric zero, not absent
	assert.Equal(t, float64(0), data["videoQuotaUsed"])
}

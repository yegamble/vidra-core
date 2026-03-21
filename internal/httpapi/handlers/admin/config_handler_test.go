package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPublicConfig_ReturnsInstanceConfig(t *testing.T) {
	h := &InstanceHandlers{
		moderationRepo: nil,
		userRepo:       nil,
		videoRepo:      nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()

	h.GetPublicConfig(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	// Response should have instance, signup, video, features sections
	assert.Contains(t, resp, "instance")
	assert.Contains(t, resp, "signup")
	assert.Contains(t, resp, "video")
	assert.Contains(t, resp, "features")
}

func TestGetPublicConfig_NoAuthRequired(t *testing.T) {
	h := &InstanceHandlers{}

	// No auth header — should still return 200
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()

	h.GetPublicConfig(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetInstanceStats_ReturnsStats(t *testing.T) {
	h := &InstanceHandlers{
		moderationRepo: nil,
		userRepo:       nil,
		videoRepo:      nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/stats", nil)
	w := httptest.NewRecorder()

	h.GetPublicStats(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	// Response should have stat fields
	assert.Contains(t, resp, "totalUsers")
	assert.Contains(t, resp, "totalVideos")
	assert.Contains(t, resp, "totalLocalVideos")
}

package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/httpapi/shared"
)

func TestReportPlaybackMetrics_Success(t *testing.T) {
	h := NewPlaybackHandler()
	body := `{
		"playerMode": "webtorrent",
		"resolution": 720,
		"fps": 30,
		"p2pEnabled": true,
		"p2pPeers": 5,
		"resolutionChanges": 2,
		"errors": 0,
		"downloadedBytesP2P": 1048576,
		"uploadedBytesP2P": 524288,
		"videoId": "abc-123"
	}`

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp shared.Response
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.True(t, resp.Success)
}

func TestReportPlaybackMetrics_MinimalPayload(t *testing.T) {
	h := NewPlaybackHandler()
	body := `{"videoId": "test-video-123"}`

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReportPlaybackMetrics_MissingVideoID(t *testing.T) {
	h := NewPlaybackHandler()
	body := `{"playerMode": "webtorrent", "resolution": 720}`

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReportPlaybackMetrics_InvalidJSON(t *testing.T) {
	h := NewPlaybackHandler()

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReportPlaybackMetrics_EmptyBody(t *testing.T) {
	h := NewPlaybackHandler()

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReportPlaybackMetrics_ResponseContainsReceivedAt(t *testing.T) {
	h := NewPlaybackHandler()
	body := `{"videoId": "test-123"}`

	req := httptest.NewRequest("POST", "/api/v1/metrics/playback", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.ReportPlaybackMetrics(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp shared.Response
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.True(t, resp.Success)

	// Verify the data contains receivedAt
	dataBytes, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var data PlaybackMetricsResponse
	require.NoError(t, json.Unmarshal(dataBytes, &data))
	assert.False(t, data.ReceivedAt.IsZero())
}

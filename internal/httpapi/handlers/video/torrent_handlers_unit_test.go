package video

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/torrent"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTorrentManager struct {
	getVideoTorrentFunc func(ctx context.Context, videoID uuid.UUID) (*domain.VideoTorrent, error)
	getGlobalStatsFunc  func(ctx context.Context) (map[string]interface{}, error)
}

func (m *mockTorrentManager) GetVideoTorrent(ctx context.Context, videoID uuid.UUID) (*domain.VideoTorrent, error) {
	if m.getVideoTorrentFunc != nil {
		return m.getVideoTorrentFunc(ctx, videoID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTorrentManager) GetGlobalStats(ctx context.Context) (map[string]interface{}, error) {
	if m.getGlobalStatsFunc != nil {
		return m.getGlobalStatsFunc(ctx)
	}
	return nil, errors.New("not implemented")
}

type mockTorrentTracker struct {
	getStatsFunc        func() torrent.TrackerStats
	getSwarmInfoFunc    func(infoHash string) map[string]interface{}
	handleWebSocketFunc func(w http.ResponseWriter, r *http.Request)
}

func (m *mockTorrentTracker) GetStats() torrent.TrackerStats {
	if m.getStatsFunc != nil {
		return m.getStatsFunc()
	}
	return torrent.TrackerStats{}
}

func (m *mockTorrentTracker) GetSwarmInfo(infoHash string) map[string]interface{} {
	if m.getSwarmInfoFunc != nil {
		return m.getSwarmInfoFunc(infoHash)
	}
	return nil
}

func (m *mockTorrentTracker) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if m.handleWebSocketFunc != nil {
		m.handleWebSocketFunc(w, r)
	}
}

func TestGetVideoTorrentFile(t *testing.T) {
	videoID := uuid.New()

	t.Run("invalid video ID", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid/torrent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "invalid-uuid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoTorrentFile(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid video ID")
	})

	t.Run("torrent not found", func(t *testing.T) {
		manager := &mockTorrentManager{
			getVideoTorrentFunc: func(ctx context.Context, vid uuid.UUID) (*domain.VideoTorrent, error) {
				return nil, errors.New("not found")
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/torrent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoTorrentFile(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Torrent not found")
	})

	t.Run("torrent file not found on disk", func(t *testing.T) {
		manager := &mockTorrentManager{
			getVideoTorrentFunc: func(ctx context.Context, vid uuid.UUID) (*domain.VideoTorrent, error) {
				return &domain.VideoTorrent{
					VideoID:         videoID,
					InfoHash:        "abcdef123456",
					TorrentFilePath: "/nonexistent/path/to/torrent.torrent",
					MagnetURI:       "magnet:?xt=urn:btih:abcdef123456",
				}, nil
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/torrent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoTorrentFile(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Torrent file not found")
	})

	t.Run("success - serves torrent file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.torrent")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		torrentData := []byte("d8:announce10:example.com13:creation datei123456789e4:info5:teste")
		_, err = tmpFile.Write(torrentData)
		require.NoError(t, err)
		require.NoError(t, tmpFile.Close())

		manager := &mockTorrentManager{
			getVideoTorrentFunc: func(ctx context.Context, vid uuid.UUID) (*domain.VideoTorrent, error) {
				return &domain.VideoTorrent{
					VideoID:         videoID,
					InfoHash:        "abcdef123456",
					TorrentFilePath: tmpFile.Name(),
					MagnetURI:       "magnet:?xt=urn:btih:abcdef123456",
				}, nil
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/torrent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoTorrentFile(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/x-bittorrent", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
		assert.Equal(t, torrentData, w.Body.Bytes())
	})
}

func TestGetVideoMagnetURI(t *testing.T) {
	videoID := uuid.New()

	t.Run("success", func(t *testing.T) {
		torrentData := &domain.VideoTorrent{
			VideoID:   videoID,
			InfoHash:  "abcdef123456",
			MagnetURI: "magnet:?xt=urn:btih:abcdef123456",
		}

		manager := &mockTorrentManager{
			getVideoTorrentFunc: func(ctx context.Context, vid uuid.UUID) (*domain.VideoTorrent, error) {
				assert.Equal(t, videoID, vid)
				return torrentData, nil
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/magnet", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoMagnetURI(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				VideoID   uuid.UUID `json:"video_id"`
				InfoHash  string    `json:"info_hash"`
				MagnetURI string    `json:"magnet_uri"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, videoID, resp.Data.VideoID)
		assert.Equal(t, "abcdef123456", resp.Data.InfoHash)
		assert.Equal(t, "magnet:?xt=urn:btih:abcdef123456", resp.Data.MagnetURI)
	})

	t.Run("invalid video ID", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid/magnet", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "invalid-uuid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoMagnetURI(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "invalid_video_id", resp.Data.Error)
	})

	t.Run("torrent not found", func(t *testing.T) {
		manager := &mockTorrentManager{
			getVideoTorrentFunc: func(ctx context.Context, vid uuid.UUID) (*domain.VideoTorrent, error) {
				return nil, errors.New("not found")
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/magnet", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoMagnetURI(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "torrent_not_found", resp.Data.Error)
	})
}

func TestGetTorrentStats(t *testing.T) {
	t.Run("success with manager and tracker", func(t *testing.T) {
		managerStats := map[string]interface{}{
			"active_torrents": 42,
			"total_uploaded":  1024000,
		}

		trackerStats := torrent.TrackerStats{
			TotalAnnounces:    100,
			TotalScrapes:      50,
			ActiveConnections: 25,
			TotalPeers:        150,
			TotalSwarms:       10,
			AnnounceErrors:    2,
			ConnectionErrors:  1,
			StartTime:         time.Unix(1234567890, 0),
		}

		manager := &mockTorrentManager{
			getGlobalStatsFunc: func(ctx context.Context) (map[string]interface{}, error) {
				return managerStats, nil
			},
		}

		tracker := &mockTorrentTracker{
			getStatsFunc: func() torrent.TrackerStats {
				return trackerStats
			},
		}

		handler := NewTorrentHandlers(manager, tracker)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/torrent/stats", nil)
		w := httptest.NewRecorder()
		handler.GetTorrentStats(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				Manager map[string]interface{} `json:"manager"`
				Tracker map[string]interface{} `json:"tracker"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, float64(42), resp.Data.Manager["active_torrents"])
		assert.Equal(t, float64(100), resp.Data.Tracker["total_announces"])
	})

	t.Run("success without tracker", func(t *testing.T) {
		managerStats := map[string]interface{}{
			"active_torrents": 10,
		}

		manager := &mockTorrentManager{
			getGlobalStatsFunc: func(ctx context.Context) (map[string]interface{}, error) {
				return managerStats, nil
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/torrent/stats", nil)
		w := httptest.NewRecorder()
		handler.GetTorrentStats(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				Manager map[string]interface{} `json:"manager"`
				Tracker map[string]interface{} `json:"tracker"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Nil(t, resp.Data.Tracker)
	})

	t.Run("manager error", func(t *testing.T) {
		manager := &mockTorrentManager{
			getGlobalStatsFunc: func(ctx context.Context) (map[string]interface{}, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewTorrentHandlers(manager, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/torrent/stats", nil)
		w := httptest.NewRecorder()
		handler.GetTorrentStats(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "stats_error", resp.Data.Error)
	})
}

func TestGetSwarmInfo(t *testing.T) {
	infoHash := "abcdef123456"

	t.Run("success", func(t *testing.T) {
		swarmInfo := map[string]interface{}{
			"seeders":   10,
			"leechers":  5,
			"completed": 100,
		}

		tracker := &mockTorrentTracker{
			getSwarmInfoFunc: func(hash string) map[string]interface{} {
				assert.Equal(t, infoHash, hash)
				return swarmInfo
			},
		}

		handler := NewTorrentHandlers(nil, tracker)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/swarm/"+infoHash, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("infoHash", infoHash)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetSwarmInfo(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				Seeders   float64 `json:"seeders"`
				Leechers  float64 `json:"leechers"`
				Completed float64 `json:"completed"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, float64(10), resp.Data.Seeders)
	})

	t.Run("empty info hash", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/swarm/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("infoHash", "")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetSwarmInfo(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "invalid_info_hash", resp.Data.Error)
	})

	t.Run("tracker not available", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/swarm/"+infoHash, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("infoHash", infoHash)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetSwarmInfo(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "tracker_unavailable", resp.Data.Error)
	})

	t.Run("swarm not found", func(t *testing.T) {
		tracker := &mockTorrentTracker{
			getSwarmInfoFunc: func(hash string) map[string]interface{} {
				return nil
			},
		}

		handler := NewTorrentHandlers(nil, tracker)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/swarm/"+infoHash, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("infoHash", infoHash)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetSwarmInfo(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "swarm_not_found", resp.Data.Error)
	})
}

func TestHandleTrackerWebSocket(t *testing.T) {
	t.Run("tracker not available", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tracker/ws", nil)
		w := httptest.NewRecorder()
		handler.HandleTrackerWebSocket(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "Tracker not enabled")
	})

	t.Run("delegates to tracker", func(t *testing.T) {
		var called bool

		tracker := &mockTorrentTracker{
			handleWebSocketFunc: func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			},
		}

		handler := NewTorrentHandlers(nil, tracker)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tracker/ws", nil)
		w := httptest.NewRecorder()
		handler.HandleTrackerWebSocket(w, req)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestGetTrackerStats(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		stats := torrent.TrackerStats{
			TotalAnnounces:    200,
			TotalScrapes:      100,
			ActiveConnections: 50,
			TotalPeers:        300,
			TotalSwarms:       20,
			AnnounceErrors:    5,
			ConnectionErrors:  3,
			StartTime:         time.Unix(1234567890, 0),
		}

		tracker := &mockTorrentTracker{
			getStatsFunc: func() torrent.TrackerStats {
				return stats
			},
		}

		handler := NewTorrentHandlers(nil, tracker)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tracker/stats", nil)
		w := httptest.NewRecorder()
		handler.GetTrackerStats(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				TotalAnnounces    float64   `json:"total_announces"`
				TotalScrapes      float64   `json:"total_scrapes"`
				ActiveConnections float64   `json:"active_connections"`
				TotalPeers        float64   `json:"total_peers"`
				TotalSwarms       float64   `json:"total_swarms"`
				AnnounceErrors    float64   `json:"announce_errors"`
				ConnectionErrors  float64   `json:"connection_errors"`
				StartTime         time.Time `json:"start_time"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, float64(200), resp.Data.TotalAnnounces)
		assert.Equal(t, float64(100), resp.Data.TotalScrapes)
		assert.Equal(t, float64(50), resp.Data.ActiveConnections)
	})

	t.Run("tracker not available", func(t *testing.T) {
		handler := NewTorrentHandlers(nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tracker/stats", nil)
		w := httptest.NewRecorder()
		handler.GetTrackerStats(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp struct {
			Data struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.False(t, resp.Success)
		assert.Equal(t, "tracker_unavailable", resp.Data.Error)
	})
}

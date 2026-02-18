package video

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/livestream"
	"athena/internal/middleware"
)

type mockStreamRepoForHLS struct {
	getByIDFunc func(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error)
}

func (m *mockStreamRepoForHLS) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, errors.New("GetByID not implemented")
}

func (m *mockStreamRepoForHLS) Create(ctx context.Context, stream *domain.LiveStream) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) Update(ctx context.Context, stream *domain.LiveStream) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) Delete(ctx context.Context, id uuid.UUID) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	return nil, errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) EndStream(ctx context.Context, id uuid.UUID) error {
	return errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	return 0, errors.New("not implemented")
}
func (m *mockStreamRepoForHLS) GetChannelByStreamID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockStreamRepoForHLS) UpdateWaitingRoom(_ context.Context, _ uuid.UUID, _ bool, _ string) error {
	return nil
}
func (m *mockStreamRepoForHLS) ScheduleStream(_ context.Context, _ uuid.UUID, _ *time.Time, _ *time.Time, _ bool, _ string) error {
	return nil
}
func (m *mockStreamRepoForHLS) CancelSchedule(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *mockStreamRepoForHLS) GetScheduledStreams(_ context.Context, _, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockStreamRepoForHLS) GetUpcomingStreams(_ context.Context, _ uuid.UUID, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}

type mockHLSTranscoder struct {
	isTranscodingFunc func(streamID uuid.UUID) bool
	getSessionFunc    func(streamID uuid.UUID) (*livestream.TranscodeSession, bool)
}

func (m *mockHLSTranscoder) IsTranscoding(streamID uuid.UUID) bool {
	if m.isTranscodingFunc != nil {
		return m.isTranscodingFunc(streamID)
	}
	return false
}

func (m *mockHLSTranscoder) GetSession(streamID uuid.UUID) (*livestream.TranscodeSession, bool) {
	if m.getSessionFunc != nil {
		return m.getSessionFunc(streamID)
	}
	return nil, false
}

func TestGetMasterPlaylist(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		setupRepo      func() *mockStreamRepoForHLS
		expectedStatus int
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "invalid stream ID",
			streamID:       "invalid-uuid",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_STREAM_ID")
			},
		},
		{
			name:     "stream not found",
			streamID: uuid.New().String(),
			setupRepo: func() *mockStreamRepoForHLS {
				return &mockStreamRepoForHLS{
					getByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
						return nil, domain.ErrNotFound
					},
				}
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "STREAM_NOT_FOUND")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HLSOutputDir: "/tmp/hls",
			}
			repo := tt.setupRepo()
			transcoder := &mockHLSTranscoder{}

			h := NewHLSHandlers(cfg, repo, transcoder, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+tt.streamID+"/hls/master.m3u8", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.streamID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()

			h.GetMasterPlaylist(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

func TestGetStreamHLSInfo(t *testing.T) {
	tests := []struct {
		name            string
		streamID        string
		setupRepo       func() *mockStreamRepoForHLS
		setupTranscoder func() *mockHLSTranscoder
		expectedStatus  int
		checkResponse   func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:            "invalid stream ID",
			streamID:        "invalid-uuid",
			setupRepo:       func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			setupTranscoder: func() *mockHLSTranscoder { return &mockHLSTranscoder{} },
			expectedStatus:  http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_STREAM_ID")
			},
		},
		{
			name:     "stream not found",
			streamID: uuid.New().String(),
			setupRepo: func() *mockStreamRepoForHLS {
				return &mockStreamRepoForHLS{
					getByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
						return nil, domain.ErrNotFound
					},
				}
			},
			setupTranscoder: func() *mockHLSTranscoder { return &mockHLSTranscoder{} },
			expectedStatus:  http.StatusNotFound,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "STREAM_NOT_FOUND")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			repo := tt.setupRepo()
			transcoder := tt.setupTranscoder()

			h := NewHLSHandlers(cfg, repo, transcoder, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+tt.streamID+"/hls/info", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.streamID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()

			h.GetStreamHLSInfo(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

func TestGetVariantPlaylist(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		variant        string
		setupRepo      func() *mockStreamRepoForHLS
		expectedStatus int
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "invalid stream ID",
			streamID:       "invalid-uuid",
			variant:        "720p",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_STREAM_ID")
			},
		},
		{
			name:           "empty variant",
			streamID:       uuid.New().String(),
			variant:        "",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_VARIANT")
			},
		},
		{
			name:           "invalid variant name",
			streamID:       uuid.New().String(),
			variant:        "invalid-variant",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_VARIANT")
			},
		},
		{
			name:     "stream not found",
			streamID: uuid.New().String(),
			variant:  "720p",
			setupRepo: func() *mockStreamRepoForHLS {
				return &mockStreamRepoForHLS{
					getByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
						return nil, domain.ErrNotFound
					},
				}
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "STREAM_NOT_FOUND")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HLSOutputDir: "/tmp/hls",
			}
			repo := tt.setupRepo()
			transcoder := &mockHLSTranscoder{}

			h := NewHLSHandlers(cfg, repo, transcoder, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+tt.streamID+"/hls/"+tt.variant+"/index.m3u8", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.streamID)
			rctx.URLParams.Add("variant", tt.variant)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()

			h.GetVariantPlaylist(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

func TestCheckStreamAccess(t *testing.T) {
	userID := uuid.New()
	otherUserID := uuid.New()

	tests := []struct {
		name        string
		stream      *domain.LiveStream
		withUserID  bool
		userID      uuid.UUID
		expectError bool
		errorCode   string
	}{
		{
			name: "public stream - no auth required",
			stream: &domain.LiveStream{
				Privacy: "public",
			},
			withUserID:  false,
			expectError: false,
		},
		{
			name: "unlisted stream - no auth required",
			stream: &domain.LiveStream{
				Privacy: "unlisted",
			},
			withUserID:  false,
			expectError: false,
		},
		{
			name: "private stream - not authenticated",
			stream: &domain.LiveStream{
				Privacy: "private",
				UserID:  userID,
			},
			withUserID:  false,
			expectError: true,
			errorCode:   "UNAUTHORIZED",
		},
		{
			name: "private stream - owner access allowed",
			stream: &domain.LiveStream{
				Privacy: "private",
				UserID:  userID,
			},
			withUserID:  true,
			userID:      userID,
			expectError: false,
		},
		{
			name: "private stream - non-owner forbidden",
			stream: &domain.LiveStream{
				Privacy: "private",
				UserID:  userID,
			},
			withUserID:  true,
			userID:      otherUserID,
			expectError: true,
			errorCode:   "FORBIDDEN",
		},
		{
			name: "invalid privacy setting",
			stream: &domain.LiveStream{
				Privacy: "invalid",
			},
			withUserID:  false,
			expectError: true,
			errorCode:   "INVALID_PRIVACY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HLSHandlers{}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.withUserID {
				ctx := context.WithValue(req.Context(), middleware.UserIDKey, tt.userID.String())
				req = req.WithContext(ctx)
			}

			err := h.checkStreamAccess(req, tt.stream)

			if tt.expectError {
				assert.Error(t, err)
				domainErr, ok := err.(domain.DomainError)
				assert.True(t, ok, "expected DomainError")
				assert.Equal(t, tt.errorCode, domainErr.Code)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetScheme(t *testing.T) {
	tests := []struct {
		name           string
		setupRequest   func() *http.Request
		expectedScheme string
	}{
		{
			name: "HTTPS with TLS",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
				req.TLS = &tls.ConnectionState{}
				return req
			},
			expectedScheme: "https",
		},
		{
			name: "HTTP with X-Forwarded-Proto header",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
				req.Header.Set("X-Forwarded-Proto", "https")
				return req
			},
			expectedScheme: "https",
		},
		{
			name: "plain HTTP",
			setupRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			},
			expectedScheme: "http",
		},
		{
			name: "TLS takes precedence over header",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
				req.TLS = &tls.ConnectionState{}
				req.Header.Set("X-Forwarded-Proto", "http")
				return req
			},
			expectedScheme: "https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.setupRequest()
			result := getScheme(req)
			assert.Equal(t, tt.expectedScheme, result)
		})
	}
}

func TestIsValidSegmentName(t *testing.T) {
	tests := []struct {
		name     string
		segment  string
		expected bool
	}{
		{"valid segment", "segment_001.ts", true},
		{"valid segment with large number", "segment_999999.ts", true},
		{"missing prefix", "001.ts", false},
		{"missing suffix", "segment_001", false},
		{"path traversal with ..", "segment_../001.ts", false},
		{"path traversal with slash", "segment_001/../.ts", false},
		{"backslash", "segment_001\\.ts", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidSegmentName(tt.segment)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetSegment(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		variant        string
		segment        string
		setupRepo      func() *mockStreamRepoForHLS
		expectedStatus int
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "invalid stream ID",
			streamID:       "invalid-uuid",
			variant:        "720p",
			segment:        "segment_001.ts",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_STREAM_ID")
			},
		},
		{
			name:           "empty variant",
			streamID:       uuid.New().String(),
			variant:        "",
			segment:        "segment_001.ts",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_VARIANT")
			},
		},
		{
			name:           "empty segment",
			streamID:       uuid.New().String(),
			variant:        "720p",
			segment:        "",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_SEGMENT")
			},
		},
		{
			name:           "invalid variant name",
			streamID:       uuid.New().String(),
			variant:        "invalid",
			segment:        "segment_001.ts",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_PATH")
			},
		},
		{
			name:           "invalid segment name",
			streamID:       uuid.New().String(),
			variant:        "720p",
			segment:        "../../../etc/passwd",
			setupRepo:      func() *mockStreamRepoForHLS { return &mockStreamRepoForHLS{} },
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "INVALID_PATH")
			},
		},
		{
			name:     "stream not found",
			streamID: uuid.New().String(),
			variant:  "720p",
			segment:  "segment_001.ts",
			setupRepo: func() *mockStreamRepoForHLS {
				return &mockStreamRepoForHLS{
					getByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
						return nil, domain.ErrNotFound
					},
				}
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Contains(t, w.Body.String(), "STREAM_NOT_FOUND")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HLSOutputDir: "/tmp/hls",
			}
			repo := tt.setupRepo()
			transcoder := &mockHLSTranscoder{}

			h := NewHLSHandlers(cfg, repo, transcoder, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+tt.streamID+"/hls/"+tt.variant+"/"+tt.segment, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.streamID)
			rctx.URLParams.Add("variant", tt.variant)
			rctx.URLParams.Add("segment", tt.segment)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()

			h.GetSegment(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

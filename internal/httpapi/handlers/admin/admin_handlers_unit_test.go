package admin

import (
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// withURLParam injects a chi URL parameter into the request context.
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withRole adds middleware.UserRoleKey to the request context.
func withRole(r *http.Request, role string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserRoleKey, role))
}

// withUserID adds middleware.UserIDKey to the request context.
func withUserID(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserIDKey, id))
}

// newHandlers creates InstanceHandlers with the given mock repos.
// moderationRepo is nil-db (panics if actually called), so only use for paths
// that do NOT touch the moderation repository.
func newHandlers(userRepo *MockUserRepo, videoRepo *MockVideoRepo) *InstanceHandlers {
	return NewInstanceHandlers(&repository.ModerationRepository{}, userRepo, videoRepo)
}

// decodeResponse unmarshals the standard JSON response envelope.
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out
}

// ---------------------------------------------------------------------------
// OEmbed handler tests
// ---------------------------------------------------------------------------

func TestOEmbed_MissingURL(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/oembed", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	resp := decodeResponse(t, rr)
	assert.False(t, resp["success"].(bool))
}

func TestOEmbed_InvalidFormat(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v1&format=yaml", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestOEmbed_InvalidVideoURL(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})

	tests := []struct {
		name string
		url  string
	}{
		{"no videos path", "http://example.com/something/v1"},
		{"empty after /videos/", "http://example.com/videos/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/oembed?url="+tt.url, nil)
			rr := httptest.NewRecorder()
			h.OEmbed(rr, req)
			assert.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestOEmbed_VideoNotFound(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/nonexistent", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestOEmbed_PrivateVideoReturnsNotFound(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-private",
			UserID:  "u1",
			Privacy: domain.PrivacyPrivate,
		},
	}
	h := newHandlers(&MockUserRepo{}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-private", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestOEmbed_UnlistedVideoReturnsNotFound(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-unlisted",
			UserID:  "u1",
			Privacy: domain.PrivacyUnlisted,
		},
	}
	h := newHandlers(&MockUserRepo{}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-unlisted", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestOEmbed_JSONSuccess(t *testing.T) {
	videoID := "vid-123"
	userID := "usr-456"

	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:           videoID,
			UserID:       userID,
			Title:        "My Test Video",
			Privacy:      domain.PrivacyPublic,
			Duration:     300,
			ThumbnailCID: "QmThumbnailCID",
		},
	}
	userRepo := &MockUserRepo{
		User: &domain.User{
			ID:          userID,
			DisplayName: "Alice",
		},
	}
	h := newHandlers(userRepo, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/"+videoID, nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	assert.Equal(t, "1.0", resp.Version)
	assert.Equal(t, "video", resp.Type)
	assert.Equal(t, "My Test Video", resp.Title)
	assert.Equal(t, "Alice", resp.AuthorName)
	assert.Equal(t, 640, resp.Width)
	assert.Equal(t, 360, resp.Height)
	assert.Equal(t, 300, resp.Duration)
	assert.NotEmpty(t, resp.ThumbnailURL)
	assert.Equal(t, 640, resp.ThumbnailWidth)
	assert.Equal(t, 360, resp.ThumbnailHeight)
	assert.Contains(t, resp.HTML, videoID)
}

func TestOEmbed_JSONDefaultFormat(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v1",
			UserID:  "u1",
			Title:   "Test",
			Privacy: domain.PrivacyPublic,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "Bob"}}, videoRepo)

	// No format param => defaults to json
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v1", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")
}

func TestOEmbed_XMLSuccess(t *testing.T) {
	videoID := "vid-xml"
	userID := "usr-xml"

	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:       videoID,
			UserID:   userID,
			Title:    "XML Video",
			Privacy:  domain.PrivacyPublic,
			Duration: 60,
		},
	}
	userRepo := &MockUserRepo{
		User: &domain.User{
			ID:          userID,
			DisplayName: "Charlie",
		},
	}
	h := newHandlers(userRepo, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/"+videoID+"&format=xml", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/xml")

	var resp OEmbedResponse
	require.NoError(t, xml.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "XML Video", resp.Title)
	assert.Equal(t, "Charlie", resp.AuthorName)
	assert.Equal(t, 60, resp.Duration)
}

func TestOEmbed_CustomMaxWidthAndHeight(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-dim",
			UserID:  "u1",
			Title:   "Sized",
			Privacy: domain.PrivacyPublic,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "D"}}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-dim&maxwidth=1280&maxheight=720", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 1280, resp.Width)
	assert.Equal(t, 720, resp.Height)
}

func TestOEmbed_InvalidMaxWidthFallsBackToDefault(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-bad-dim",
			UserID:  "u1",
			Title:   "Bad Dims",
			Privacy: domain.PrivacyPublic,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "E"}}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-bad-dim&maxwidth=abc&maxheight=-5", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	// Invalid maxwidth falls back to default 640, invalid maxheight to default 360
	assert.Equal(t, 640, resp.Width)
	assert.Equal(t, 360, resp.Height)
}

func TestOEmbed_UploaderNotFound_FallsBackToUnknown(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-no-user",
			UserID:  "u-gone",
			Title:   "Orphan Video",
			Privacy: domain.PrivacyPublic,
		},
	}
	// MockUserRepo with no User set => GetByID returns NOT_FOUND
	h := newHandlers(&MockUserRepo{}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-no-user", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "Unknown User", resp.AuthorName)
}

func TestOEmbed_NoThumbnailCID(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:           "v-no-thumb",
			UserID:       "u1",
			Title:        "No Thumbnail",
			Privacy:      domain.PrivacyPublic,
			ThumbnailCID: "", // empty
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "F"}}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-no-thumb", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Empty(t, resp.ThumbnailURL)
	assert.Zero(t, resp.ThumbnailWidth)
	assert.Zero(t, resp.ThumbnailHeight)
}

func TestOEmbed_ZeroDuration(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:       "v-zero-dur",
			UserID:   "u1",
			Title:    "Zero Duration",
			Privacy:  domain.PrivacyPublic,
			Duration: 0,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "G"}}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-zero-dur", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Zero(t, resp.Duration)
}

func TestOEmbed_URLWithQueryParams(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-query",
			UserID:  "u1",
			Title:   "Query Params",
			Privacy: domain.PrivacyPublic,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "H"}}, videoRepo)

	// Video URL itself contains query params after the ID
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-query?t=10&autoplay=1", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
}

func TestOEmbed_ProviderName(t *testing.T) {
	videoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:      "v-prov",
			UserID:  "u1",
			Title:   "Provider",
			Privacy: domain.PrivacyPublic,
		},
	}
	h := newHandlers(&MockUserRepo{User: &domain.User{ID: "u1", DisplayName: "I"}}, videoRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=http://example.com/videos/v-prov", nil)
	rr := httptest.NewRecorder()

	h.OEmbed(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp OEmbedResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "Athena Video Platform", resp.ProviderName)
}

// ---------------------------------------------------------------------------
// ListInstanceConfigs authorization tests
// ---------------------------------------------------------------------------

func TestListInstanceConfigs_NoAuth(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config", nil)
	rr := httptest.NewRecorder()

	// No UserRoleKey, no UserIDKey in context
	h.ListInstanceConfigs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeResponse(t, rr)
	assert.False(t, resp["success"].(bool))
}

func TestListInstanceConfigs_NonAdminRole(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config", nil)
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.ListInstanceConfigs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestListInstanceConfigs_ModeratorRole(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config", nil)
	req = withRole(req, "moderator")
	rr := httptest.NewRecorder()

	h.ListInstanceConfigs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// GetInstanceConfig authorization + validation tests
// ---------------------------------------------------------------------------

func TestGetInstanceConfig_NoAuth(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config/some_key", nil)
	req = withURLParam(req, "key", "some_key")
	rr := httptest.NewRecorder()

	h.GetInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetInstanceConfig_NonAdminRole(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config/some_key", nil)
	req = withURLParam(req, "key", "some_key")
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.GetInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetInstanceConfig_MissingKey(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config/", nil)
	// Admin role set but no key URL param
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetInstanceConfig(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// UpdateInstanceConfig authorization + validation tests
// ---------------------------------------------------------------------------

func TestUpdateInstanceConfig_NoAuth(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	body := `{"value": "test", "is_public": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/instance/config/some_key", strings.NewReader(body))
	req = withURLParam(req, "key", "some_key")
	rr := httptest.NewRecorder()

	h.UpdateInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestUpdateInstanceConfig_NonAdminRole(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	body := `{"value": "test", "is_public": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/instance/config/some_key", strings.NewReader(body))
	req = withURLParam(req, "key", "some_key")
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.UpdateInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestUpdateInstanceConfig_MissingKey(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	body := `{"value": "test", "is_public": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/instance/config/", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.UpdateInstanceConfig(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateInstanceConfig_InvalidBody(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/instance/config/some_key", strings.NewReader("{bad json"))
	req = withURLParam(req, "key", "some_key")
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.UpdateInstanceConfig(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// Auth fallback path: UserIDKey present but no UserRoleKey
// (This path calls moderationRepo.GetUserRole which needs a real DB.
//  We test that nil UserIDKey triggers Forbidden before the repo call.)
// ---------------------------------------------------------------------------

func TestListInstanceConfigs_UserIDKeyNil(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	// No UserRoleKey (triggers else branch), no UserIDKey => should return 403
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config", nil)
	rr := httptest.NewRecorder()

	h.ListInstanceConfigs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetInstanceConfig_UserIDKeyNil(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/instance/config/key1", nil)
	req = withURLParam(req, "key", "key1")
	rr := httptest.NewRecorder()

	h.GetInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestUpdateInstanceConfig_UserIDKeyNil(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})
	body := `{"value": "test", "is_public": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/instance/config/key1", strings.NewReader(body))
	req = withURLParam(req, "key", "key1")
	rr := httptest.NewRecorder()

	h.UpdateInstanceConfig(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// NewInstanceHandlers constructor
// ---------------------------------------------------------------------------

func TestNewInstanceHandlers(t *testing.T) {
	userRepo := &MockUserRepo{}
	videoRepo := &MockVideoRepo{}
	modRepo := &repository.ModerationRepository{}

	h := NewInstanceHandlers(modRepo, userRepo, videoRepo)

	require.NotNil(t, h)
	assert.Equal(t, modRepo, h.moderationRepo)
	assert.Equal(t, userRepo, h.userRepo)
	assert.Equal(t, videoRepo, h.videoRepo)
}

// ---------------------------------------------------------------------------
// OEmbed table-driven edge cases
// ---------------------------------------------------------------------------

func TestOEmbed_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		queryString    string
		video          *domain.Video
		user           *domain.User
		expectedStatus int
		checkBody      func(t *testing.T, rr *httptest.ResponseRecorder)
	}{
		{
			name:           "missing url param",
			queryString:    "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "empty url param",
			queryString:    "url=",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "url without /videos/ path",
			queryString:    "url=http://example.com/channels/ch1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "format=xml but video not found",
			queryString:    "url=http://example.com/videos/missing&format=xml",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:        "explicit format=json",
			queryString: "url=http://example.com/videos/v-explicit-json&format=json",
			video: &domain.Video{
				ID:      "v-explicit-json",
				UserID:  "u1",
				Title:   "Explicit JSON",
				Privacy: domain.PrivacyPublic,
			},
			user:           &domain.User{ID: "u1", DisplayName: "Tester"},
			expectedStatus: http.StatusOK,
			checkBody: func(t *testing.T, rr *httptest.ResponseRecorder) {
				assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")
			},
		},
		{
			name:        "format=xml success",
			queryString: "url=http://example.com/videos/v-xml-ok&format=xml",
			video: &domain.Video{
				ID:      "v-xml-ok",
				UserID:  "u1",
				Title:   "XML OK",
				Privacy: domain.PrivacyPublic,
			},
			user:           &domain.User{ID: "u1", DisplayName: "XML User"},
			expectedStatus: http.StatusOK,
			checkBody: func(t *testing.T, rr *httptest.ResponseRecorder) {
				assert.Contains(t, rr.Header().Get("Content-Type"), "application/xml")
			},
		},
		{
			name:        "maxwidth=0 uses default",
			queryString: "url=http://example.com/videos/v-zero-w&maxwidth=0",
			video: &domain.Video{
				ID:      "v-zero-w",
				UserID:  "u1",
				Title:   "Zero W",
				Privacy: domain.PrivacyPublic,
			},
			user:           &domain.User{ID: "u1", DisplayName: "W"},
			expectedStatus: http.StatusOK,
			checkBody: func(t *testing.T, rr *httptest.ResponseRecorder) {
				var resp OEmbedResponse
				require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
				// maxwidth=0 => not positive so default 640 is used
				assert.Equal(t, 640, resp.Width)
			},
		},
		{
			name:        "video with trailing slash in URL",
			queryString: "url=http://example.com/videos/v-trail/",
			video: &domain.Video{
				ID:      "v-trail",
				UserID:  "u1",
				Title:   "Trailing",
				Privacy: domain.PrivacyPublic,
			},
			user:           &domain.User{ID: "u1", DisplayName: "Trail"},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoRepo := &MockVideoRepo{Video: tt.video}
			userRepo := &MockUserRepo{User: tt.user}
			h := newHandlers(userRepo, videoRepo)

			req := httptest.NewRequest(http.MethodGet, "/api/oembed?"+tt.queryString, nil)
			rr := httptest.NewRecorder()

			h.OEmbed(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code, "status mismatch for %s", tt.name)
			if tt.checkBody != nil {
				tt.checkBody(t, rr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Admin authorization table-driven tests
// ---------------------------------------------------------------------------

func TestAdminHandlers_AuthorizationMatrix(t *testing.T) {
	h := newHandlers(&MockUserRepo{}, &MockVideoRepo{})

	type handlerFunc func(http.ResponseWriter, *http.Request)

	handlers := map[string]handlerFunc{
		"ListInstanceConfigs":  h.ListInstanceConfigs,
		"GetInstanceConfig":    h.GetInstanceConfig,
		"UpdateInstanceConfig": h.UpdateInstanceConfig,
	}

	for name, handler := range handlers {
		t.Run(name+"_no_context", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rr := httptest.NewRecorder()
			handler(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
		})

		t.Run(name+"_user_role", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req = withRole(req, "user")
			rr := httptest.NewRecorder()
			handler(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
		})

		t.Run(name+"_moderator_role", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req = withRole(req, "moderator")
			rr := httptest.NewRecorder()
			handler(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
		})

		t.Run(name+"_empty_role", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req = withRole(req, "")
			rr := httptest.NewRecorder()
			handler(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
		})
	}
}

package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

type MockBlacklistRepository struct {
	mock.Mock
}

func (m *MockBlacklistRepository) AddToBlacklist(ctx context.Context, entry *domain.VideoBlacklist) error {
	return m.Called(ctx, entry).Error(0)
}
func (m *MockBlacklistRepository) RemoveFromBlacklist(ctx context.Context, videoID uuid.UUID) error {
	return m.Called(ctx, videoID).Error(0)
}
func (m *MockBlacklistRepository) GetByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.VideoBlacklist, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoBlacklist), args.Error(1)
}
func (m *MockBlacklistRepository) List(ctx context.Context, limit, offset int) ([]*domain.VideoBlacklist, int, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]*domain.VideoBlacklist), args.Int(1), args.Error(2)
}

var _ BlacklistRepository = (*MockBlacklistRepository)(nil)

func withAdminContext(r *http.Request) *http.Request {
	adminID := uuid.New()
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, adminID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")
	return r.WithContext(ctx)
}

func requestWithVideoID(method, url string, body []byte, videoID uuid.UUID) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, url, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestAddToBlacklist_Success(t *testing.T) {
	repo := &MockBlacklistRepository{}
	videoID := uuid.New()

	repo.On("GetByVideoID", mock.Anything, videoID).Return(nil, domain.ErrNotFound)
	repo.On("AddToBlacklist", mock.Anything, mock.MatchedBy(func(b *domain.VideoBlacklist) bool {
		return b.VideoID == videoID
	})).Return(nil)

	handlers := NewBlacklistHandlers(repo)
	body, _ := json.Marshal(map[string]interface{}{"reason": "NSFW content", "unfederated": false})
	req := requestWithVideoID(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/blacklist", body, videoID)
	req = withAdminContext(req)
	w := httptest.NewRecorder()

	handlers.AddToBlacklist(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
}

func TestAddToBlacklist_AlreadyBlacklisted(t *testing.T) {
	repo := &MockBlacklistRepository{}
	videoID := uuid.New()
	existing := &domain.VideoBlacklist{ID: uuid.New(), VideoID: videoID, Reason: "spam", CreatedAt: time.Now()}

	repo.On("GetByVideoID", mock.Anything, videoID).Return(existing, nil)

	handlers := NewBlacklistHandlers(repo)
	body, _ := json.Marshal(map[string]interface{}{"reason": "spam"})
	req := requestWithVideoID(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/blacklist", body, videoID)
	req = withAdminContext(req)
	w := httptest.NewRecorder()

	handlers.AddToBlacklist(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRemoveFromBlacklist_Success(t *testing.T) {
	repo := &MockBlacklistRepository{}
	videoID := uuid.New()

	repo.On("RemoveFromBlacklist", mock.Anything, videoID).Return(nil)

	handlers := NewBlacklistHandlers(repo)
	req := requestWithVideoID(http.MethodDelete, "/api/v1/videos/"+videoID.String()+"/blacklist", nil, videoID)
	req = withAdminContext(req)
	w := httptest.NewRecorder()

	handlers.RemoveFromBlacklist(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	repo.AssertExpectations(t)
}

func TestListBlacklist_Success(t *testing.T) {
	repo := &MockBlacklistRepository{}
	videoID := uuid.New()
	entries := []*domain.VideoBlacklist{
		{ID: uuid.New(), VideoID: videoID, Reason: "spam", CreatedAt: time.Now()},
	}

	repo.On("List", mock.Anything, 20, 0).Return(entries, 1, nil)

	handlers := NewBlacklistHandlers(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/blacklist", nil)
	req = withAdminContext(req)
	w := httptest.NewRecorder()

	handlers.ListBlacklist(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}

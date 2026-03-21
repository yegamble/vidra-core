package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"athena/internal/domain"
	"athena/internal/usecase"
)

// MockVideoRepoForFeed satisfies usecase.VideoRepository for feed tests.
type MockVideoRepoForFeed struct {
	mock.Mock
}

func (m *MockVideoRepoForFeed) Create(ctx context.Context, v *domain.Video) error {
	return m.Called(ctx, v).Error(0)
}
func (m *MockVideoRepoForFeed) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *MockVideoRepoForFeed) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *MockVideoRepoForFeed) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepoForFeed) GetByChannelID(ctx context.Context, channelID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepoForFeed) Update(ctx context.Context, v *domain.Video) error {
	return m.Called(ctx, v).Error(0)
}
func (m *MockVideoRepoForFeed) Delete(ctx context.Context, id string, userID string) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *MockVideoRepoForFeed) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepoForFeed) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepoForFeed) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return nil
}
func (m *MockVideoRepoForFeed) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return nil
}
func (m *MockVideoRepoForFeed) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockVideoRepoForFeed) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepoForFeed) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepoForFeed) CreateRemoteVideo(ctx context.Context, v *domain.Video) error {
	return nil
}
func (m *MockVideoRepoForFeed) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

var _ usecase.VideoRepository = (*MockVideoRepoForFeed)(nil)

// MockCommentRepoForFeed satisfies usecase.CommentRepository for feed tests.
type MockCommentRepoForFeed struct {
	mock.Mock
}

func (m *MockCommentRepoForFeed) Create(ctx context.Context, c *domain.Comment) error {
	return m.Called(ctx, c).Error(0)
}
func (m *MockCommentRepoForFeed) GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Comment), args.Error(1)
}
func (m *MockCommentRepoForFeed) GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CommentWithUser), args.Error(1)
}
func (m *MockCommentRepoForFeed) Update(ctx context.Context, id uuid.UUID, body string) error {
	return m.Called(ctx, id, body).Error(0)
}
func (m *MockCommentRepoForFeed) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *MockCommentRepoForFeed) ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}
func (m *MockCommentRepoForFeed) ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}
func (m *MockCommentRepoForFeed) ListRepliesBatch(ctx context.Context, parentIDs []uuid.UUID, limit int) (map[uuid.UUID][]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentIDs, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[uuid.UUID][]*domain.CommentWithUser), args.Error(1)
}
func (m *MockCommentRepoForFeed) CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error) {
	return m.Called(ctx, videoID, activeOnly).Int(0), m.Called(ctx, videoID, activeOnly).Error(1)
}
func (m *MockCommentRepoForFeed) FlagComment(ctx context.Context, flag *domain.CommentFlag) error {
	return m.Called(ctx, flag).Error(0)
}
func (m *MockCommentRepoForFeed) UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return m.Called(ctx, commentID, userID).Error(0)
}
func (m *MockCommentRepoForFeed) GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error) {
	args := m.Called(ctx, commentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentFlag), args.Error(1)
}
func (m *MockCommentRepoForFeed) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error {
	return m.Called(ctx, id, status).Error(0)
}
func (m *MockCommentRepoForFeed) IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, commentID, userID)
	return args.Bool(0), args.Error(1)
}

var _ usecase.CommentRepository = (*MockCommentRepoForFeed)(nil)

func TestVideosFeedAtom_ReturnsValidAtomXML(t *testing.T) {
	videoRepo := &MockVideoRepoForFeed{}
	commentRepo := &MockCommentRepoForFeed{}

	videos := []*domain.Video{
		{ID: "v1", Title: "Test Video", Description: "A test video"},
	}
	videoRepo.On("List", mock.Anything, mock.AnythingOfType("*domain.VideoSearchRequest")).
		Return(videos, int64(1), nil)

	handlers := NewFeedHandlers(videoRepo, commentRepo, "https://example.com")
	req := httptest.NewRequest(http.MethodGet, "/feeds/videos.atom", nil)
	w := httptest.NewRecorder()

	handlers.VideosFeed(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/atom+xml; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "response should be an Atom feed")
	assert.True(t, strings.Contains(body, "Test Video"), "feed should contain video title")
	videoRepo.AssertExpectations(t)
}

func TestVideosFeedRSS_ReturnsValidRSSXML(t *testing.T) {
	videoRepo := &MockVideoRepoForFeed{}
	commentRepo := &MockCommentRepoForFeed{}

	videos := []*domain.Video{
		{ID: "v1", Title: "RSS Video", Description: "An RSS video"},
	}
	videoRepo.On("List", mock.Anything, mock.AnythingOfType("*domain.VideoSearchRequest")).
		Return(videos, int64(1), nil)

	handlers := NewFeedHandlers(videoRepo, commentRepo, "https://example.com")
	req := httptest.NewRequest(http.MethodGet, "/feeds/videos.rss", nil)
	w := httptest.NewRecorder()

	handlers.VideosFeedRSS(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/rss+xml; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<rss"), "response should be an RSS feed")
	assert.True(t, strings.Contains(body, "RSS Video"), "feed should contain video title")
	videoRepo.AssertExpectations(t)
}

func TestCommentsFeedAtom_WithVideoID_ReturnsComments(t *testing.T) {
	videoRepo := &MockVideoRepoForFeed{}
	commentRepo := &MockCommentRepoForFeed{}

	videoID := uuid.New()
	comments := []*domain.CommentWithUser{
		{Comment: domain.Comment{ID: uuid.New(), VideoID: videoID, Body: "Great video!"}, Username: "alice"},
	}
	commentRepo.On("ListByVideo", mock.Anything, mock.MatchedBy(func(opts domain.CommentListOptions) bool {
		return opts.VideoID == videoID
	})).Return(comments, nil)

	handlers := NewFeedHandlers(videoRepo, commentRepo, "https://example.com")
	req := httptest.NewRequest(http.MethodGet, "/feeds/video-comments.atom?videoId="+videoID.String(), nil)
	w := httptest.NewRecorder()

	handlers.CommentsFeed(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/atom+xml; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "response should be an Atom feed")
	assert.True(t, strings.Contains(body, "Great video!"), "feed should contain comment body")
	commentRepo.AssertExpectations(t)
}

func TestCommentsFeedAtom_NoVideoID_ReturnsEmptyFeed(t *testing.T) {
	videoRepo := &MockVideoRepoForFeed{}
	commentRepo := &MockCommentRepoForFeed{}

	handlers := NewFeedHandlers(videoRepo, commentRepo, "https://example.com")
	req := httptest.NewRequest(http.MethodGet, "/feeds/video-comments.atom", nil)
	w := httptest.NewRecorder()

	handlers.CommentsFeed(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/atom+xml; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "response should be a valid Atom feed")
}

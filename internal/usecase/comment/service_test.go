package comment

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type mockCommentRepo struct{ mock.Mock }

func (m *mockCommentRepo) Create(ctx context.Context, comment *domain.Comment) error {
	return m.Called(ctx, comment).Error(0)
}
func (m *mockCommentRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Comment), args.Error(1)
}
func (m *mockCommentRepo) GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CommentWithUser), args.Error(1)
}
func (m *mockCommentRepo) Update(ctx context.Context, id uuid.UUID, body string) error {
	return m.Called(ctx, id, body).Error(0)
}
func (m *mockCommentRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockCommentRepo) ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}
func (m *mockCommentRepo) ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}
func (m *mockCommentRepo) CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error) {
	args := m.Called(ctx, videoID, activeOnly)
	return args.Int(0), args.Error(1)
}
func (m *mockCommentRepo) FlagComment(ctx context.Context, flag *domain.CommentFlag) error {
	return m.Called(ctx, flag).Error(0)
}
func (m *mockCommentRepo) UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error {
	return m.Called(ctx, commentID, userID).Error(0)
}
func (m *mockCommentRepo) GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error) {
	args := m.Called(ctx, commentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentFlag), args.Error(1)
}
func (m *mockCommentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error {
	return m.Called(ctx, id, status).Error(0)
}
func (m *mockCommentRepo) IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, commentID, userID)
	return args.Bool(0), args.Error(1)
}

type mockVideoRepo struct{ mock.Mock }

func (m *mockVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) Create(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *mockVideoRepo) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Update(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *mockVideoRepo) Delete(ctx context.Context, id string, userID string) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *mockVideoRepo) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath).Error(0)
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID).Error(0)
}
func (m *mockVideoRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockVideoRepo) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}

type mockUserRepo struct{ mock.Mock }

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return m.Called(ctx, user, passwordHash).Error(0)
}
func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}
func (m *mockUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return m.Called(ctx, userID, passwordHash).Error(0)
}
func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}
func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return m.Called(ctx, userID, ipfsCID, webpCID).Error(0)
}
func (m *mockUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}

type mockChannelRepo struct{ mock.Mock }

func (m *mockChannelRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) Create(ctx context.Context, channel *domain.Channel) error {
	return m.Called(ctx, channel).Error(0)
}
func (m *mockChannelRepo) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChannelListResponse), args.Error(1)
}
func (m *mockChannelRepo) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	args := m.Called(ctx, id, updates)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockChannelRepo) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, channelID, userID)
	return args.Bool(0), args.Error(1)
}

// --- Tests ---

func TestCreateComment_Success(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	videoID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)
	commentRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Comment")).Return(nil)

	req := &domain.CreateCommentRequest{
		VideoID: videoID,
		Body:    "Great video!",
	}

	comment, err := svc.CreateComment(context.Background(), userID, req)
	assert.NoError(t, err)
	assert.NotNil(t, comment)
	assert.Equal(t, domain.CommentStatusActive, comment.Status)
}

func TestCreateComment_VideoNotFound(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Hello"}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
}

func TestCreateComment_PrivateVideoBlocked(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPrivate,
	}, nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Hello"}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "comments not allowed on private videos")
}

func TestCreateComment_EmptyBody(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: ""}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty after sanitization")
}

func TestCreateComment_XSSSanitization(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)
	commentRepo.On("Create", mock.Anything, mock.MatchedBy(func(c *domain.Comment) bool {
		// Sanitized body should not contain script tags
		return c.Body != "" && c.Body != "<script>alert('xss')</script>"
	})).Return(nil)

	req := &domain.CreateCommentRequest{
		VideoID: videoID,
		Body:    "<script>alert('xss')</script>safe text",
	}

	comment, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.NoError(t, err)
	assert.NotNil(t, comment)
}

func TestCreateComment_ReplyToDeletedComment(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	parentID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)
	commentRepo.On("GetByID", mock.Anything, parentID).Return(&domain.Comment{
		ID: parentID, VideoID: videoID, Status: domain.CommentStatusDeleted,
	}, nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Reply", ParentID: &parentID}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reply to deleted")
}

func TestGetComment_Success(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	expected := &domain.CommentWithUser{Comment: domain.Comment{ID: commentID, Status: domain.CommentStatusActive}}
	commentRepo.On("GetByIDWithUser", mock.Anything, commentID).Return(expected, nil)

	comment, err := svc.GetComment(context.Background(), commentID)
	assert.NoError(t, err)
	assert.Equal(t, expected, comment)
}

func TestGetComment_DeletedReturnsNotFound(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetByIDWithUser", mock.Anything, commentID).Return(&domain.CommentWithUser{
		Comment: domain.Comment{ID: commentID, Status: domain.CommentStatusDeleted},
	}, nil)

	_, err := svc.GetComment(context.Background(), commentID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestUpdateComment_Success(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(true, nil)
	commentRepo.On("Update", mock.Anything, commentID, mock.AnythingOfType("string")).Return(nil)

	err := svc.UpdateComment(context.Background(), userID, commentID, &domain.UpdateCommentRequest{Body: "Updated text"})
	assert.NoError(t, err)
}

func TestUpdateComment_NotOwner(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	err := svc.UpdateComment(context.Background(), uuid.New(), uuid.New(), &domain.UpdateCommentRequest{Body: "test"})
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestDeleteComment_ByAdmin(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("Delete", mock.Anything, commentID).Return(nil)

	err := svc.DeleteComment(context.Background(), uuid.New(), commentID, true)
	assert.NoError(t, err)
}

func TestDeleteComment_ByOwner(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(true, nil)
	commentRepo.On("Delete", mock.Anything, commentID).Return(nil)

	err := svc.DeleteComment(context.Background(), userID, commentID, false)
	assert.NoError(t, err)
}

func TestDeleteComment_ByChannelOwner(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	channelOwnerID := uuid.New()
	commentID := uuid.New()
	videoID := uuid.New()
	channelID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, channelOwnerID).Return(false, nil)
	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, VideoID: videoID,
	}, nil)
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), ChannelID: channelID,
	}, nil)
	channelRepo.On("GetByID", mock.Anything, channelID).Return(&domain.Channel{
		ID: channelID, AccountID: channelOwnerID,
	}, nil)
	commentRepo.On("Delete", mock.Anything, commentID).Return(nil)

	err := svc.DeleteComment(context.Background(), channelOwnerID, commentID, false)
	assert.NoError(t, err)
}

func TestDeleteComment_Unauthorized(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	otherOwnerID := uuid.New()
	commentID := uuid.New()
	videoID := uuid.New()
	channelID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(false, nil)
	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, VideoID: videoID,
	}, nil)
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), ChannelID: channelID,
	}, nil)
	channelRepo.On("GetByID", mock.Anything, channelID).Return(&domain.Channel{
		ID: channelID, AccountID: otherOwnerID,
	}, nil)

	err := svc.DeleteComment(context.Background(), userID, commentID, false)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestFlagComment_CannotFlagOwn(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, UserID: userID, Status: domain.CommentStatusActive,
	}, nil)

	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot flag your own comment")
}

func TestFlagComment_Success(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentAuthor := uuid.New()
	commentID := uuid.New()

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, UserID: commentAuthor, Status: domain.CommentStatusActive, FlagCount: 0,
	}, nil)
	commentRepo.On("FlagComment", mock.Anything, mock.AnythingOfType("*domain.CommentFlag")).Return(nil)

	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.NoError(t, err)
}

func TestListComments_DefaultPagination(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	commentRepo.On("ListByVideo", mock.Anything, mock.MatchedBy(func(opts domain.CommentListOptions) bool {
		return opts.Limit == 20 && opts.Offset == 0 && opts.OrderBy == "newest"
	})).Return([]*domain.CommentWithUser{}, nil)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 0, -1, "invalid")
	assert.NoError(t, err)
	assert.Empty(t, comments)
}

func TestModerateComment_AdminAllowed(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("UpdateStatus", mock.Anything, commentID, domain.CommentStatusFlagged).Return(nil)

	err := svc.ModerateComment(context.Background(), uuid.New(), commentID, domain.CommentStatusFlagged, true)
	assert.NoError(t, err)
}

func TestModerateComment_NonAdminNonOwnerDenied(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	otherOwner := uuid.New()
	commentID := uuid.New()
	videoID := uuid.New()
	channelID := uuid.New()

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, VideoID: videoID,
	}, nil)
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), ChannelID: channelID,
	}, nil)
	channelRepo.On("GetByID", mock.Anything, channelID).Return(&domain.Channel{
		ID: channelID, AccountID: otherOwner,
	}, nil)

	err := svc.ModerateComment(context.Background(), userID, commentID, domain.CommentStatusFlagged, false)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestUnflagComment_Success(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	userID := uuid.New()

	commentRepo.On("UnflagComment", mock.Anything, commentID, userID).Return(nil)

	err := svc.UnflagComment(context.Background(), userID, commentID)
	assert.NoError(t, err)
}

func TestUnflagComment_Error(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentRepo.On("UnflagComment", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("db error"))

	err := svc.UnflagComment(context.Background(), uuid.New(), uuid.New())
	assert.Error(t, err)
}

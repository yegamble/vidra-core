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
func (m *mockCommentRepo) ListRepliesBatch(ctx context.Context, parentIDs []uuid.UUID, limit int) (map[uuid.UUID][]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentIDs, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[uuid.UUID][]*domain.CommentWithUser), args.Error(1)
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

func TestGetCommentFlags_AdminSuccess(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	expectedFlags := []*domain.CommentFlag{
		{ID: uuid.New(), CommentID: commentID, UserID: uuid.New(), Reason: domain.FlagReasonSpam},
		{ID: uuid.New(), CommentID: commentID, UserID: uuid.New(), Reason: domain.FlagReasonHarassment},
	}

	commentRepo.On("GetFlags", mock.Anything, commentID).Return(expectedFlags, nil)

	flags, err := svc.GetCommentFlags(context.Background(), commentID, uuid.New(), true)
	assert.NoError(t, err)
	assert.Len(t, flags, 2)
	assert.Equal(t, expectedFlags, flags)
}

func TestGetCommentFlags_ChannelOwnerSuccess(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	channelOwnerID := uuid.New()
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
		ID: channelID, AccountID: channelOwnerID,
	}, nil)

	expectedFlags := []*domain.CommentFlag{
		{ID: uuid.New(), CommentID: commentID, Reason: domain.FlagReasonSpam},
	}
	commentRepo.On("GetFlags", mock.Anything, commentID).Return(expectedFlags, nil)

	flags, err := svc.GetCommentFlags(context.Background(), commentID, channelOwnerID, false)
	assert.NoError(t, err)
	assert.Len(t, flags, 1)
}

func TestGetCommentFlags_NonOwnerUnauthorized(t *testing.T) {
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

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, VideoID: videoID,
	}, nil)
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), ChannelID: channelID,
	}, nil)
	channelRepo.On("GetByID", mock.Anything, channelID).Return(&domain.Channel{
		ID: channelID, AccountID: otherOwnerID,
	}, nil)

	flags, err := svc.GetCommentFlags(context.Background(), commentID, userID, false)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
	assert.Nil(t, flags)
}

func TestGetCommentFlags_CommentNotFound(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetByID", mock.Anything, commentID).Return(nil, domain.ErrNotFound)

	flags, err := svc.GetCommentFlags(context.Background(), commentID, uuid.New(), false)
	assert.Error(t, err)
	assert.Nil(t, flags)
}

func TestGetCommentFlags_GetFlagsRepoError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetFlags", mock.Anything, commentID).Return(nil, errors.New("db error"))

	flags, err := svc.GetCommentFlags(context.Background(), commentID, uuid.New(), true)
	assert.Error(t, err)
	assert.Nil(t, flags)
	assert.Contains(t, err.Error(), "failed to get comment flags")
}

func TestGetCommentFlags_EmptyFlags(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetFlags", mock.Anything, commentID).Return([]*domain.CommentFlag{}, nil)

	flags, err := svc.GetCommentFlags(context.Background(), commentID, uuid.New(), true)
	assert.NoError(t, err)
	assert.Empty(t, flags)
}

func TestFlagComment_CommentNotFound(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetByID", mock.Anything, commentID).Return(nil, domain.ErrNotFound)

	err := svc.FlagComment(context.Background(), uuid.New(), commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.Error(t, err)
}

func TestFlagComment_DeletedComment(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentAuthor := uuid.New()
	commentID := uuid.New()

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, UserID: commentAuthor, Status: domain.CommentStatusDeleted,
	}, nil)

	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot flag deleted or hidden comment")
}

func TestFlagComment_WithDetails(t *testing.T) {
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

	details := "This comment is spamming product links"
	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{
		Reason:  "spam",
		Details: &details,
	})
	assert.NoError(t, err)
}

func TestFlagComment_AutoHideHighFlagCount(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentAuthor := uuid.New()
	commentID := uuid.New()

	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, UserID: commentAuthor, Status: domain.CommentStatusActive, FlagCount: 5,
	}, nil)
	commentRepo.On("FlagComment", mock.Anything, mock.AnythingOfType("*domain.CommentFlag")).Return(nil)
	commentRepo.On("UpdateStatus", mock.Anything, commentID, domain.CommentStatusFlagged).Return(nil)

	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.NoError(t, err)
	commentRepo.AssertCalled(t, "UpdateStatus", mock.Anything, commentID, domain.CommentStatusFlagged)
}

func TestFlagComment_RepoError(t *testing.T) {
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
	commentRepo.On("FlagComment", mock.Anything, mock.AnythingOfType("*domain.CommentFlag")).Return(errors.New("db error"))

	err := svc.FlagComment(context.Background(), userID, commentID, &domain.FlagCommentRequest{Reason: "spam"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to flag comment")
}

func TestDeleteComment_IsOwnerCheckError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("IsOwner", mock.Anything, commentID, mock.Anything).Return(false, errors.New("db error"))

	err := svc.DeleteComment(context.Background(), uuid.New(), commentID, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestDeleteComment_GetCommentError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(false, nil)
	commentRepo.On("GetByID", mock.Anything, commentID).Return(nil, errors.New("db error"))

	err := svc.DeleteComment(context.Background(), userID, commentID, false)
	assert.Error(t, err)
}

func TestDeleteComment_GetVideoError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()
	videoID := uuid.New()

	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(false, nil)
	commentRepo.On("GetByID", mock.Anything, commentID).Return(&domain.Comment{
		ID: commentID, VideoID: videoID,
	}, nil)
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, errors.New("db error"))

	err := svc.DeleteComment(context.Background(), userID, commentID, false)
	assert.Error(t, err)
}

func TestDeleteComment_GetChannelError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
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
	channelRepo.On("GetByID", mock.Anything, channelID).Return(nil, errors.New("db error"))

	err := svc.DeleteComment(context.Background(), userID, commentID, false)
	assert.Error(t, err)
}

func TestDeleteComment_RepoDeleteError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("Delete", mock.Anything, commentID).Return(errors.New("db error"))

	err := svc.DeleteComment(context.Background(), uuid.New(), commentID, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete comment")
}

func TestUpdateComment_IsOwnerCheckError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("IsOwner", mock.Anything, commentID, mock.Anything).Return(false, errors.New("db error"))

	err := svc.UpdateComment(context.Background(), uuid.New(), commentID, &domain.UpdateCommentRequest{Body: "test"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestUpdateComment_EmptyBody(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()
	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(true, nil)

	err := svc.UpdateComment(context.Background(), userID, commentID, &domain.UpdateCommentRequest{Body: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty after sanitization")
}

func TestUpdateComment_XSSSanitization(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()
	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(true, nil)
	commentRepo.On("Update", mock.Anything, commentID, mock.AnythingOfType("string")).Return(nil)

	err := svc.UpdateComment(context.Background(), userID, commentID, &domain.UpdateCommentRequest{
		Body: "<script>alert('xss')</script>safe update",
	})
	assert.NoError(t, err)
}

func TestUpdateComment_RepoError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	userID := uuid.New()
	commentID := uuid.New()
	commentRepo.On("IsOwner", mock.Anything, commentID, userID).Return(true, nil)
	commentRepo.On("Update", mock.Anything, commentID, mock.AnythingOfType("string")).Return(errors.New("db error"))

	err := svc.UpdateComment(context.Background(), userID, commentID, &domain.UpdateCommentRequest{Body: "valid text"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update comment")
}

func TestListComments_VideoNotFound(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 20, 0, "newest")
	assert.Error(t, err)
	assert.Nil(t, comments)
}

func TestListComments_CustomPagination(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	commentRepo.On("ListByVideo", mock.Anything, mock.MatchedBy(func(opts domain.CommentListOptions) bool {
		return opts.Limit == 50 && opts.Offset == 10 && opts.OrderBy == "oldest"
	})).Return([]*domain.CommentWithUser{}, nil)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 50, 10, "oldest")
	assert.NoError(t, err)
	assert.Empty(t, comments)
}

func TestListComments_LimitCapped(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	commentRepo.On("ListByVideo", mock.Anything, mock.MatchedBy(func(opts domain.CommentListOptions) bool {
		return opts.Limit == 20
	})).Return([]*domain.CommentWithUser{}, nil)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 200, 0, "newest")
	assert.NoError(t, err)
	assert.Empty(t, comments)
}

func TestListComments_WithReplies(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	commentID := uuid.New()
	replyUserID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)

	topLevelComment := &domain.CommentWithUser{
		Comment: domain.Comment{
			ID:      commentID,
			VideoID: videoID,
			UserID:  uuid.New(),
			Body:    "Top level comment",
			Status:  domain.CommentStatusActive,
		},
		Username: "testuser",
	}

	commentRepo.On("ListByVideo", mock.Anything, mock.Anything).Return([]*domain.CommentWithUser{topLevelComment}, nil)

	replyComment := &domain.CommentWithUser{
		Comment: domain.Comment{
			ID:       uuid.New(),
			VideoID:  videoID,
			UserID:   replyUserID,
			ParentID: &commentID,
			Body:     "This is a reply",
			Status:   domain.CommentStatusActive,
		},
		Username: "replyuser",
	}

	batchResult := map[uuid.UUID][]*domain.CommentWithUser{
		commentID: {replyComment},
	}
	commentRepo.On("ListRepliesBatch", mock.Anything, []uuid.UUID{commentID}, 3).Return(batchResult, nil)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 20, 0, "newest")
	assert.NoError(t, err)
	assert.Len(t, comments, 1)
	assert.Len(t, comments[0].Replies, 1)
	assert.Equal(t, "This is a reply", comments[0].Replies[0].Body)
}

func TestListComments_WithParentID(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	parentID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	commentRepo.On("ListByVideo", mock.Anything, mock.MatchedBy(func(opts domain.CommentListOptions) bool {
		return opts.ParentID != nil && *opts.ParentID == parentID
	})).Return([]*domain.CommentWithUser{}, nil)

	comments, err := svc.ListComments(context.Background(), videoID, &parentID, 20, 0, "newest")
	assert.NoError(t, err)
	assert.Empty(t, comments)
	commentRepo.AssertNotCalled(t, "ListReplies", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestListComments_RepoError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	commentRepo.On("ListByVideo", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	comments, err := svc.ListComments(context.Background(), videoID, nil, 20, 0, "newest")
	assert.Error(t, err)
	assert.Nil(t, comments)
	assert.Contains(t, err.Error(), "failed to list comments")
}

func TestGetComment_RepoError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("GetByIDWithUser", mock.Anything, commentID).Return(nil, errors.New("db error"))

	comment, err := svc.GetComment(context.Background(), commentID)
	assert.Error(t, err)
	assert.Nil(t, comment)
}

func TestModerateComment_ChannelOwnerAllowed(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	channelOwnerID := uuid.New()
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
		ID: channelID, AccountID: channelOwnerID,
	}, nil)
	commentRepo.On("UpdateStatus", mock.Anything, commentID, domain.CommentStatusHidden).Return(nil)

	err := svc.ModerateComment(context.Background(), channelOwnerID, commentID, domain.CommentStatusHidden, false)
	assert.NoError(t, err)
}

func TestModerateComment_UpdateStatusError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	commentID := uuid.New()
	commentRepo.On("UpdateStatus", mock.Anything, commentID, domain.CommentStatusFlagged).Return(errors.New("db error"))

	err := svc.ModerateComment(context.Background(), uuid.New(), commentID, domain.CommentStatusFlagged, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update comment status")
}

func TestCreateComment_ReplyToNonExistentParent(t *testing.T) {
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
	commentRepo.On("GetByID", mock.Anything, parentID).Return(nil, domain.ErrNotFound)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Reply", ParentID: &parentID}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parent comment not found")
}

func TestCreateComment_ReplyToDifferentVideo(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	otherVideoID := uuid.New()
	parentID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)
	commentRepo.On("GetByID", mock.Anything, parentID).Return(&domain.Comment{
		ID: parentID, VideoID: otherVideoID, Status: domain.CommentStatusActive,
	}, nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Reply", ParentID: &parentID}
	_, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parent comment is from a different video")
}

func TestCreateComment_UnlistedVideoAllowed(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyUnlisted,
	}, nil)
	commentRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Comment")).Return(nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Comment on unlisted video"}
	comment, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.NoError(t, err)
	assert.NotNil(t, comment)
}

func TestCreateComment_RepoCreateError(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{
		ID: videoID.String(), Privacy: domain.PrivacyPublic,
	}, nil)
	commentRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Comment")).Return(errors.New("db error"))

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "Test comment"}
	comment, err := svc.CreateComment(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Nil(t, comment)
	assert.Contains(t, err.Error(), "failed to create comment")
}

func TestListComments_BatchFetchReplies(t *testing.T) {
	commentRepo := new(mockCommentRepo)
	videoRepo := new(mockVideoRepo)
	userRepo := new(mockUserRepo)
	channelRepo := new(mockChannelRepo)
	svc := NewService(commentRepo, videoRepo, userRepo, channelRepo)

	videoID := uuid.New()
	id1 := uuid.New()
	id2 := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)

	comment1 := &domain.CommentWithUser{Comment: domain.Comment{ID: id1, VideoID: videoID, Body: "first", Status: domain.CommentStatusActive}, Username: "u1"}
	comment2 := &domain.CommentWithUser{Comment: domain.Comment{ID: id2, VideoID: videoID, Body: "second", Status: domain.CommentStatusActive}, Username: "u2"}
	commentRepo.On("ListByVideo", mock.Anything, mock.Anything).Return([]*domain.CommentWithUser{comment1, comment2}, nil)

	batchResult := map[uuid.UUID][]*domain.CommentWithUser{
		id1: {},
		id2: {},
	}
	commentRepo.On("ListRepliesBatch", mock.Anything, mock.MatchedBy(func(ids []uuid.UUID) bool {
		return len(ids) == 2
	}), 3).Return(batchResult, nil)

	comments, err := svc.ListComments(context.Background(), videoID, nil, 20, 0, "newest")
	assert.NoError(t, err)
	assert.Len(t, comments, 2)

	commentRepo.AssertNotCalled(t, "ListReplies", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	commentRepo.AssertCalled(t, "ListRepliesBatch", mock.Anything, mock.Anything, 3)
}

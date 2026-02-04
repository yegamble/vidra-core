package comment

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mocks

type MockCommentRepository struct {
	mock.Mock
}

func (m *MockCommentRepository) Create(ctx context.Context, comment *domain.Comment) error {
	args := m.Called(ctx, comment)
	return args.Error(0)
}

func (m *MockCommentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Comment), args.Error(1)
}

func (m *MockCommentRepository) GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) Update(ctx context.Context, id uuid.UUID, body string) error {
	args := m.Called(ctx, id, body)
	return args.Error(0)
}

func (m *MockCommentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockCommentRepository) ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error) {
	args := m.Called(ctx, videoID, activeOnly)
	return args.Int(0), args.Error(1)
}

func (m *MockCommentRepository) FlagComment(ctx context.Context, flag *domain.CommentFlag) error {
	args := m.Called(ctx, flag)
	return args.Error(0)
}

func (m *MockCommentRepository) UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error {
	args := m.Called(ctx, commentID, userID)
	return args.Error(0)
}

func (m *MockCommentRepository) GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error) {
	args := m.Called(ctx, commentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentFlag), args.Error(1)
}

func (m *MockCommentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockCommentRepository) IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, commentID, userID)
	return args.Bool(0), args.Error(1)
}

type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error { return nil }
func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error                         { return nil }
func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error                    { return nil }
func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return nil
}
func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return nil
}
func (m *MockVideoRepository) Count(ctx context.Context) (int64, error)                               { return 0, nil }
func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) { return nil, nil }
func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error)   { return nil, nil }
func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error              { return nil }

type MockChannelRepository struct {
	mock.Mock
}

func (m *MockChannelRepository) Create(ctx context.Context, channel *domain.Channel) error { return nil }
func (m *MockChannelRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *MockChannelRepository) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockChannelRepository) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	return nil, nil
}
func (m *MockChannelRepository) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockChannelRepository) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *MockChannelRepository) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	return nil, nil
}
func (m *MockChannelRepository) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockChannelRepository) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	return false, nil
}

type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return nil
}
func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepository) Update(ctx context.Context, user *domain.User) error { return nil }
func (m *MockUserRepository) Delete(ctx context.Context, id string) error         { return nil }
func (m *MockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	return "", nil
}
func (m *MockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return nil
}
func (m *MockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepository) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return nil
}
func (m *MockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error { return nil }

// Tests

func TestCreateComment_Success(t *testing.T) {
	mockCommentRepo := new(MockCommentRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockUserRepo := new(MockUserRepository)
	mockChannelRepo := new(MockChannelRepository)

	service := NewService(mockCommentRepo, mockVideoRepo, mockUserRepo, mockChannelRepo)

	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()
	videoIDStr := videoID.String()

	req := &domain.CreateCommentRequest{
		VideoID: videoID,
		Body:    "Great video!",
	}

	mockVideoRepo.On("GetByID", ctx, videoIDStr).Return(&domain.Video{
		ID:      videoIDStr,
		Privacy: domain.PrivacyPublic,
	}, nil)

	mockCommentRepo.On("Create", ctx, mock.MatchedBy(func(c *domain.Comment) bool {
		return c.VideoID == videoID && c.UserID == userID && c.Body == "Great video!" && c.Status == domain.CommentStatusActive
	})).Return(nil)

	comment, err := service.CreateComment(ctx, userID, req)
	require.NoError(t, err)
	assert.NotNil(t, comment)
	assert.Equal(t, "Great video!", comment.Body)
	assert.Equal(t, videoID, comment.VideoID)
	assert.Equal(t, userID, comment.UserID)

	mockVideoRepo.AssertExpectations(t)
	mockCommentRepo.AssertExpectations(t)
}

func TestCreateComment_Validation(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()

	tests := []struct {
		name      string
		body      string
		expectErr string
	}{
		{
			name:      "Empty body",
			body:      "",
			expectErr: "comment body is empty",
		},
		{
			name:      "Too long body",
			body:      strings.Repeat("a", 10001),
			expectErr: "comment body exceeds maximum length",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockVideoRepo := new(MockVideoRepository)
			mockCommentRepo := new(MockCommentRepository) // Need to pass fresh mocks or reset
			s := NewService(mockCommentRepo, mockVideoRepo, new(MockUserRepository), new(MockChannelRepository))

			mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(&domain.Video{
				ID:      videoID.String(),
				Privacy: domain.PrivacyPublic,
			}, nil)

			req := &domain.CreateCommentRequest{
				VideoID: videoID,
				Body:    tc.body,
			}

			_, err := s.CreateComment(ctx, userID, req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectErr)
		})
	}
}

func TestCreateComment_VideoNotFound(t *testing.T) {
	mockVideoRepo := new(MockVideoRepository)
	service := NewService(new(MockCommentRepository), mockVideoRepo, new(MockUserRepository), new(MockChannelRepository))

	ctx := context.Background()
	videoID := uuid.New()

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, errors.New("not found"))

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "test"}
	_, err := service.CreateComment(ctx, uuid.New(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video not found")
}

func TestCreateComment_PrivateVideo(t *testing.T) {
	mockVideoRepo := new(MockVideoRepository)
	service := NewService(new(MockCommentRepository), mockVideoRepo, new(MockUserRepository), new(MockChannelRepository))

	ctx := context.Background()
	videoID := uuid.New()

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(&domain.Video{
		ID:      videoID.String(),
		Privacy: domain.PrivacyPrivate,
	}, nil)

	req := &domain.CreateCommentRequest{VideoID: videoID, Body: "test"}
	_, err := service.CreateComment(ctx, uuid.New(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comments not allowed on private videos")
}

func TestCreateComment_ReplyValidation(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()
	parentID := uuid.New()
	diffVideoID := uuid.New()

	tests := []struct {
		name       string
		setupMocks func(*MockVideoRepository, *MockCommentRepository)
		expectErr  string
	}{
		{
			name: "Parent not found",
			setupMocks: func(v *MockVideoRepository, c *MockCommentRepository) {
				v.On("GetByID", ctx, videoID.String()).Return(&domain.Video{ID: videoID.String(), Privacy: domain.PrivacyPublic}, nil)
				c.On("GetByID", ctx, parentID).Return(nil, errors.New("not found"))
			},
			expectErr: "parent comment not found",
		},
		{
			name: "Parent different video",
			setupMocks: func(v *MockVideoRepository, c *MockCommentRepository) {
				v.On("GetByID", ctx, videoID.String()).Return(&domain.Video{ID: videoID.String(), Privacy: domain.PrivacyPublic}, nil)
				c.On("GetByID", ctx, parentID).Return(&domain.Comment{ID: parentID, VideoID: diffVideoID, Status: domain.CommentStatusActive}, nil)
			},
			expectErr: "parent comment is from a different video",
		},
		{
			name: "Parent deleted",
			setupMocks: func(v *MockVideoRepository, c *MockCommentRepository) {
				v.On("GetByID", ctx, videoID.String()).Return(&domain.Video{ID: videoID.String(), Privacy: domain.PrivacyPublic}, nil)
				c.On("GetByID", ctx, parentID).Return(&domain.Comment{ID: parentID, VideoID: videoID, Status: domain.CommentStatusDeleted}, nil)
			},
			expectErr: "cannot reply to deleted or hidden comment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockVideoRepo := new(MockVideoRepository)
			mockCommentRepo := new(MockCommentRepository)
			service := NewService(mockCommentRepo, mockVideoRepo, new(MockUserRepository), new(MockChannelRepository))

			tc.setupMocks(mockVideoRepo, mockCommentRepo)

			req := &domain.CreateCommentRequest{
				VideoID:  videoID,
				Body:     "reply",
				ParentID: &parentID,
			}

			_, err := service.CreateComment(ctx, userID, req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectErr)
		})
	}
}

func TestFlagComment(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	commentID := uuid.New()
	otherUserID := uuid.New()

	t.Run("Success", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:        commentID,
			UserID:    otherUserID,
			Status:    domain.CommentStatusActive,
			FlagCount: 0,
		}, nil)

		mockCommentRepo.On("FlagComment", ctx, mock.MatchedBy(func(f *domain.CommentFlag) bool {
			return f.CommentID == commentID && f.UserID == userID
		})).Return(nil)

		req := &domain.FlagCommentRequest{Reason: "spam"}
		err := service.FlagComment(ctx, userID, commentID, req)
		require.NoError(t, err)
	})

	t.Run("Self flag", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:     commentID,
			UserID: userID,
			Status: domain.CommentStatusActive,
		}, nil)

		req := &domain.FlagCommentRequest{Reason: "spam"}
		err := service.FlagComment(ctx, userID, commentID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot flag your own comment")
	})

	t.Run("Deleted comment", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:     commentID,
			UserID: otherUserID,
			Status: domain.CommentStatusDeleted,
		}, nil)

		req := &domain.FlagCommentRequest{Reason: "spam"}
		err := service.FlagComment(ctx, userID, commentID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot flag deleted")
	})

	t.Run("Auto Hide", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:        commentID,
			UserID:    otherUserID,
			Status:    domain.CommentStatusActive,
			FlagCount: 5, // Threshold reached
		}, nil)

		mockCommentRepo.On("FlagComment", ctx, mock.Anything).Return(nil)
		mockCommentRepo.On("UpdateStatus", ctx, commentID, domain.CommentStatusFlagged).Return(nil)

		req := &domain.FlagCommentRequest{Reason: "spam"}
		err := service.FlagComment(ctx, userID, commentID, req)
		require.NoError(t, err)
		mockCommentRepo.AssertCalled(t, "UpdateStatus", ctx, commentID, domain.CommentStatusFlagged)
	})
}

func TestDeleteComment(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	commentID := uuid.New()
	videoID := uuid.New()
	channelID := uuid.New()

	t.Run("Owner delete", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		mockCommentRepo.On("IsOwner", ctx, commentID, userID).Return(true, nil)
		mockCommentRepo.On("Delete", ctx, commentID).Return(nil)

		err := service.DeleteComment(ctx, userID, commentID, false)
		require.NoError(t, err)
	})

	t.Run("Admin delete", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		service := NewService(mockCommentRepo, new(MockVideoRepository), new(MockUserRepository), new(MockChannelRepository))

		// IsOwner check skipped for admin
		mockCommentRepo.On("Delete", ctx, commentID).Return(nil)

		err := service.DeleteComment(ctx, userID, commentID, true)
		require.NoError(t, err)
	})

	t.Run("Video owner delete", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		mockVideoRepo := new(MockVideoRepository)
		mockChannelRepo := new(MockChannelRepository)
		service := NewService(mockCommentRepo, mockVideoRepo, new(MockUserRepository), mockChannelRepo)

		mockCommentRepo.On("IsOwner", ctx, commentID, userID).Return(false, nil)
		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:      commentID,
			VideoID: videoID,
		}, nil)

		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(&domain.Video{
			ID:        videoID.String(),
			ChannelID: channelID,
		}, nil)

		mockChannelRepo.On("GetByID", ctx, channelID).Return(&domain.Channel{
			ID:        channelID,
			AccountID: userID, // User owns the channel/video
		}, nil)

		mockCommentRepo.On("Delete", ctx, commentID).Return(nil)

		err := service.DeleteComment(ctx, userID, commentID, false)
		require.NoError(t, err)
	})

	t.Run("Unauthorized delete", func(t *testing.T) {
		mockCommentRepo := new(MockCommentRepository)
		mockVideoRepo := new(MockVideoRepository)
		mockChannelRepo := new(MockChannelRepository)
		service := NewService(mockCommentRepo, mockVideoRepo, new(MockUserRepository), mockChannelRepo)

		otherUserID := uuid.New()

		mockCommentRepo.On("IsOwner", ctx, commentID, userID).Return(false, nil)
		mockCommentRepo.On("GetByID", ctx, commentID).Return(&domain.Comment{
			ID:      commentID,
			VideoID: videoID,
		}, nil)

		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(&domain.Video{
			ID:        videoID.String(),
			ChannelID: channelID,
		}, nil)

		mockChannelRepo.On("GetByID", ctx, channelID).Return(&domain.Channel{
			ID:        channelID,
			AccountID: otherUserID, // User does NOT own the channel
		}, nil)

		err := service.DeleteComment(ctx, userID, commentID, false)
		require.Error(t, err)
		assert.Equal(t, domain.ErrUnauthorized, err)
	})
}

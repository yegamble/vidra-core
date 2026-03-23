package messaging

import (
	"context"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type Response = shared.Response

type ErrorInfo = shared.ErrorInfo

type Meta = shared.Meta

type MockLiveStreamRepository struct {
	mock.Mock
}

func (m *MockLiveStreamRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) Create(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	args := m.Called(ctx, streamKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	args := m.Called(ctx, channelID)
	return args.Int(0), args.Error(1)
}

func (m *MockLiveStreamRepository) Update(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	args := m.Called(ctx, id, count)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) EndStream(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) GetChannelByStreamID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockLiveStreamRepository) UpdateWaitingRoom(_ context.Context, _ uuid.UUID, _ bool, _ string) error {
	return nil
}
func (m *MockLiveStreamRepository) ScheduleStream(_ context.Context, _ uuid.UUID, _ *time.Time, _ *time.Time, _ bool, _ string) error {
	return nil
}
func (m *MockLiveStreamRepository) CancelSchedule(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *MockLiveStreamRepository) GetScheduledStreams(_ context.Context, _, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *MockLiveStreamRepository) GetUpcomingStreams(_ context.Context, _ uuid.UUID, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}

//nolint:unused // used in test files
func createTestUser(t interface{}, userRepo interface{}, ctx context.Context, username, email string) *domain.User {
	user := &domain.User{
		ID:        uuid.NewString(),
		Username:  username,
		Email:     email,
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if repo, ok := userRepo.(interface {
		Create(context.Context, *domain.User, string) error
	}); ok {
		if err := repo.Create(ctx, user, "$2a$10$abcdefghijklmnopqrstuvwxabcdefghijklmnopqrstuvwxabcdefghij"); err != nil {
			if tb, ok := t.(interface {
				Fatalf(string, ...interface{})
			}); ok {
				tb.Fatalf("failed to create test user %s: %v", username, err)
			}
		}
	}

	return user
}

//nolint:unused // used in test files
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}

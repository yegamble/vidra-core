package chat

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
)

type MockChatRepository struct {
	mock.Mock
}

func (m *MockChatRepository) CreateMessage(ctx context.Context, msg *domain.ChatMessage) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func (m *MockChatRepository) GetMessages(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ChatMessage, error) {
	args := m.Called(ctx, streamID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ChatMessage), args.Error(1)
}

func (m *MockChatRepository) GetMessagesSince(ctx context.Context, streamID uuid.UUID, since time.Time) ([]*domain.ChatMessage, error) {
	args := m.Called(ctx, streamID, since)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ChatMessage), args.Error(1)
}

func (m *MockChatRepository) DeleteMessage(ctx context.Context, messageID uuid.UUID) error {
	args := m.Called(ctx, messageID)
	return args.Error(0)
}

func (m *MockChatRepository) GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.ChatMessage, error) {
	args := m.Called(ctx, messageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChatMessage), args.Error(1)
}

func (m *MockChatRepository) AddModerator(ctx context.Context, mod *domain.ChatModerator) error {
	args := m.Called(ctx, mod)
	return args.Error(0)
}

func (m *MockChatRepository) RemoveModerator(ctx context.Context, streamID, userID uuid.UUID) error {
	args := m.Called(ctx, streamID, userID)
	return args.Error(0)
}

func (m *MockChatRepository) IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, streamID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockChatRepository) GetModerators(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatModerator, error) {
	args := m.Called(ctx, streamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ChatModerator), args.Error(1)
}

func (m *MockChatRepository) BanUser(ctx context.Context, ban *domain.ChatBan) error {
	args := m.Called(ctx, ban)
	return args.Error(0)
}

func (m *MockChatRepository) UnbanUser(ctx context.Context, streamID, userID uuid.UUID) error {
	args := m.Called(ctx, streamID, userID)
	return args.Error(0)
}

func (m *MockChatRepository) IsUserBanned(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, streamID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockChatRepository) GetBans(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatBan, error) {
	args := m.Called(ctx, streamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ChatBan), args.Error(1)
}

func (m *MockChatRepository) GetBanByID(ctx context.Context, banID uuid.UUID) (*domain.ChatBan, error) {
	args := m.Called(ctx, banID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChatBan), args.Error(1)
}

func (m *MockChatRepository) CleanupExpiredBans(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
}

func (m *MockChatRepository) GetStreamStats(ctx context.Context, streamID uuid.UUID) (*domain.ChatStreamStats, error) {
	args := m.Called(ctx, streamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChatStreamStats), args.Error(1)
}

func (m *MockChatRepository) GetMessageCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	args := m.Called(ctx, streamID)
	return args.Int(0), args.Error(1)
}

type MockStreamRepository struct {
	mock.Mock
}

func (m *MockStreamRepository) Create(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockStreamRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	args := m.Called(ctx, streamKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	args := m.Called(ctx, channelID)
	return args.Int(0), args.Error(1)
}

func (m *MockStreamRepository) Update(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockStreamRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockStreamRepository) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	args := m.Called(ctx, id, count)
	return args.Error(0)
}

func (m *MockStreamRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockStreamRepository) EndStream(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockStreamRepository) GetChannelByStreamID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockStreamRepository) UpdateWaitingRoom(_ context.Context, _ uuid.UUID, _ bool, _ string) error {
	return nil
}
func (m *MockStreamRepository) ScheduleStream(_ context.Context, _ uuid.UUID, _ *time.Time, _ *time.Time, _ bool, _ string) error {
	return nil
}
func (m *MockStreamRepository) CancelSchedule(_ context.Context, _ uuid.UUID) error { return nil }
func (m *MockStreamRepository) GetScheduledStreams(_ context.Context, _, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *MockStreamRepository) GetUpcomingStreams(_ context.Context, _ uuid.UUID, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func setupChatServerTest(t *testing.T) (*ChatServer, *MockChatRepository, *MockStreamRepository) {
	mockChatRepo := new(MockChatRepository)
	mockStreamRepo := new(MockStreamRepository)

	cfg := &config.Config{}

	var redisClient *redis.Client

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	server := NewChatServer(cfg, mockChatRepo, mockStreamRepo, redisClient, logger)

	return server, mockChatRepo, mockStreamRepo
}

func TestNewChatServer(t *testing.T) {
	cfg := &config.Config{}
	mockChatRepo := new(MockChatRepository)
	mockStreamRepo := new(MockStreamRepository)
	redisClient := &redis.Client{}
	logger := logrus.New()

	server := NewChatServer(cfg, mockChatRepo, mockStreamRepo, redisClient, logger)

	assert.NotNil(t, server)
	assert.NotNil(t, server.Upgrader)
	assert.NotNil(t, server.connections)
}

func TestChatServer_RegisterUnregisterClient(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	streamID := uuid.New()
	client := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "testuser",
	}

	server.registerClient(client)

	server.mu.RLock()
	clients := server.connections[streamID]
	server.mu.RUnlock()

	assert.NotNil(t, clients)
	assert.True(t, clients[client])

	server.unregisterClient(client)

	server.mu.RLock()
	clients = server.connections[streamID]
	server.mu.RUnlock()

	assert.False(t, clients[client])
}

func TestChatServer_Broadcast(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	streamID := uuid.New()

	client1 := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "user1",
	}

	client2 := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "user2",
	}

	server.registerClient(client1)
	server.registerClient(client2)

	msg := &ChatMessage{
		Type:      "message",
		StreamID:  streamID,
		UserID:    client1.UserID,
		Username:  client1.Username,
		Message:   "Hello!",
		Timestamp: time.Now(),
	}

	server.broadcast(streamID, msg)

	select {
	case received := <-client1.send:
		assert.Equal(t, "Hello!", received.Message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client1 did not receive message")
	}

	select {
	case received := <-client2.send:
		assert.Equal(t, "Hello!", received.Message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client2 did not receive message")
	}
}

func TestChatServer_Broadcast_NoClients(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	streamID := uuid.New()
	msg := &ChatMessage{
		Type:      "message",
		StreamID:  streamID,
		Message:   "Hello!",
		Timestamp: time.Now(),
	}

	server.broadcast(streamID, msg)
}

func TestChatServer_BroadcastSystemMessage(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	streamID := uuid.New()
	client := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "user1",
	}

	server.registerClient(client)

	server.broadcastSystemMessage(streamID, "Test system message")

	select {
	case received := <-client.send:
		assert.Equal(t, "system", received.Type)
		assert.Equal(t, "Test system message", received.Message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client did not receive system message")
	}
}

func TestChatServer_SendToClient(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	client := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: uuid.New(),
		UserID:   uuid.New(),
		Username: "user1",
	}

	msg := &ChatMessage{
		Type:      "message",
		Message:   "Direct message",
		Timestamp: time.Now(),
	}

	server.sendToClient(client, msg)

	select {
	case received := <-client.send:
		assert.Equal(t, "Direct message", received.Message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client did not receive message")
	}
}

func TestChatServer_SendToClient_FullBuffer(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	client := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, 1),
		StreamID: uuid.New(),
		UserID:   uuid.New(),
		Username: "user1",
	}

	msg := &ChatMessage{
		Type:      "message",
		Message:   "Test",
		Timestamp: time.Now(),
	}

	client.send <- msg

	server.sendToClient(client, msg)
}

func TestChatServer_DeleteMessage(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	messageID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, ownerID).Return(false, nil)

	mockChatRepo.On("GetMessageByID", ctx, messageID).Return(&domain.ChatMessage{
		ID:       messageID,
		StreamID: streamID,
	}, nil)

	mockChatRepo.On("DeleteMessage", ctx, messageID).Return(nil)

	err := server.DeleteMessage(ctx, streamID, messageID, ownerID)
	assert.NoError(t, err)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_DeleteMessage_AsModerator(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	messageID := uuid.New()
	userID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, userID).Return(true, nil)

	mockChatRepo.On("GetMessageByID", ctx, messageID).Return(&domain.ChatMessage{
		ID:       messageID,
		StreamID: streamID,
	}, nil)

	mockChatRepo.On("DeleteMessage", ctx, messageID).Return(nil)

	err := server.DeleteMessage(ctx, streamID, messageID, userID)
	assert.NoError(t, err)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_DeleteMessage_Unauthorized(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	messageID := uuid.New()
	userID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, userID).Return(false, nil)

	err := server.DeleteMessage(ctx, streamID, messageID, userID)
	assert.ErrorIs(t, err, domain.ErrNotModerator)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_BanUser(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()
	ownerID := uuid.New()
	reason := "spam"
	duration := 10 * time.Minute

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, ownerID).Return(false, nil)

	mockChatRepo.On("BanUser", ctx, mock.MatchedBy(func(ban *domain.ChatBan) bool {
		return ban.StreamID == streamID &&
			ban.UserID == userID &&
			ban.BannedBy == ownerID &&
			ban.Reason == reason
	})).Return(nil)

	err := server.BanUser(ctx, streamID, userID, ownerID, reason, duration)
	assert.NoError(t, err)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_BanUser_AsModerator(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()
	moderatorID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, moderatorID).Return(true, nil)

	mockChatRepo.On("BanUser", ctx, mock.AnythingOfType("*domain.ChatBan")).Return(nil)

	err := server.BanUser(ctx, streamID, userID, moderatorID, "spam", 10*time.Minute)
	assert.NoError(t, err)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_BanUser_Unauthorized(t *testing.T) {
	server, mockChatRepo, mockStreamRepo := setupChatServerTest(t)

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()
	moderatorID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", ctx, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", ctx, streamID, moderatorID).Return(false, nil)

	err := server.BanUser(ctx, streamID, userID, moderatorID, "spam", 10*time.Minute)
	assert.ErrorIs(t, err, domain.ErrNotModerator)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatServer_GetConnectedUsers(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	streamID := uuid.New()

	count := server.GetConnectedUsers(streamID)
	assert.Equal(t, 0, count)

	client1 := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "user1",
	}

	client2 := &ChatClient{
		server:   server,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   uuid.New(),
		Username: "user2",
	}

	server.registerClient(client1)
	server.registerClient(client2)

	count = server.GetConnectedUsers(streamID)
	assert.Equal(t, 2, count)
}

func TestRateLimitMemberUniqueness(t *testing.T) {
	const calls = 100
	seen := make(map[string]struct{}, calls)
	for i := range calls {
		m := rateLimitMember()
		if _, dup := seen[m]; dup {
			t.Fatalf("duplicate rateLimitMember on call %d: %q", i, m)
		}
		seen[m] = struct{}{}
	}
}

func TestChatServer_Shutdown(t *testing.T) {
	server, _, _ := setupChatServerTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	assert.NoError(t, err)
}

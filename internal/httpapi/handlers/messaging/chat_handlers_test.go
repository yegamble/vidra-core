package messaging

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/chat"
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepository) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

type MockSubscriptionRepository struct {
	mock.Mock
}

func (m *MockSubscriptionRepository) IsSubscribed(ctx context.Context, subscriberID, channelID uuid.UUID) (bool, error) {
	args := m.Called(ctx, subscriberID, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockSubscriptionRepository) SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	return nil
}

func (m *MockSubscriptionRepository) UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	return nil
}

func (m *MockSubscriptionRepository) ListUserSubscriptions(_ context.Context, _ uuid.UUID, _, _ int, _ ...string) (*domain.SubscriptionResponse, error) {
	return nil, nil
}

func (m *MockSubscriptionRepository) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	return nil, nil
}

func (m *MockSubscriptionRepository) GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error) {
	return nil, 0, nil
}

func (m *MockSubscriptionRepository) Subscribe(ctx context.Context, subscriberID, channelID string) error {
	return nil
}

func (m *MockSubscriptionRepository) Unsubscribe(ctx context.Context, subscriberID, channelID string) error {
	return nil
}

func (m *MockSubscriptionRepository) ListSubscriptions(_ context.Context, _ string, _, _ int, _ ...string) ([]*domain.User, int64, error) {
	return nil, 0, nil
}

func (m *MockSubscriptionRepository) ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (m *MockSubscriptionRepository) CountSubscribers(ctx context.Context, channelID string) (int64, error) {
	return 0, nil
}

func (m *MockSubscriptionRepository) GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error) {
	return nil, nil
}

func (m *MockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockUserRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockUserRepository) Anonymize(_ context.Context, _ string) error { return nil }

func withUserContext(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, userID.String())
}

func setupChatHandlerTest(t *testing.T) (*ChatHandlers, *MockChatRepository, *MockLiveStreamRepository, *MockUserRepository, *chat.ChatServer) {
	mockChatRepo := new(MockChatRepository)
	mockStreamRepo := new(MockLiveStreamRepository)
	mockUserRepo := new(MockUserRepository)
	mockSubscriptionRepo := new(MockSubscriptionRepository)

	cfg := &config.Config{}
	redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	logger := slog.Default()

	chatServer := chat.NewChatServer(cfg, mockChatRepo, mockStreamRepo, redisClient, logger)

	handlers := NewChatHandlers(chatServer, mockChatRepo, mockStreamRepo, mockUserRepo, mockSubscriptionRepo)

	return handlers, mockChatRepo, mockStreamRepo, mockUserRepo, chatServer
}

func TestChatHandlers_GetChatMessages_PublicStream(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	messages := []*domain.ChatMessage{
		domain.NewChatMessage(streamID, uuid.New(), "user1", "Hello"),
		domain.NewChatMessage(streamID, uuid.New(), "user2", "Hi"),
	}

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:      streamID,
		Privacy: "public",
	}, nil)

	mockChatRepo.On("GetMessages", mock.Anything, streamID, 50, 0).Return(messages, nil)
	mockChatRepo.On("GetMessageCount", mock.Anything, streamID).Return(2, nil)

	req := httptest.NewRequest("GET", "/api/v1/streams/"+streamID.String()+"/chat/messages", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handlers.GetChatMessages(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	mockStreamRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_GetChatMessages_PrivateStreamUnauthorized(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:      streamID,
		UserID:  ownerID,
		Privacy: "private",
	}, nil)

	req := httptest.NewRequest("GET", "/api/v1/streams/"+streamID.String()+"/chat/messages", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handlers.GetChatMessages(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_DeleteMessage_AsModerator(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	messageID := uuid.New()
	moderatorID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", mock.Anything, streamID, moderatorID).Return(true, nil)

	mockChatRepo.On("GetMessageByID", mock.Anything, messageID).Return(&domain.ChatMessage{
		ID:       messageID,
		StreamID: streamID,
	}, nil)

	mockChatRepo.On("DeleteMessage", mock.Anything, messageID).Return(nil)

	req := httptest.NewRequest("DELETE", "/api/v1/streams/"+streamID.String()+"/chat/messages/"+messageID.String(), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	rctx.URLParams.Add("messageId", messageID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), moderatorID))

	handlers.DeleteMessage(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_AddModerator_AsOwner(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, mockUserRepo, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	newModID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockUserRepo.On("GetByID", mock.Anything, newModID.String()).Return(&domain.User{
		ID:       newModID.String(),
		Username: "newmod",
	}, nil)

	mockChatRepo.On("AddModerator", mock.Anything, mock.MatchedBy(func(mod *domain.ChatModerator) bool {
		return mod.StreamID == streamID && mod.UserID == newModID && mod.GrantedBy == ownerID
	})).Return(nil)

	reqBody := map[string]string{"user_id": newModID.String()}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/moderators", bytes.NewReader(body))
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), ownerID))

	handlers.AddModerator(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockStreamRepo.AssertExpectations(t)
	mockUserRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_AddModerator_NotOwner(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	otherUserID := uuid.New()
	newModID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	reqBody := map[string]string{"user_id": newModID.String()}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/moderators", bytes.NewReader(body))
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), otherUserID))

	handlers.AddModerator(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_BanUser_AsModerator(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	moderatorID := uuid.New()
	bannedUserID := uuid.New()
	ownerID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	mockChatRepo.On("IsModerator", mock.Anything, streamID, moderatorID).Return(true, nil)
	mockChatRepo.On("BanUser", mock.Anything, mock.MatchedBy(func(ban *domain.ChatBan) bool {
		return ban.StreamID == streamID && ban.UserID == bannedUserID && ban.BannedBy == moderatorID
	})).Return(nil)

	reqBody := map[string]interface{}{
		"user_id":  bannedUserID.String(),
		"reason":   "spam",
		"duration": 600,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/bans", bytes.NewReader(body))
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), moderatorID))

	handlers.BanUser(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_GetChatStats(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	userID := uuid.New()

	stats := &domain.ChatStreamStats{
		StreamID:       streamID,
		MessageCount:   100,
		UniqueChatters: 25,
		ActiveBanCount: 2,
		ModeratorCount: 3,
	}

	mockChatRepo.On("GetStreamStats", mock.Anything, streamID).Return(stats, nil)

	req := httptest.NewRequest("GET", "/api/v1/streams/"+streamID.String()+"/chat/stats", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), userID))

	handlers.GetChatStats(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response["success"].(bool))
	data := response["data"].(map[string]interface{})

	assert.NotNil(t, data["stats"])
	assert.NotNil(t, data["connected_users"])

	statsData := data["stats"].(map[string]interface{})
	assert.Equal(t, float64(100), statsData["message_count"])
	assert.Equal(t, float64(25), statsData["unique_chatters"])
	assert.Equal(t, float64(0), data["connected_users"])

	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest("GET", "/api/v1/streams/invalid-uuid/chat/messages", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handlers.GetChatMessages(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestChatHandlers_GetChatMessages_PrivateStreamSubscriberAuthorized(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	subscriberID := uuid.New()
	channelID := uuid.New()

	messages := []*domain.ChatMessage{
		domain.NewChatMessage(streamID, uuid.New(), "user1", "Hello"),
	}

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		UserID:    ownerID,
		Privacy:   "private",
		ChannelID: channelID,
	}, nil)

	mockSubscriptionRepo := new(MockSubscriptionRepository)
	mockSubscriptionRepo.On("IsSubscribed", mock.Anything, subscriberID, channelID).Return(true, nil)
	handlers.subscriptionRepo = mockSubscriptionRepo

	mockChatRepo.On("GetMessages", mock.Anything, streamID, 50, 0).Return(messages, nil)
	mockChatRepo.On("GetMessageCount", mock.Anything, streamID).Return(1, nil)

	req := httptest.NewRequest("GET", "/api/v1/streams/"+streamID.String()+"/chat/messages", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), subscriberID))

	handlers.GetChatMessages(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockStreamRepo.AssertExpectations(t)
	mockSubscriptionRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_GetChatMessages_PrivateStreamNonSubscriberDenied(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	nonSubscriberID := uuid.New()
	channelID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		UserID:    ownerID,
		Privacy:   "private",
		ChannelID: channelID,
	}, nil)

	mockSubscriptionRepo := new(MockSubscriptionRepository)
	mockSubscriptionRepo.On("IsSubscribed", mock.Anything, nonSubscriberID, channelID).Return(false, nil)
	handlers.subscriptionRepo = mockSubscriptionRepo

	req := httptest.NewRequest("GET", "/api/v1/streams/"+streamID.String()+"/chat/messages", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), nonSubscriberID))

	handlers.GetChatMessages(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	mockStreamRepo.AssertExpectations(t)
	mockSubscriptionRepo.AssertExpectations(t)
}

// fakeInvoiceRepo is the seam for the system-message handler tests. The real BTCPayRepository
// hits a database; for unit tests we want explicit control over which invoice the lookup
// returns and whether the conditional UPDATE wins.
type fakeInvoiceRepo struct {
	getByID    func(ctx context.Context, id string) (*domain.BTCPayInvoice, error)
	markCalled int
	markErr    error
}

func (f *fakeInvoiceRepo) GetInvoiceByID(ctx context.Context, id string) (*domain.BTCPayInvoice, error) {
	return f.getByID(ctx, id)
}

func (f *fakeInvoiceRepo) MarkInvoiceSystemMessageBroadcast(_ context.Context, _ string) error {
	f.markCalled++
	return f.markErr
}

// systemMessageRequest builds the JSON body for the broadcast endpoint.
func systemMessageRequest(invoiceID string) []byte {
	body, _ := json.Marshal(map[string]string{"invoice_id": invoiceID})
	return body
}

func TestChatHandlers_BroadcastTipSystemMessage_HappyPath(t *testing.T) {
	handlers, _, mockStreamRepo, mockUserRepo, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	channelID := uuid.New()
	callerID := uuid.New()
	invoiceID := uuid.NewString()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		ChannelID: channelID,
	}, nil)
	mockUserRepo.On("GetByID", mock.Anything, callerID.String()).Return(&domain.User{
		ID:       callerID.String(),
		Username: "alice",
	}, nil)

	meta, _ := json.Marshal(map[string]string{"type": "tip", "channel_id": channelID.String()})
	repo := &fakeInvoiceRepo{
		getByID: func(_ context.Context, id string) (*domain.BTCPayInvoice, error) {
			require.Equal(t, invoiceID, id)
			return &domain.BTCPayInvoice{
				ID:         invoiceID,
				UserID:     callerID.String(),
				AmountSats: 1000,
				Status:     domain.InvoiceStatusSettled,
				Metadata:   meta,
			}, nil
		},
	}
	handlers.SetInvoiceRepo(repo)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/system-message",
		bytes.NewReader(systemMessageRequest(invoiceID)))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), callerID))

	handlers.BroadcastTipSystemMessage(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "alice")
	assert.Contains(t, w.Body.String(), "1000 sat")
	assert.Equal(t, 1, repo.markCalled)
}

func TestChatHandlers_BroadcastTipSystemMessage_RejectsAlienInvoice(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	callerID := uuid.New()
	otherUserID := uuid.New()
	streamID := uuid.New()
	invoiceID := uuid.NewString()

	repo := &fakeInvoiceRepo{
		getByID: func(_ context.Context, _ string) (*domain.BTCPayInvoice, error) {
			return &domain.BTCPayInvoice{
				ID:     invoiceID,
				UserID: otherUserID.String(),
				Status: domain.InvoiceStatusSettled,
			}, nil
		},
	}
	handlers.SetInvoiceRepo(repo)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/system-message",
		bytes.NewReader(systemMessageRequest(invoiceID)))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), callerID))

	handlers.BroadcastTipSystemMessage(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, repo.markCalled)
}

func TestChatHandlers_BroadcastTipSystemMessage_RejectsUnsettledInvoice(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	callerID := uuid.New()
	streamID := uuid.New()
	invoiceID := uuid.NewString()

	repo := &fakeInvoiceRepo{
		getByID: func(_ context.Context, _ string) (*domain.BTCPayInvoice, error) {
			return &domain.BTCPayInvoice{
				ID:     invoiceID,
				UserID: callerID.String(),
				Status: domain.InvoiceStatusProcessing,
			}, nil
		},
	}
	handlers.SetInvoiceRepo(repo)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/system-message",
		bytes.NewReader(systemMessageRequest(invoiceID)))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), callerID))

	handlers.BroadcastTipSystemMessage(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 0, repo.markCalled)
}

func TestChatHandlers_BroadcastTipSystemMessage_RejectsChannelMismatch(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	callerID := uuid.New()
	streamID := uuid.New()
	streamChannelID := uuid.New()
	invoiceChannelID := uuid.New()
	invoiceID := uuid.NewString()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		ChannelID: streamChannelID,
	}, nil)

	meta, _ := json.Marshal(map[string]string{"type": "tip", "channel_id": invoiceChannelID.String()})
	repo := &fakeInvoiceRepo{
		getByID: func(_ context.Context, _ string) (*domain.BTCPayInvoice, error) {
			return &domain.BTCPayInvoice{
				ID:         invoiceID,
				UserID:     callerID.String(),
				AmountSats: 500,
				Status:     domain.InvoiceStatusSettled,
				Metadata:   meta,
			}, nil
		},
	}
	handlers.SetInvoiceRepo(repo)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/system-message",
		bytes.NewReader(systemMessageRequest(invoiceID)))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), callerID))

	handlers.BroadcastTipSystemMessage(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, repo.markCalled)
}

func TestChatHandlers_BroadcastTipSystemMessage_RejectsReplay(t *testing.T) {
	handlers, _, mockStreamRepo, mockUserRepo, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	channelID := uuid.New()
	callerID := uuid.New()
	invoiceID := uuid.NewString()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		ChannelID: channelID,
	}, nil)
	mockUserRepo.On("GetByID", mock.Anything, callerID.String()).Return(&domain.User{
		ID:       callerID.String(),
		Username: "alice",
	}, nil).Maybe()

	meta, _ := json.Marshal(map[string]string{"type": "tip", "channel_id": channelID.String()})
	repo := &fakeInvoiceRepo{
		getByID: func(_ context.Context, _ string) (*domain.BTCPayInvoice, error) {
			return &domain.BTCPayInvoice{
				ID:         invoiceID,
				UserID:     callerID.String(),
				AmountSats: 100,
				Status:     domain.InvoiceStatusSettled,
				Metadata:   meta,
			}, nil
		},
		markErr: domain.ErrInvoiceAlreadyBroadcast,
	}
	handlers.SetInvoiceRepo(repo)

	req := httptest.NewRequest("POST", "/api/v1/streams/"+streamID.String()+"/chat/system-message",
		bytes.NewReader(systemMessageRequest(invoiceID)))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), callerID))

	handlers.BroadcastTipSystemMessage(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 1, repo.markCalled)
}

func TestChatHandlers_SetSlowMode_AsModerator(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	moderatorID := uuid.New()
	ownerID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, moderatorID).Return(true, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{ID: streamID, UserID: ownerID}, nil).Maybe()
	mockStreamRepo.On("SetSlowMode", mock.Anything, streamID, 30).Return(nil)

	body, _ := json.Marshal(map[string]int{"seconds": 30})
	req := httptest.NewRequest("PUT", "/api/v1/streams/"+streamID.String()+"/chat/slow-mode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), moderatorID))

	handlers.SetSlowMode(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockStreamRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_SetSlowMode_NonModeratorForbidden(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	userID := uuid.New()
	ownerID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, userID).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{ID: streamID, UserID: ownerID}, nil)

	body, _ := json.Marshal(map[string]int{"seconds": 10})
	req := httptest.NewRequest("PUT", "/api/v1/streams/"+streamID.String()+"/chat/slow-mode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("streamId", streamID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withUserContext(req.Context(), userID))

	handlers.SetSlowMode(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestChatHandlers_SetSlowMode_RejectsOutOfRange(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	moderatorID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, moderatorID).Return(true, nil).Maybe()
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{ID: streamID}, nil).Maybe()

	cases := map[string]int{
		"negative": -1,
		"too_big":  601,
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]int{"seconds": val})
			req := httptest.NewRequest("PUT", "/api/v1/streams/"+streamID.String()+"/chat/slow-mode", bytes.NewReader(body))
			w := httptest.NewRecorder()
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("streamId", streamID.String())
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			req = req.WithContext(withUserContext(req.Context(), moderatorID))

			handlers.SetSlowMode(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

package chat

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
)

const (
	writeWait = 10 * time.Second

	pongWait = 60 * time.Second

	pingPeriod = (pongWait * 9) / 10

	maxMessageSize = 512

	clientSendBuffer = 256
)

type moderatorCacheEntry struct {
	isMod    bool
	cachedAt time.Time
}

type ChatServer struct {
	cfg        *config.Config
	chatRepo   repository.ChatRepository
	streamRepo repository.LiveStreamRepository
	redis      *redis.Client
	logger     *slog.Logger

	mu          sync.RWMutex
	connections map[uuid.UUID]map[*ChatClient]bool
	Upgrader    websocket.Upgrader

	moderatorCache sync.Map

	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

type ChatClient struct {
	server   *ChatServer
	conn     *websocket.Conn
	send     chan *ChatMessage
	StreamID uuid.UUID
	UserID   uuid.UUID
	Username string
}

type ChatMessage struct {
	Type      string                 `json:"type"`
	ID        uuid.UUID              `json:"id,omitempty"`
	StreamID  uuid.UUID              `json:"stream_id"`
	UserID    uuid.UUID              `json:"user_id,omitempty"`
	Username  string                 `json:"username,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func NewChatServer(
	cfg *config.Config,
	chatRepo repository.ChatRepository,
	streamRepo repository.LiveStreamRepository,
	redis *redis.Client,
	logger *slog.Logger,
) *ChatServer {
	s := &ChatServer{
		cfg:          cfg,
		chatRepo:     chatRepo,
		streamRepo:   streamRepo,
		redis:        redis,
		logger:       logger,
		connections:  make(map[uuid.UUID]map[*ChatClient]bool),
		shutdownChan: make(chan struct{}),
	}

	s.Upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     s.checkWebSocketOrigin,
	}

	return s
}

func (s *ChatServer) checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	allowedOrigins := make(map[string]bool)

	if s.cfg.PublicBaseURL != "" {
		allowedOrigins[s.cfg.PublicBaseURL] = true
	}

	isDevelopment := s.cfg.ValidationTestMode ||
		s.cfg.LogLevel == "debug" ||
		(s.cfg.PublicBaseURL != "" &&
			((len(s.cfg.PublicBaseURL) >= 16 && s.cfg.PublicBaseURL[:16] == "http://localhost") ||
				(len(s.cfg.PublicBaseURL) >= 14 && s.cfg.PublicBaseURL[:14] == "http://127.0.0")))

	if isDevelopment {
		allowedOrigins["http://localhost:3000"] = true
		allowedOrigins["http://localhost:8080"] = true
		allowedOrigins["http://127.0.0.1:3000"] = true
		allowedOrigins["http://127.0.0.1:8080"] = true
	}

	if allowedOrigins[origin] {
		return true
	}

	slog.Warn("WebSocket connection rejected: invalid origin")

	return false
}

func (s *ChatServer) HandleWebSocket(ctx context.Context, conn *websocket.Conn, streamID, userID uuid.UUID, username string) error {
	banned, err := s.chatRepo.IsUserBanned(ctx, streamID, userID)
	if err != nil {
		s.logger.With("error", err).Error("Failed to check ban status")
		return fmt.Errorf("failed to check ban status: %w", err)
	}
	if banned {
		if err := conn.WriteJSON(map[string]string{"error": "You are banned from this chat"}); err != nil {
			slog.Warn("Failed to write ban message", "error", err)
		}
		if err := conn.Close(); err != nil {
			slog.Warn("Failed to close banned connection", "error", err)
		}
		return domain.ErrUserBanned
	}

	client := &ChatClient{
		server:   s,
		conn:     conn,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   userID,
		Username: username,
	}

	s.registerClient(client)
	defer s.unregisterClient(client)

	s.broadcastSystemMessage(streamID, fmt.Sprintf("%s joined the chat", username))

	s.wg.Add(2)
	go s.writePump(client)
	s.readPump(client)

	return nil
}

func (s *ChatServer) registerClient(client *ChatClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connections[client.StreamID] == nil {
		s.connections[client.StreamID] = make(map[*ChatClient]bool)
	}
	s.connections[client.StreamID][client] = true

	slog.Info("Client connected to chat")
}

func (s *ChatServer) unregisterClient(client *ChatClient) {
	var shouldSendLeaveMessage bool
	var username string
	var streamID uuid.UUID

	s.mu.Lock()
	if clients, ok := s.connections[client.StreamID]; ok {
		if _, exists := clients[client]; exists {
			delete(clients, client)
			close(client.send)

			if len(clients) == 0 {
				delete(s.connections, client.StreamID)
			}

			slog.Info("Client disconnected from chat")

			shouldSendLeaveMessage = true
			username = client.Username
			streamID = client.StreamID
		}
	}
	s.mu.Unlock()

	if shouldSendLeaveMessage {
		s.broadcastSystemMessage(streamID, fmt.Sprintf("%s left the chat", username))
	}
}

func (s *ChatServer) readPump(client *ChatClient) {
	defer func() {
		s.wg.Done()
		s.unregisterClient(client)
		if err := client.conn.Close(); err != nil {
			s.logger.With("error", err).Debug("Error closing connection in readPump")
		}
	}()

	client.conn.SetReadLimit(maxMessageSize)
	if err := client.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		s.logger.With("error", err).Warn("Failed to set read deadline")
	}
	client.conn.SetPongHandler(func(string) error {
		if err := client.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			s.logger.With("error", err).Warn("Failed to set read deadline in pong handler")
		}
		return nil
	})

	for {
		select {
		case <-s.shutdownChan:
			return
		default:
		}

		var msg ChatMessage
		err := client.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.With("error", err).Warn("WebSocket error")
			}
			break
		}

		s.handleMessage(client, &msg)
	}
}

func (s *ChatServer) writePump(client *ChatClient) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		s.wg.Done()
		ticker.Stop()
		if err := client.conn.Close(); err != nil {
			s.logger.With("error", err).Debug("Error closing connection in writePump")
		}
	}()

	for {
		select {
		case message, ok := <-client.send:
			if err := client.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				s.logger.With("error", err).Warn("Failed to set write deadline")
				return
			}
			if !ok {
				if err := client.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					s.logger.With("error", err).Debug("Failed to write close message")
				}
				return
			}

			if err := client.conn.WriteJSON(message); err != nil {
				s.logger.With("error", err).Error("Failed to write message")
				return
			}

		case <-ticker.C:
			if err := client.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				s.logger.With("error", err).Warn("Failed to set write deadline for ping")
				return
			}
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-s.shutdownChan:
			return
		}
	}
}

func (s *ChatServer) handleMessage(client *ChatClient, msg *ChatMessage) {
	ctx := context.Background()

	if !s.checkRateLimit(ctx, client.UserID, client.StreamID) {
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   "Rate limit exceeded. Please slow down.",
			Timestamp: time.Now(),
		})
		return
	}

	banned, err := s.chatRepo.IsUserBanned(ctx, client.StreamID, client.UserID)
	if err != nil {
		s.logger.With("error", err).Error("Failed to check ban status")
		return
	}
	if banned {
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   "You have been banned from this chat",
			Timestamp: time.Now(),
		})
		return
	}

	domainMsg := domain.NewChatMessage(
		client.StreamID,
		client.UserID,
		client.Username,
		msg.Message,
	)

	if err := domainMsg.Validate(); err != nil {
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   err.Error(),
			Timestamp: time.Now(),
		})
		return
	}

	if err := s.chatRepo.CreateMessage(ctx, domainMsg); err != nil {
		s.logger.With("error", err).Error("Failed to save message")
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   "Failed to send message",
			Timestamp: time.Now(),
		})
		return
	}

	wsMsg := &ChatMessage{
		Type:      "message",
		ID:        domainMsg.ID,
		StreamID:  domainMsg.StreamID,
		UserID:    domainMsg.UserID,
		Username:  domainMsg.Username,
		Message:   domainMsg.Message,
		Timestamp: domainMsg.CreatedAt,
		Metadata:  domainMsg.Metadata,
	}
	s.broadcast(client.StreamID, wsMsg)
}

func (s *ChatServer) broadcast(streamID uuid.UUID, message *ChatMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clients, ok := s.connections[streamID]
	if !ok {
		return
	}

	for client := range clients {
		select {
		case client.send <- message:
		default:
			slog.Warn("Client send buffer full, dropping message")
		}
	}
}

func (s *ChatServer) broadcastSystemMessage(streamID uuid.UUID, message string) {
	wsMsg := &ChatMessage{
		Type:      "system",
		StreamID:  streamID,
		Username:  "System",
		Message:   message,
		Timestamp: time.Now(),
	}
	s.broadcast(streamID, wsMsg)
}

func (s *ChatServer) sendToClient(client *ChatClient, message *ChatMessage) {
	select {
	case client.send <- message:
	default:
		slog.Warn("Failed to send message to client, buffer full")
	}
}

const moderatorCacheTTL = 60 * time.Second

func (s *ChatServer) isModerator(ctx context.Context, streamID, userID uuid.UUID) bool {
	cacheKey := streamID.String() + ":" + userID.String()
	if v, ok := s.moderatorCache.Load(cacheKey); ok {
		entry := v.(moderatorCacheEntry)
		if time.Since(entry.cachedAt) < moderatorCacheTTL {
			return entry.isMod
		}
	}
	isMod, err := s.chatRepo.IsModerator(ctx, streamID, userID)
	if err != nil {
		s.logger.With("error", err).Error("Failed to check moderator status")
	}
	s.moderatorCache.Store(cacheKey, moderatorCacheEntry{isMod: isMod, cachedAt: time.Now()})
	return isMod
}

func (s *ChatServer) checkRateLimit(ctx context.Context, userID, streamID uuid.UUID) bool {
	isMod := s.isModerator(ctx, streamID, userID)

	limit := 5
	window := 10 * time.Second
	if isMod {
		limit = 10
		window = 10 * time.Second
	}

	key := fmt.Sprintf("chat:ratelimit:%s:%s", streamID, userID)

	pipe := s.redis.Pipeline()
	now := time.Now().Unix()

	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", now-int64(window.Seconds())))

	countCmd := pipe.ZCard(ctx, key)

	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: rateLimitMember()})

	pipe.Expire(ctx, key, window)

	_, execErr := pipe.Exec(ctx)
	if execErr != nil {
		s.logger.With("error", execErr).Error("Failed to check rate limit")
		return true
	}

	count := countCmd.Val()
	return count < int64(limit)
}

func (s *ChatServer) DeleteMessage(ctx context.Context, streamID, messageID uuid.UUID, moderatorID uuid.UUID) error {
	isMod, err := s.chatRepo.IsModerator(ctx, streamID, moderatorID)
	if err != nil {
		return fmt.Errorf("failed to check moderator status: %w", err)
	}

	isOwner := false
	if s.streamRepo != nil {
		stream, err := s.streamRepo.GetByID(ctx, streamID)
		if err == nil && stream != nil {
			isOwner = stream.UserID == moderatorID
		}
	}

	if !isMod && !isOwner {
		return domain.ErrNotModerator
	}

	msg, err := s.chatRepo.GetMessageByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}
	if msg.StreamID != streamID {
		return fmt.Errorf("message does not belong to this stream")
	}

	if err := s.chatRepo.DeleteMessage(ctx, messageID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	wsMsg := &ChatMessage{
		Type:      "delete",
		ID:        messageID,
		StreamID:  streamID,
		Timestamp: time.Now(),
	}
	s.broadcast(streamID, wsMsg)

	slog.Info("Message deleted by moderator")

	return nil
}

// BanRequest groups the parameters for BanUser.
type BanRequest struct {
	StreamID    uuid.UUID
	UserID      uuid.UUID
	ModeratorID uuid.UUID
	Reason      string
	Duration    time.Duration
}

func (s *ChatServer) BanUser(ctx context.Context, req BanRequest) error {
	isMod, err := s.chatRepo.IsModerator(ctx, req.StreamID, req.ModeratorID)
	if err != nil {
		return fmt.Errorf("failed to check moderator status: %w", err)
	}

	isOwner := false
	if s.streamRepo != nil {
		stream, err := s.streamRepo.GetByID(ctx, req.StreamID)
		if err == nil && stream != nil {
			isOwner = stream.UserID == req.ModeratorID
		}
	}

	if !isMod && !isOwner {
		return domain.ErrNotModerator
	}

	ban := domain.NewChatBan(req.StreamID, req.UserID, req.ModeratorID, req.Reason, req.Duration)
	if err := s.chatRepo.BanUser(ctx, ban); err != nil {
		return fmt.Errorf("failed to ban user: %w", err)
	}

	s.disconnectUser(req.StreamID, req.UserID)

	banType := "timeout"
	if req.Duration == 0 {
		banType = "ban"
	}

	wsMsg := &ChatMessage{
		Type:      banType,
		StreamID:  req.StreamID,
		UserID:    req.UserID,
		Message:   req.Reason,
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"duration": req.Duration.Seconds(),
		},
	}
	s.broadcast(req.StreamID, wsMsg)

	slog.Info("User banned from chat")

	return nil
}

func (s *ChatServer) disconnectUser(streamID, userID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clients, ok := s.connections[streamID]
	if !ok {
		return
	}

	for client := range clients {
		if client.UserID == userID {
			client.send <- &ChatMessage{
				Type:      "disconnect",
				StreamID:  streamID,
				Message:   "You have been disconnected from chat",
				Timestamp: time.Now(),
			}

			if err := client.conn.Close(); err != nil {
				s.logger.With("error", err).Debug("Failed to close connection when disconnecting user")
			}
			delete(clients, client)
			close(client.send)

			if len(clients) == 0 {
				delete(s.connections, streamID)
			}

			break
		}
	}
}

func (s *ChatServer) GetConnectedUsers(streamID uuid.UUID) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clients, ok := s.connections[streamID]
	if !ok {
		return 0
	}

	return len(clients)
}

func (s *ChatServer) Shutdown(ctx context.Context) error {
	slog.Info("Shutting down chat server...")

	close(s.shutdownChan)

	s.mu.Lock()
	for streamID, clients := range s.connections {
		for client := range clients {
			if err := client.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "Server shutting down"),
			); err != nil {
				s.logger.With("error", err).Debug("Failed to write shutdown message")
			}
			if err := client.conn.Close(); err != nil {
				s.logger.With("error", err).Debug("Failed to close connection during shutdown")
			}
		}
		delete(s.connections, streamID)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("Chat server shutdown complete")
		return nil
	case <-ctx.Done():
		slog.Warn("Chat server shutdown timeout")
		return ctx.Err()
	}
}

func rateLimitMember() string {
	return fmt.Sprintf("%d:%s", time.Now().UnixNano(), uuid.New().String())
}

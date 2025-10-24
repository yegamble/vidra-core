package chat

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512

	// Buffer size for client send channel
	clientSendBuffer = 256
)

// ChatServer manages WebSocket connections for live stream chat
type ChatServer struct {
	cfg        *config.Config
	chatRepo   repository.ChatRepository
	streamRepo repository.LiveStreamRepository
	redis      *redis.Client
	logger     *logrus.Logger

	// Connection management
	mu          sync.RWMutex
	connections map[uuid.UUID]map[*ChatClient]bool // streamID -> clients
	Upgrader    websocket.Upgrader

	// Shutdown
	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

// ChatClient represents a connected chat user
type ChatClient struct {
	server   *ChatServer
	conn     *websocket.Conn
	send     chan *ChatMessage
	StreamID uuid.UUID
	UserID   uuid.UUID
	Username string
}

// ChatMessage represents a WebSocket message
type ChatMessage struct {
	Type      string                 `json:"type"` // message, system, join, leave, delete, ban
	ID        uuid.UUID              `json:"id,omitempty"`
	StreamID  uuid.UUID              `json:"stream_id"`
	UserID    uuid.UUID              `json:"user_id,omitempty"`
	Username  string                 `json:"username,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// NewChatServer creates a new chat server
func NewChatServer(
	cfg *config.Config,
	chatRepo repository.ChatRepository,
	streamRepo repository.LiveStreamRepository,
	redis *redis.Client,
	logger *logrus.Logger,
) *ChatServer {
	server := &ChatServer{
		cfg:         cfg,
		chatRepo:    chatRepo,
		streamRepo:  streamRepo,
		redis:       redis,
		logger:      logger,
		connections: make(map[uuid.UUID]map[*ChatClient]bool),
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     server.checkWebSocketOrigin,
		},
		shutdownChan: make(chan struct{}),
	}
	return server
}

// checkWebSocketOrigin validates the WebSocket origin to prevent CSRF attacks
func (s *ChatServer) checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No origin header - allow (some clients don't send it)
		return true
	}

	// Build list of allowed origins
	allowedOrigins := make(map[string]bool)

	// Add public base URL from config
	if s.cfg.PublicBaseURL != "" {
		allowedOrigins[s.cfg.PublicBaseURL] = true
	}

	// Add localhost for development
	if s.cfg.Environment == "development" || s.cfg.Environment == "test" {
		allowedOrigins["http://localhost:3000"] = true
		allowedOrigins["http://localhost:8080"] = true
		allowedOrigins["http://127.0.0.1:3000"] = true
		allowedOrigins["http://127.0.0.1:8080"] = true
	}

	// Check if origin is allowed
	if allowedOrigins[origin] {
		return true
	}

	// Log rejected origin for security monitoring
	s.logger.WithFields(logrus.Fields{
		"origin":     origin,
		"remote_ip":  r.RemoteAddr,
		"user_agent": r.UserAgent(),
	}).Warn("WebSocket connection rejected: invalid origin")

	return false
}

// HandleWebSocket handles a new WebSocket connection
func (s *ChatServer) HandleWebSocket(ctx context.Context, conn *websocket.Conn, streamID, userID uuid.UUID, username string) error {
	// Check if user is banned
	banned, err := s.chatRepo.IsUserBanned(ctx, streamID, userID)
	if err != nil {
		s.logger.WithError(err).Error("Failed to check ban status")
		return fmt.Errorf("failed to check ban status: %w", err)
	}
	if banned {
		if err := conn.WriteJSON(map[string]string{"error": "You are banned from this chat"}); err != nil {
			log.Printf("Failed to write ban message: %v", err)
		}
		if err := conn.Close(); err != nil {
			log.Printf("Failed to close banned connection: %v", err)
		}
		return domain.ErrUserBanned
	}

	// Create client
	client := &ChatClient{
		server:   s,
		conn:     conn,
		send:     make(chan *ChatMessage, clientSendBuffer),
		StreamID: streamID,
		UserID:   userID,
		Username: username,
	}

	// Register client
	s.registerClient(client)
	defer s.unregisterClient(client)

	// Send join message
	s.broadcastSystemMessage(streamID, fmt.Sprintf("%s joined the chat", username))

	// Start goroutines
	s.wg.Add(2)
	go s.writePump(client)
	s.readPump(client) // Blocking call

	return nil
}

// registerClient adds a client to the connections map
func (s *ChatServer) registerClient(client *ChatClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connections[client.StreamID] == nil {
		s.connections[client.StreamID] = make(map[*ChatClient]bool)
	}
	s.connections[client.StreamID][client] = true

	s.logger.WithFields(logrus.Fields{
		"stream_id": client.StreamID,
		"user_id":   client.UserID,
		"username":  client.Username,
	}).Info("Client connected to chat")
}

// unregisterClient removes a client from the connections map
func (s *ChatServer) unregisterClient(client *ChatClient) {
	// We need to release the lock before calling broadcast to avoid deadlock
	var shouldSendLeaveMessage bool
	var username string
	var streamID uuid.UUID

	s.mu.Lock()
	if clients, ok := s.connections[client.StreamID]; ok {
		if _, exists := clients[client]; exists {
			delete(clients, client)
			close(client.send)

			// Clean up empty stream maps
			if len(clients) == 0 {
				delete(s.connections, client.StreamID)
			}

			s.logger.WithFields(logrus.Fields{
				"stream_id": client.StreamID,
				"user_id":   client.UserID,
				"username":  client.Username,
			}).Info("Client disconnected from chat")

			shouldSendLeaveMessage = true
			username = client.Username
			streamID = client.StreamID
		}
	}
	s.mu.Unlock()

	// Send leave message after releasing the lock
	if shouldSendLeaveMessage {
		s.broadcastSystemMessage(streamID, fmt.Sprintf("%s left the chat", username))
	}
}

// readPump pumps messages from the WebSocket connection to the server
func (s *ChatServer) readPump(client *ChatClient) {
	defer func() {
		s.wg.Done()
		s.unregisterClient(client)
		if err := client.conn.Close(); err != nil {
			s.logger.WithError(err).Debug("Error closing connection in readPump")
		}
	}()

	client.conn.SetReadLimit(maxMessageSize)
	if err := client.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		s.logger.WithError(err).Warn("Failed to set read deadline")
	}
	client.conn.SetPongHandler(func(string) error {
		if err := client.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			s.logger.WithError(err).Warn("Failed to set read deadline in pong handler")
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
				s.logger.WithError(err).Warn("WebSocket error")
			}
			break
		}

		// Handle the message
		s.handleMessage(client, &msg)
	}
}

// writePump pumps messages from the server to the WebSocket connection
func (s *ChatServer) writePump(client *ChatClient) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		s.wg.Done()
		ticker.Stop()
		if err := client.conn.Close(); err != nil {
			s.logger.WithError(err).Debug("Error closing connection in writePump")
		}
	}()

	for {
		select {
		case message, ok := <-client.send:
			if err := client.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				s.logger.WithError(err).Warn("Failed to set write deadline")
				return
			}
			if !ok {
				// Channel closed
				if err := client.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					s.logger.WithError(err).Debug("Failed to write close message")
				}
				return
			}

			if err := client.conn.WriteJSON(message); err != nil {
				s.logger.WithError(err).Error("Failed to write message")
				return
			}

		case <-ticker.C:
			if err := client.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				s.logger.WithError(err).Warn("Failed to set write deadline for ping")
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

// handleMessage processes an incoming message
func (s *ChatServer) handleMessage(client *ChatClient, msg *ChatMessage) {
	ctx := context.Background()

	// Check rate limit
	if !s.checkRateLimit(ctx, client.UserID, client.StreamID) {
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   "Rate limit exceeded. Please slow down.",
			Timestamp: time.Now(),
		})
		return
	}

	// Check if user is still not banned (ban could happen mid-stream)
	banned, err := s.chatRepo.IsUserBanned(ctx, client.StreamID, client.UserID)
	if err != nil {
		s.logger.WithError(err).Error("Failed to check ban status")
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

	// Create domain message
	domainMsg := domain.NewChatMessage(
		client.StreamID,
		client.UserID,
		client.Username,
		msg.Message,
	)

	// Validate message
	if err := domainMsg.Validate(); err != nil {
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   err.Error(),
			Timestamp: time.Now(),
		})
		return
	}

	// Save to database
	if err := s.chatRepo.CreateMessage(ctx, domainMsg); err != nil {
		s.logger.WithError(err).Error("Failed to save message")
		s.sendToClient(client, &ChatMessage{
			Type:      "error",
			StreamID:  client.StreamID,
			Message:   "Failed to send message",
			Timestamp: time.Now(),
		})
		return
	}

	// Broadcast to all clients
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

// broadcast sends a message to all clients in a stream
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
			// Client's send buffer is full, skip
			s.logger.WithFields(logrus.Fields{
				"stream_id": streamID,
				"user_id":   client.UserID,
			}).Warn("Client send buffer full, dropping message")
		}
	}
}

// broadcastSystemMessage sends a system message to all clients
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

// sendToClient sends a message to a specific client
func (s *ChatServer) sendToClient(client *ChatClient, message *ChatMessage) {
	select {
	case client.send <- message:
	default:
		s.logger.WithFields(logrus.Fields{
			"stream_id": client.StreamID,
			"user_id":   client.UserID,
		}).Warn("Failed to send message to client, buffer full")
	}
}

// checkRateLimit checks if user has exceeded rate limit
func (s *ChatServer) checkRateLimit(ctx context.Context, userID, streamID uuid.UUID) bool {
	// Check if user is moderator (moderators get higher limits)
	isMod, err := s.chatRepo.IsModerator(ctx, streamID, userID)
	if err != nil {
		s.logger.WithError(err).Error("Failed to check moderator status")
	}

	// Different limits for moderators
	limit := 5
	window := 10 * time.Second
	if isMod {
		limit = 10
		window = 10 * time.Second
	}

	key := fmt.Sprintf("chat:ratelimit:%s:%s", streamID, userID)

	// Use Redis for rate limiting (sliding window)
	pipe := s.redis.Pipeline()
	now := time.Now().Unix()

	// Remove old entries
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", now-int64(window.Seconds())))

	// Count current entries
	countCmd := pipe.ZCard(ctx, key)

	// Add current timestamp
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})

	// Set expiration
	pipe.Expire(ctx, key, window)

	_, err = pipe.Exec(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to check rate limit")
		return true // Allow on error
	}

	count := countCmd.Val()
	return count < int64(limit)
}

// DeleteMessage deletes a message (moderator action)
func (s *ChatServer) DeleteMessage(ctx context.Context, streamID, messageID uuid.UUID, moderatorID uuid.UUID) error {
	// Check if user is moderator or stream owner
	isMod, err := s.chatRepo.IsModerator(ctx, streamID, moderatorID)
	if err != nil {
		return fmt.Errorf("failed to check moderator status: %w", err)
	}

	// Check if moderatorID is stream owner
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

	// Verify message exists and belongs to this stream
	msg, err := s.chatRepo.GetMessageByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}
	if msg.StreamID != streamID {
		return fmt.Errorf("message does not belong to this stream")
	}

	// Delete the message
	if err := s.chatRepo.DeleteMessage(ctx, messageID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	// Broadcast deletion to all clients
	wsMsg := &ChatMessage{
		Type:      "delete",
		ID:        messageID,
		StreamID:  streamID,
		Timestamp: time.Now(),
	}
	s.broadcast(streamID, wsMsg)

	s.logger.WithFields(logrus.Fields{
		"stream_id":    streamID,
		"message_id":   messageID,
		"moderator_id": moderatorID,
	}).Info("Message deleted by moderator")

	return nil
}

// BanUser bans a user from chat (moderator action)
func (s *ChatServer) BanUser(ctx context.Context, streamID, userID, moderatorID uuid.UUID, reason string, duration time.Duration) error {
	// Check if user is moderator or stream owner
	isMod, err := s.chatRepo.IsModerator(ctx, streamID, moderatorID)
	if err != nil {
		return fmt.Errorf("failed to check moderator status: %w", err)
	}

	// Check if moderatorID is stream owner
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

	// Create ban
	ban := domain.NewChatBan(streamID, userID, moderatorID, reason, duration)
	if err := s.chatRepo.BanUser(ctx, ban); err != nil {
		return fmt.Errorf("failed to ban user: %w", err)
	}

	// Disconnect the user if they're connected
	s.disconnectUser(streamID, userID)

	// Broadcast ban message
	banType := "timeout"
	if duration == 0 {
		banType = "ban"
	}

	wsMsg := &ChatMessage{
		Type:      banType,
		StreamID:  streamID,
		UserID:    userID,
		Message:   reason,
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"duration": duration.Seconds(),
		},
	}
	s.broadcast(streamID, wsMsg)

	s.logger.WithFields(logrus.Fields{
		"stream_id":    streamID,
		"user_id":      userID,
		"moderator_id": moderatorID,
		"duration":     duration,
		"reason":       reason,
	}).Info("User banned from chat")

	return nil
}

// disconnectUser forcibly disconnects a user from chat
func (s *ChatServer) disconnectUser(streamID, userID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clients, ok := s.connections[streamID]
	if !ok {
		return
	}

	for client := range clients {
		if client.UserID == userID {
			// Send disconnect message
			client.send <- &ChatMessage{
				Type:      "disconnect",
				StreamID:  streamID,
				Message:   "You have been disconnected from chat",
				Timestamp: time.Now(),
			}

			// Close connection
			if err := client.conn.Close(); err != nil {
				s.logger.WithError(err).Debug("Failed to close connection when disconnecting user")
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

// GetConnectedUsers returns the count of connected users for a stream
func (s *ChatServer) GetConnectedUsers(streamID uuid.UUID) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clients, ok := s.connections[streamID]
	if !ok {
		return 0
	}

	return len(clients)
}

// Shutdown gracefully shuts down the chat server
func (s *ChatServer) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down chat server...")

	// Signal shutdown
	close(s.shutdownChan)

	// Disconnect all clients
	s.mu.Lock()
	for streamID, clients := range s.connections {
		for client := range clients {
			if err := client.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "Server shutting down"),
			); err != nil {
				s.logger.WithError(err).Debug("Failed to write shutdown message")
			}
			if err := client.conn.Close(); err != nil {
				s.logger.WithError(err).Debug("Failed to close connection during shutdown")
			}
		}
		delete(s.connections, streamID)
	}
	s.mu.Unlock()

	// Wait for all goroutines with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("Chat server shutdown complete")
		return nil
	case <-ctx.Done():
		s.logger.Warn("Chat server shutdown timeout")
		return ctx.Err()
	}
}

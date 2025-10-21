package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/chat"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/usecase"
)

// ChatHandlers handles chat-related HTTP requests
type ChatHandlers struct {
	chatServer *chat.ChatServer
	chatRepo   repository.ChatRepository
	streamRepo repository.LiveStreamRepository
	userRepo   usecase.UserRepository
}

// NewChatHandlers creates new chat handlers
func NewChatHandlers(
	chatServer *chat.ChatServer,
	chatRepo repository.ChatRepository,
	streamRepo repository.LiveStreamRepository,
	userRepo usecase.UserRepository,
) *ChatHandlers {
	return &ChatHandlers{
		chatServer: chatServer,
		chatRepo:   chatRepo,
		streamRepo: streamRepo,
		userRepo:   userRepo,
	}
}

// RegisterRoutes registers chat routes
func (h *ChatHandlers) RegisterRoutes(r chi.Router) {
	r.Route("/streams/{streamId}/chat", func(r chi.Router) {
		// WebSocket endpoint (requires authentication)
		r.With(middleware.RequireAuth).Get("/ws", h.HandleWebSocketConnection)

		// Chat history (public for public streams, auth for private)
		r.Get("/messages", h.GetChatMessages)

		// Moderation endpoints (require authentication and moderator role)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth)

			// Delete message
			r.Delete("/messages/{messageId}", h.DeleteMessage)

			// Moderator management
			r.Post("/moderators", h.AddModerator)
			r.Delete("/moderators/{userId}", h.RemoveModerator)
			r.Get("/moderators", h.GetModerators)

			// Ban management
			r.Post("/bans", h.BanUser)
			r.Delete("/bans/{userId}", h.UnbanUser)
			r.Get("/bans", h.GetBans)

			// Statistics
			r.Get("/stats", h.GetChatStats)
		})
	})
}

// HandleWebSocketConnection upgrades HTTP to WebSocket for chat
func (h *ChatHandlers) HandleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID from URL
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get authenticated user
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Get user details
	user, err := h.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get user details"))
		return
	}

	// Verify stream exists and is live
	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	if stream.Status != "live" {
		WriteError(w, http.StatusBadRequest, errors.New("stream is not live"))
		return
	}

	// Check if chat is enabled for this stream
	// TODO: Add chat_enabled field to live_streams table

	// Upgrade to WebSocket
	conn, err := h.chatServer.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already writes response
		return
	}

	// Handle the WebSocket connection
	if err := h.chatServer.HandleWebSocket(ctx, conn, streamID, userID, user.Username); err != nil {
		// Connection already closed by HandleWebSocket
		return
	}
}

// GetChatMessages retrieves chat message history
func (h *ChatHandlers) GetChatMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Verify stream exists
	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	// Check privacy (if private, require authentication and authorization)
	if stream.Privacy == "private" {
		userID, authenticated := middleware.GetUserIDFromContext(ctx)
		if !authenticated {
			WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}

		// Check if user is owner or has access
		if stream.UserID != userID {
			// TODO: Check if user is subscriber or has been granted access
			WriteError(w, http.StatusForbidden, errors.New("access denied"))
			return
		}
	}

	// Parse pagination parameters
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	// Get messages
	messages, err := h.chatRepo.GetMessages(ctx, streamID, limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get messages"))
		return
	}

	// Get total count
	totalCount, err := h.chatRepo.GetMessageCount(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get message count"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
		"pagination": map[string]interface{}{
			"limit":  limit,
			"offset": offset,
			"total":  totalCount,
		},
	})
}

// DeleteMessage deletes a chat message (moderator action)
func (h *ChatHandlers) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID and message ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	messageIDStr := chi.URLParam(r, "messageId")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid message ID"))
		return
	}

	// Get authenticated user
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Delete the message (this checks permissions internally)
	if err := h.chatServer.DeleteMessage(ctx, streamID, messageID, userID); err != nil {
		if err == domain.ErrNotModerator {
			WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
			return
		}
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, errors.New("message not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to delete message"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Message deleted successfully",
	})
}

// AddModeratorRequest is the request body for adding a moderator
type AddModeratorRequest struct {
	UserID string `json:"user_id"`
}

// AddModerator adds a moderator to a stream chat
func (h *ChatHandlers) AddModerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get authenticated user (must be stream owner)
	ownerID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Verify stream ownership
	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusNotFound, errors.New("stream not found"))
		return
	}

	if stream.UserID != ownerID {
		WriteError(w, http.StatusForbidden, errors.New("only stream owner can add moderators"))
		return
	}

	// Parse request
	var req AddModeratorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	modUserID, err := uuid.Parse(req.UserID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	// Verify user exists
	_, err = h.userRepo.GetByID(ctx, modUserID.String())
	if err != nil {
		WriteError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	// Add moderator
	moderator := domain.NewChatModerator(streamID, modUserID, ownerID)
	if err := h.chatRepo.AddModerator(ctx, moderator); err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to add moderator"))
		return
	}

	WriteJSON(w, http.StatusOK, moderator)
}

// RemoveModerator removes a moderator from a stream chat
func (h *ChatHandlers) RemoveModerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get user ID to remove
	userIDStr := chi.URLParam(r, "userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	// Get authenticated user (must be stream owner)
	ownerID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Verify stream ownership
	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusNotFound, errors.New("stream not found"))
		return
	}

	if stream.UserID != ownerID {
		WriteError(w, http.StatusForbidden, errors.New("only stream owner can remove moderators"))
		return
	}

	// Remove moderator
	if err := h.chatRepo.RemoveModerator(ctx, streamID, userID); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, errors.New("moderator not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to remove moderator"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Moderator removed successfully",
	})
}

// GetModerators gets all moderators for a stream
func (h *ChatHandlers) GetModerators(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get moderators
	moderators, err := h.chatRepo.GetModerators(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get moderators"))
		return
	}

	WriteJSON(w, http.StatusOK, moderators)
}

// BanUserRequest is the request body for banning a user
type BanUserRequest struct {
	UserID   string `json:"user_id"`
	Reason   string `json:"reason"`
	Duration int    `json:"duration"` // Duration in seconds, 0 for permanent
}

// BanUser bans a user from chat
func (h *ChatHandlers) BanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get authenticated user (moderator)
	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Parse request
	var req BanUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	// Convert duration to time.Duration
	var duration time.Duration
	if req.Duration > 0 {
		duration = time.Duration(req.Duration) * time.Second
	}

	// Ban the user (this checks permissions internally)
	if err := h.chatServer.BanUser(ctx, streamID, userID, moderatorID, req.Reason, duration); err != nil {
		if err == domain.ErrNotModerator {
			WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to ban user"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"message": "User banned successfully",
	})
}

// UnbanUser unbans a user from chat
func (h *ChatHandlers) UnbanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get user ID to unban
	userIDStr := chi.URLParam(r, "userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	// Get authenticated user (moderator)
	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Check if user is moderator or stream owner
	isMod, err := h.chatRepo.IsModerator(ctx, streamID, moderatorID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to check moderator status"))
		return
	}

	// TODO: Also check if moderatorID is stream owner
	if !isMod {
		WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
		return
	}

	// Unban the user
	if err := h.chatRepo.UnbanUser(ctx, streamID, userID); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, errors.New("ban not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("failed to unban user"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"message": "User unbanned successfully",
	})
}

// GetBans gets all bans for a stream
func (h *ChatHandlers) GetBans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get authenticated user (moderator)
	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	// Check if user is moderator or stream owner
	isMod, err := h.chatRepo.IsModerator(ctx, streamID, moderatorID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to check moderator status"))
		return
	}

	// TODO: Also check if moderatorID is stream owner
	if !isMod {
		WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
		return
	}

	// Get bans
	bans, err := h.chatRepo.GetBans(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get bans"))
		return
	}

	WriteJSON(w, http.StatusOK, bans)
}

// GetChatStats gets chat statistics for a stream
func (h *ChatHandlers) GetChatStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get chat statistics
	stats, err := h.chatRepo.GetStreamStats(ctx, streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("failed to get chat stats"))
		return
	}

	// Get connected users count
	connectedUsers := h.chatServer.GetConnectedUsers(streamID)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"stats":           stats,
		"connected_users": connectedUsers,
	})
}

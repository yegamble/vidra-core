package messaging

import (
	"athena/internal/httpapi/shared"
	"context"
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
	"athena/internal/port"
	"athena/internal/repository"
	"athena/internal/usecase"
)

type ChatHandlers struct {
	chatServer       *chat.ChatServer
	chatRepo         repository.ChatRepository
	streamRepo       repository.LiveStreamRepository
	userRepo         usecase.UserRepository
	subscriptionRepo port.SubscriptionRepository
}

func NewChatHandlers(
	chatServer *chat.ChatServer,
	chatRepo repository.ChatRepository,
	streamRepo repository.LiveStreamRepository,
	userRepo usecase.UserRepository,
	subscriptionRepo port.SubscriptionRepository,
) *ChatHandlers {
	return &ChatHandlers{
		chatServer:       chatServer,
		chatRepo:         chatRepo,
		streamRepo:       streamRepo,
		userRepo:         userRepo,
		subscriptionRepo: subscriptionRepo,
	}
}

func (h *ChatHandlers) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/streams/{streamId}/chat", func(r chi.Router) {
		r.With(middleware.Auth(jwtSecret)).Get("/ws", h.HandleWebSocketConnection)

		r.Get("/messages", h.GetChatMessages)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(jwtSecret))

			r.Delete("/messages/{messageId}", h.DeleteMessage)

			r.Post("/moderators", h.AddModerator)
			r.Delete("/moderators/{userId}", h.RemoveModerator)
			r.Get("/moderators", h.GetModerators)

			r.Post("/bans", h.BanUser)
			r.Delete("/bans/{userId}", h.UnbanUser)
			r.Get("/bans", h.GetBans)

			r.Get("/stats", h.GetChatStats)
		})
	})
}

func (h *ChatHandlers) HandleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	user, err := h.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get user details"))
		return
	}

	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	if stream.Status != "live" {
		shared.WriteError(w, http.StatusBadRequest, errors.New("stream is not live"))
		return
	}

	if !stream.ChatEnabled {
		shared.WriteError(w, http.StatusForbidden, errors.New("chat is disabled for this stream"))
		return
	}

	conn, err := h.chatServer.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	if err := h.chatServer.HandleWebSocket(ctx, conn, streamID, userID, user.Username); err != nil {
		return
	}
}

func (h *ChatHandlers) GetChatMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	if stream.Privacy == "private" {
		userID, authenticated := middleware.GetUserIDFromContext(ctx)
		if !authenticated {
			shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}

		if stream.UserID != userID {
			isSubscribed, err := h.subscriptionRepo.IsSubscribed(ctx, userID, stream.ChannelID)
			if err != nil {
				shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to check subscription status"))
				return
			}
			if !isSubscribed {
				shared.WriteError(w, http.StatusForbidden, errors.New("access denied"))
				return
			}
		}
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	messages, err := h.chatRepo.GetMessages(ctx, streamID, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get messages"))
		return
	}

	totalCount, err := h.chatRepo.GetMessageCount(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get message count"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
		"pagination": map[string]interface{}{
			"limit":  limit,
			"offset": offset,
			"total":  totalCount,
		},
	})
}

func (h *ChatHandlers) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	messageIDStr := chi.URLParam(r, "messageId")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid message ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	if err := h.chatServer.DeleteMessage(ctx, streamID, messageID, userID); err != nil {
		if err == domain.ErrNotModerator {
			shared.WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
			return
		}
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("message not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to delete message"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Message deleted successfully",
	})
}

type AddModeratorRequest struct {
	UserID string `json:"user_id"`
}

func (h *ChatHandlers) AddModerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	ownerID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
		return
	}

	if stream.UserID != ownerID {
		shared.WriteError(w, http.StatusForbidden, errors.New("only stream owner can add moderators"))
		return
	}

	var req AddModeratorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	modUserID, err := uuid.Parse(req.UserID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	_, err = h.userRepo.GetByID(ctx, modUserID.String())
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("user not found"))
		return
	}

	moderator := domain.NewChatModerator(streamID, modUserID, ownerID)
	if err := h.chatRepo.AddModerator(ctx, moderator); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to add moderator"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, moderator)
}

func (h *ChatHandlers) RemoveModerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	userIDStr := chi.URLParam(r, "userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	ownerID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
		return
	}

	if stream.UserID != ownerID {
		shared.WriteError(w, http.StatusForbidden, errors.New("only stream owner can remove moderators"))
		return
	}

	if err := h.chatRepo.RemoveModerator(ctx, streamID, userID); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("moderator not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to remove moderator"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Moderator removed successfully",
	})
}

func (h *ChatHandlers) GetModerators(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	moderators, err := h.chatRepo.GetModerators(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get moderators"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, moderators)
}

type BanUserRequest struct {
	UserID   string `json:"user_id"`
	Reason   string `json:"reason"`
	Duration int    `json:"duration"`
}

func (h *ChatHandlers) BanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	var req BanUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	var duration time.Duration
	if req.Duration > 0 {
		duration = time.Duration(req.Duration) * time.Second
	}

	if err := h.chatServer.BanUser(ctx, streamID, userID, moderatorID, req.Reason, duration); err != nil {
		if err == domain.ErrNotModerator {
			shared.WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to ban user"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "User banned successfully",
	})
}

func (h *ChatHandlers) UnbanUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	userIDStr := chi.URLParam(r, "userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid user ID"))
		return
	}

	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	if !h.verifyModeratorOrOwner(w, ctx, streamID, moderatorID) {
		return
	}

	if err := h.chatRepo.UnbanUser(ctx, streamID, userID); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("ban not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to unban user"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "User unbanned successfully",
	})
}

func (h *ChatHandlers) GetBans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	moderatorID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	if !h.verifyModeratorOrOwner(w, ctx, streamID, moderatorID) {
		return
	}

	bans, err := h.chatRepo.GetBans(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get bans"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, bans)
}

func (h *ChatHandlers) GetChatStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	stats, err := h.chatRepo.GetStreamStats(ctx, streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get chat stats"))
		return
	}

	connectedUsers := h.chatServer.GetConnectedUsers(streamID)

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"stats":           stats,
		"connected_users": connectedUsers,
	})
}

func (h *ChatHandlers) verifyModeratorOrOwner(w http.ResponseWriter, ctx context.Context, streamID, userID uuid.UUID) bool {
	isMod, err := h.chatRepo.IsModerator(ctx, streamID, userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to check moderator status"))
		return false
	}

	if isMod {
		return true
	}

	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return false
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return false
	}

	if stream.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, errors.New("moderator privileges required"))
		return false
	}

	return true
}

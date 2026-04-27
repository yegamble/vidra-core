package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"vidra-core/internal/httpapi/shared"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/chat"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
	"vidra-core/internal/repository"
	"vidra-core/internal/usecase"
)

type ChatHandlers struct {
	chatServer       *chat.ChatServer
	chatRepo         repository.ChatRepository
	streamRepo       repository.LiveStreamRepository
	userRepo         usecase.UserRepository
	subscriptionRepo port.SubscriptionRepository
	invoiceRepo      InvoiceLookup
}

// InvoiceLookup is the subset of BTCPayRepository the chat handler needs to validate a
// tip-mediated system-message broadcast. Defined as an interface for test isolation.
type InvoiceLookup interface {
	GetInvoiceByID(ctx context.Context, id string) (*domain.BTCPayInvoice, error)
	MarkInvoiceSystemMessageBroadcast(ctx context.Context, invoiceID string) error
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

// SetInvoiceRepo wires the BTCPay invoice repo. Optional — when nil, the tip-system-message
// route is unavailable (handler returns 503 in that case).
func (h *ChatHandlers) SetInvoiceRepo(repo InvoiceLookup) { h.invoiceRepo = repo }

func (h *ChatHandlers) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/streams/{streamId}/chat", func(r chi.Router) {
		r.With(middleware.WSAuth(jwtSecret)).Get("/ws", h.HandleWebSocketConnection)

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

			r.Put("/slow-mode", h.SetSlowMode)

			r.Post("/system-message", h.BroadcastTipSystemMessage)

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

	if err := h.chatServer.BanUser(ctx, chat.BanRequest{
		StreamID: streamID, UserID: userID, ModeratorID: moderatorID, Reason: req.Reason, Duration: duration,
	}); err != nil {
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

// SlowModeRequest is the body for PUT /streams/{id}/chat/slow-mode.
type SlowModeRequest struct {
	Seconds int `json:"seconds"`
}

const slowModeMaxSeconds = 600

// SetSlowMode updates the per-stream slow-mode interval. Mod-or-owner only.
//   PUT /api/v1/streams/{streamId}/chat/slow-mode
//   Body: {"seconds": int}    // 0 disables; capped at 600 (10 minutes).
func (h *ChatHandlers) SetSlowMode(w http.ResponseWriter, r *http.Request) {
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

	var req SlowModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if req.Seconds < 0 {
		shared.WriteError(w, http.StatusBadRequest, errors.New("seconds must be >= 0"))
		return
	}
	if req.Seconds > slowModeMaxSeconds {
		shared.WriteError(w, http.StatusBadRequest, errors.New("seconds must be <= 600"))
		return
	}

	if err := h.streamRepo.SetSlowMode(ctx, streamID, req.Seconds); err != nil {
		if err == domain.ErrNotFound || err == domain.ErrStreamNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to set slow mode"))
		return
	}

	h.chatServer.BroadcastSlowModeChange(streamID, req.Seconds)

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"slow_mode_seconds": req.Seconds,
	})
}

// BroadcastTipSystemMessageRequest carries the invoice id whose successful settlement gives
// the caller a one-shot right to publish a "💛 X tipped Y sat" system message into the live
// chat of the stream identified in the URL.
type BroadcastTipSystemMessageRequest struct {
	InvoiceID string `json:"invoice_id"`
}

// BroadcastTipSystemMessage validates that the caller's invoice was settled, that it targeted
// THIS stream's channel, and that it has not been previously used to broadcast a system
// message. On success, marks the invoice and dispatches a system message to the chat hub.
//   POST /api/v1/streams/{streamId}/chat/system-message
//   Body: {"invoice_id": "<uuid>"}
//   200 OK on success; 400/403/404/409/503 on validation failures.
func (h *ChatHandlers) BroadcastTipSystemMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.invoiceRepo == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, errors.New("invoice repo not configured"))
		return
	}

	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	callerID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}

	var req BroadcastTipSystemMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	invoice, err := h.invoiceRepo.GetInvoiceByID(ctx, req.InvoiceID)
	if err != nil {
		if err == domain.ErrInvoiceNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("invoice not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to look up invoice"))
		return
	}

	// Trust check #1: invoice belongs to the caller.
	if invoice.UserID != callerID.String() {
		shared.WriteError(w, http.StatusForbidden, errors.New("invoice does not belong to caller"))
		return
	}

	// Trust check #2: invoice is settled.
	if invoice.Status != domain.InvoiceStatusSettled {
		shared.WriteError(w, http.StatusConflict, domain.ErrInvoiceUnsettled)
		return
	}

	// Trust check #3: invoice's destination channel matches this stream's channel. Pulled
	// from the invoice metadata blob (where the tip flow records `{type:"tip",
	// channel_id:"..."}`).
	stream, err := h.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		if err == domain.ErrNotFound || err == domain.ErrStreamNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to look up stream"))
		return
	}

	channelID, ok := tipChannelFromInvoice(invoice)
	if !ok {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invoice metadata is not a tip"))
		return
	}
	if channelID != stream.ChannelID.String() {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invoice channel does not match stream"))
		return
	}

	// Trust check #4: single-use. The repo's conditional UPDATE returns
	// ErrInvoiceAlreadyBroadcast when the marker is already set.
	if err := h.invoiceRepo.MarkInvoiceSystemMessageBroadcast(ctx, invoice.ID); err != nil {
		if err == domain.ErrInvoiceAlreadyBroadcast {
			shared.WriteError(w, http.StatusConflict, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to mark invoice broadcast"))
		return
	}

	// Resolve the caller's username for the system-message text.
	caller, err := h.userRepo.GetByID(ctx, callerID.String())
	username := callerID.String()
	if err == nil && caller != nil && caller.Username != "" {
		username = caller.Username
	}

	formatted := fmt.Sprintf("💛 %s tipped %d sat", username, invoice.AmountSats)
	h.chatServer.BroadcastSystemMessage(streamID, formatted)

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"broadcast": true,
		"message":   formatted,
	})
}

// tipChannelFromInvoice extracts the destination channel id from a tip invoice's metadata
// blob. Returns ok=false when the invoice is not a tip or the field is missing.
func tipChannelFromInvoice(invoice *domain.BTCPayInvoice) (string, bool) {
	if len(invoice.Metadata) == 0 {
		return "", false
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(invoice.Metadata, &meta); err != nil {
		return "", false
	}
	if t, _ := meta["type"].(string); t != "tip" {
		return "", false
	}
	channelID, _ := meta["channel_id"].(string)
	if channelID == "" {
		return "", false
	}
	return channelID, true
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

package messaging

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"vidra-core/internal/usecase/message"
)

// MessagesWSHandler upgrades the HTTP request and registers the resulting connection with the
// hub. It is gated by middleware.WSAuth (registered in routes.go) so by the time we get here,
// the request context already carries UserIDKey.
type MessagesWSHandler struct {
	hub *message.WSHub
}

// NewMessagesWSHandler builds the upgrade handler.
func NewMessagesWSHandler(hub *message.WSHub) *MessagesWSHandler {
	return &MessagesWSHandler{hub: hub}
}

// ServeHTTP implements the WS upgrade. It expects UserIDKey in the request context; without
// it, returns 401 (defensive — middleware.WSAuth should have rejected first).
func (h *MessagesWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userIDStr := getUserID(r.Context())
	if userIDStr == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		http.Error(w, "invalid user id in context", http.StatusBadRequest)
		return
	}

	conn, err := h.hub.Upgrader().Upgrade(w, r, nil)
	if err != nil {
		// Upgrader writes the response on failure.
		return
	}

	client := h.hub.Register(conn, userID)
	go h.hub.WritePump(client)
	// ReadPump runs synchronously and blocks until the connection closes. Typing resolver is
	// nil for now — the messaging service can call hub.PublishTyping directly when wired with
	// a participant-lookup helper in a follow-up.
	h.hub.ReadPump(client, defaultTypingResolver)
}

// defaultTypingResolver is a placeholder that returns no other participants. Wired later when
// participant lookup is plumbed in. Keeping it here means the hub still receives typing events
// without crashing; they're simply not rebroadcast until a real resolver lands.
func defaultTypingResolver(ctx context.Context, conversationID string, typerID uuid.UUID) ([]uuid.UUID, error) {
	_ = ctx
	_ = conversationID
	_ = typerID
	return nil, errors.New("typing rebroadcast not yet wired")
}

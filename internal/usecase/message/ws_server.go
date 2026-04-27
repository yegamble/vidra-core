package message

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	wsWriteWait        = 10 * time.Second
	wsPongWait         = 60 * time.Second
	wsPingPeriod       = (wsPongWait * 9) / 10
	wsMaxMessageSize   = 4 * 1024
	wsClientSendBuffer = 64
)

// WSEnvelope is the wire shape for messages-WS events. Unlike the chat hub (broadcast, flat
// fields per top-level type), DM events are point-to-point and may carry richer payloads
// (typing context, read-receipts, future delivery_ack/reactions). Keeping the {type, data}
// envelope future-proofs richer payloads without flattening polymorphism.
type WSEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// MessageReceivedPayload is published when a participant in the conversation receives a new
// message. ClientMessageID echoes the optimistic id supplied by the sender so multi-tab
// reconciliation is deterministic.
type MessageReceivedPayload struct {
	ID              string    `json:"id"`
	ConversationID  string    `json:"conversation_id"`
	SenderID        string    `json:"sender_id"`
	RecipientID     string    `json:"recipient_id"`
	Body            string    `json:"body"`
	Nonce           string    `json:"nonce,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ReadAt          *time.Time `json:"read_at,omitempty"`
	ClientMessageID string    `json:"client_message_id,omitempty"`
}

// MessageReadPayload signals a message has been read by the recipient.
type MessageReadPayload struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	ReaderID       string `json:"reader_id"`
}

// TypingPayload is emitted by clients and re-broadcast to the other participants of a
// conversation. We re-broadcast (and never echo back to the sender).
type TypingPayload struct {
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
}

// WSClient is one authenticated WebSocket connection.
type WSClient struct {
	hub    *WSHub
	conn   *websocket.Conn
	send   chan WSEnvelope
	UserID uuid.UUID
}

// WSHub maintains the set of authenticated user connections and dispatches publish events to
// the correct recipient(s). One user may have multiple connections (browser tabs); each gets
// every event published for that user.
type WSHub struct {
	mu          sync.RWMutex
	connections map[uuid.UUID]map[*WSClient]bool

	upgrader websocket.Upgrader
	logger   *slog.Logger

	shutdown chan struct{}
	wg       sync.WaitGroup
}

// NewWSHub builds an unstarted hub. Callers must call Run on a background goroutine if they
// need the shutdown channel; for simple deployments the hub is fully event-driven and Run is
// optional.
func NewWSHub(logger *slog.Logger, checkOrigin func(*http.Request) bool) *WSHub {
	return &WSHub{
		connections: make(map[uuid.UUID]map[*WSClient]bool),
		logger:      logger,
		shutdown:    make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     checkOrigin,
			Subprotocols:    []string{"access_token"},
		},
	}
}

// Upgrader exposes the configured upgrader so HTTP handlers can call Upgrade directly.
func (h *WSHub) Upgrader() *websocket.Upgrader { return &h.upgrader }

// Register adds a new authenticated connection. Caller must invoke ReadPump/WritePump.
func (h *WSHub) Register(conn *websocket.Conn, userID uuid.UUID) *WSClient {
	client := &WSClient{
		hub:    h,
		conn:   conn,
		send:   make(chan WSEnvelope, wsClientSendBuffer),
		UserID: userID,
	}

	h.mu.Lock()
	if h.connections[userID] == nil {
		h.connections[userID] = make(map[*WSClient]bool)
	}
	h.connections[userID][client] = true
	h.mu.Unlock()

	if h.logger != nil {
		h.logger.Debug("dm-ws client registered", "user_id", userID.String())
	}
	return client
}

// Unregister removes a client and closes its send channel. Idempotent.
func (h *WSHub) Unregister(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.connections[client.UserID]; ok {
		if _, exists := clients[client]; exists {
			delete(clients, client)
			close(client.send)
			if len(clients) == 0 {
				delete(h.connections, client.UserID)
			}
		}
	}
}

// publishTo sends an envelope to every connection registered for userID. Slow consumers are
// dropped silently — the hub guarantees liveness, not delivery (clients reconcile on reconnect).
func (h *WSHub) publishTo(userID uuid.UUID, env WSEnvelope) {
	h.mu.RLock()
	clients := h.connections[userID]
	targets := make([]*WSClient, 0, len(clients))
	for c := range clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- env:
		default:
			if h.logger != nil {
				h.logger.Warn("dm-ws send buffer full, dropping", "user_id", userID.String())
			}
		}
	}
}

// PublishMessageReceived emits message_received to BOTH sender and recipient (so multi-tab
// senders see their own message appear via the same path that recipients use).
func (h *WSHub) PublishMessageReceived(senderID, recipientID uuid.UUID, payload MessageReceivedPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("dm-ws marshal message_received", "err", err)
		}
		return
	}
	env := WSEnvelope{Type: "message_received", Data: data}
	h.publishTo(recipientID, env)
	if senderID != recipientID {
		h.publishTo(senderID, env)
	}
}

// PublishMessageRead notifies the SENDER of the message that the recipient has read it.
func (h *WSHub) PublishMessageRead(senderID uuid.UUID, payload MessageReadPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("dm-ws marshal message_read", "err", err)
		}
		return
	}
	h.publishTo(senderID, WSEnvelope{Type: "message_read", Data: data})
}

// PublishTyping forwards typing events from one participant to the other(s) in the conversation.
// otherUserIDs is the set of conversation participants OTHER than the typer.
func (h *WSHub) PublishTyping(otherUserIDs []uuid.UUID, payload TypingPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("dm-ws marshal typing", "err", err)
		}
		return
	}
	env := WSEnvelope{Type: "typing", Data: data}
	for _, uid := range otherUserIDs {
		h.publishTo(uid, env)
	}
}

// ConnectionCount returns the number of unique users with at least one open connection. For
// metrics / tests.
func (h *WSHub) ConnectionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// Shutdown closes the shutdown channel and waits for in-flight pumps to drain. Safe to call
// once; subsequent calls are no-ops.
func (h *WSHub) Shutdown(ctx context.Context) error {
	select {
	case <-h.shutdown:
		return nil
	default:
		close(h.shutdown)
	}
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReadPump runs the inbound loop on a client connection. Caller must invoke as a goroutine
// AFTER Register. Exits when the connection closes or shutdown is signalled. typingResolver,
// when non-nil, returns the set of OTHER participants for a given conversationID — used to
// rebroadcast typing events.
func (h *WSHub) ReadPump(client *WSClient, typingResolver func(ctx context.Context, conversationID string, typerID uuid.UUID) ([]uuid.UUID, error)) {
	h.wg.Add(1)
	defer func() {
		h.wg.Done()
		h.Unregister(client)
		_ = client.conn.Close()
	}()

	client.conn.SetReadLimit(wsMaxMessageSize)
	_ = client.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	client.conn.SetPongHandler(func(string) error {
		_ = client.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		select {
		case <-h.shutdown:
			return
		default:
		}

		var env WSEnvelope
		if err := client.conn.ReadJSON(&env); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				if h.logger != nil {
					h.logger.Debug("dm-ws read error", "err", err)
				}
			}
			return
		}

		if env.Type == "typing" && typingResolver != nil {
			var p TypingPayload
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			others, err := typingResolver(context.Background(), p.ConversationID, client.UserID)
			if err != nil {
				continue
			}
			// Force the user_id field so a malicious client can't impersonate someone else.
			p.UserID = client.UserID.String()
			h.PublishTyping(others, p)
		}
		// All other inbound types are ignored — the hub is push-only for receive/read events.
	}
}

// WritePump runs the outbound loop on a client connection. Caller must invoke as a goroutine
// AFTER Register.
func (h *WSHub) WritePump(client *WSClient) {
	h.wg.Add(1)
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		h.wg.Done()
		ticker.Stop()
		_ = client.conn.Close()
	}()

	for {
		select {
		case env, ok := <-client.send:
			_ = client.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.conn.WriteJSON(env); err != nil {
				if h.logger != nil {
					h.logger.Debug("dm-ws write error", "err", err)
				}
				return
			}
		case <-ticker.C:
			_ = client.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-h.shutdown:
			return
		}
	}
}

// ErrHubShutdown is returned when the hub is shutting down and refuses new operations.
var ErrHubShutdown = errors.New("dm-ws hub shutting down")

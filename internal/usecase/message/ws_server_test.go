package message

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeClient captures envelopes that the hub publishes to its send channel. We don't run a
// real WebSocket here — the hub's pub/sub paths are exercised through the channel so we don't
// need network plumbing in unit tests.
type fakeClient struct {
	userID uuid.UUID
	got    chan WSEnvelope
}

// directRegister bypasses the conn-bound Register and inserts a fake-channel client. This is
// the unit-test seam — we want to assert publish routing, not the WebSocket upgrade.
func directRegister(h *WSHub, userID uuid.UUID) (*WSClient, *fakeClient) {
	got := make(chan WSEnvelope, 8)
	client := &WSClient{
		hub:    h,
		send:   make(chan WSEnvelope, wsClientSendBuffer),
		UserID: userID,
	}
	h.mu.Lock()
	if h.connections[userID] == nil {
		h.connections[userID] = make(map[*WSClient]bool)
	}
	h.connections[userID][client] = true
	h.mu.Unlock()

	// Drain the client's send channel into the fake's got channel so tests can assert.
	go func() {
		for env := range client.send {
			got <- env
		}
		close(got)
	}()

	return client, &fakeClient{userID: userID, got: got}
}

// receiveOne waits for an envelope of the given type, with timeout. Returns the raw payload.
func (f *fakeClient) receiveOne(t *testing.T, wantType string) json.RawMessage {
	t.Helper()
	select {
	case env := <-f.got:
		if env.Type != wantType {
			t.Fatalf("expected type %q, got %q", wantType, env.Type)
		}
		return env.Data
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for envelope type %q", wantType)
		return nil
	}
}

// expectNothing asserts no envelope arrives within a short window.
func (f *fakeClient) expectNothing(t *testing.T) {
	t.Helper()
	select {
	case env := <-f.got:
		t.Fatalf("expected no envelope, got %+v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWSHub_PublishMessageReceivedRoutesToSenderAndRecipient(t *testing.T) {
	hub := NewWSHub(nil, nil)

	senderID := uuid.New()
	recipientID := uuid.New()
	otherID := uuid.New()

	_, sender := directRegister(hub, senderID)
	_, recipient := directRegister(hub, recipientID)
	_, other := directRegister(hub, otherID)

	hub.PublishMessageReceived(senderID, recipientID, MessageReceivedPayload{
		ID:             "msg-1",
		ConversationID: "conv-1",
		SenderID:       senderID.String(),
		RecipientID:    recipientID.String(),
		Body:           "hi",
		CreatedAt:      time.Now(),
	})

	senderRaw := sender.receiveOne(t, "message_received")
	recipientRaw := recipient.receiveOne(t, "message_received")
	other.expectNothing(t)

	var senderPayload, recipientPayload MessageReceivedPayload
	if err := json.Unmarshal(senderRaw, &senderPayload); err != nil {
		t.Fatalf("unmarshal sender payload: %v", err)
	}
	if err := json.Unmarshal(recipientRaw, &recipientPayload); err != nil {
		t.Fatalf("unmarshal recipient payload: %v", err)
	}
	if senderPayload.ID != "msg-1" || recipientPayload.ID != "msg-1" {
		t.Fatalf("expected both payloads to carry msg-1, got sender=%q recipient=%q",
			senderPayload.ID, recipientPayload.ID)
	}
	if senderPayload.Body != "hi" {
		t.Fatalf("expected body 'hi', got %q", senderPayload.Body)
	}
}

func TestWSHub_PublishMessageReadOnlyToSender(t *testing.T) {
	hub := NewWSHub(nil, nil)

	senderID := uuid.New()
	recipientID := uuid.New()

	_, sender := directRegister(hub, senderID)
	_, recipient := directRegister(hub, recipientID)

	hub.PublishMessageRead(senderID, MessageReadPayload{
		MessageID:      "msg-1",
		ConversationID: "conv-1",
		ReaderID:       recipientID.String(),
	})

	senderRaw := sender.receiveOne(t, "message_read")
	recipient.expectNothing(t)

	var p MessageReadPayload
	if err := json.Unmarshal(senderRaw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.ReaderID != recipientID.String() {
		t.Fatalf("expected reader_id %q, got %q", recipientID.String(), p.ReaderID)
	}
}

func TestWSHub_PublishTypingRoutesToOthersOnly(t *testing.T) {
	hub := NewWSHub(nil, nil)

	a := uuid.New()
	b := uuid.New()
	c := uuid.New()

	_, aClient := directRegister(hub, a)
	_, bClient := directRegister(hub, b)
	_, cClient := directRegister(hub, c)

	hub.PublishTyping([]uuid.UUID{b, c}, TypingPayload{
		ConversationID: "conv-1",
		UserID:         a.String(),
	})

	bClient.receiveOne(t, "typing")
	cClient.receiveOne(t, "typing")
	aClient.expectNothing(t)
}

func TestWSHub_MultipleConnectionsPerUser(t *testing.T) {
	hub := NewWSHub(nil, nil)

	userID := uuid.New()
	otherID := uuid.New()

	_, tab1 := directRegister(hub, userID)
	_, tab2 := directRegister(hub, userID)

	hub.PublishMessageReceived(otherID, userID, MessageReceivedPayload{
		ID:          "msg-1",
		SenderID:    otherID.String(),
		RecipientID: userID.String(),
	})

	tab1.receiveOne(t, "message_received")
	tab2.receiveOne(t, "message_received")
}

func TestWSHub_UnregisterRemovesClient(t *testing.T) {
	hub := NewWSHub(nil, nil)

	userID := uuid.New()
	otherID := uuid.New()

	client, fc := directRegister(hub, userID)
	hub.Unregister(client)
	// Drain — the goroutine that pumps client.send into fc.got will close fc.got after
	// client.send is closed by Unregister.
	for range fc.got {
	}

	if hub.ConnectionCount() != 0 {
		t.Fatalf("expected ConnectionCount 0 after unregister, got %d", hub.ConnectionCount())
	}

	// Publishing to the unregistered user should not panic.
	hub.PublishMessageReceived(otherID, userID, MessageReceivedPayload{ID: "msg-x"})
}

func TestWSHub_DropsOnFullBuffer(t *testing.T) {
	hub := NewWSHub(nil, nil)

	userID := uuid.New()
	// Register a client manually with a SMALL buffer so it overflows quickly.
	smallClient := &WSClient{
		hub:    hub,
		send:   make(chan WSEnvelope, 2),
		UserID: userID,
	}
	hub.mu.Lock()
	hub.connections[userID] = map[*WSClient]bool{smallClient: true}
	hub.mu.Unlock()

	// Don't drain the channel. Publish > 2 messages. The hub must not block; excess messages
	// are dropped.
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			hub.PublishMessageReceived(uuid.New(), userID, MessageReceivedPayload{ID: "m"})
		}
		close(done)
	}()

	select {
	case <-done:
		// Good — hub didn't block.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("hub appears to have blocked on a full client buffer")
	}
	wg.Wait()

	// Channel should hold at most cap=2 envelopes.
	if got := len(smallClient.send); got > 2 {
		t.Fatalf("expected at most 2 envelopes in channel, got %d", got)
	}
}

func TestWSHub_ShutdownIsIdempotent(t *testing.T) {
	hub := NewWSHub(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := hub.Shutdown(ctx); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := hub.Shutdown(ctx); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

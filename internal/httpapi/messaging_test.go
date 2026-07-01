package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// messagingFakeRepo is an in-memory messaging.Repository for handler tests. It
// resolves user identity/existence from the shared auth fake (mirroring the real
// JOINs), translating the auth fake's miss error to pgx.ErrNoRows so the service
// maps an unknown recipient to ErrRecipientNotFound.
type messagingFakeRepo struct {
	auth         *authFakeRepo
	convByKey    map[string]uuid.UUID
	convs        map[uuid.UUID]time.Time // conversation id -> updated_at
	participants map[uuid.UUID]map[uuid.UUID]bool
	messages     []sqlcgen.Message
}

func newMessagingFakeRepo(auth *authFakeRepo) *messagingFakeRepo {
	return &messagingFakeRepo{
		auth:         auth,
		convByKey:    map[string]uuid.UUID{},
		convs:        map[uuid.UUID]time.Time{},
		participants: map[uuid.UUID]map[uuid.UUID]bool{},
	}
}

func (f *messagingFakeRepo) GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, err := f.auth.GetUserByID(ctx, id)
	if err != nil {
		return sqlcgen.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *messagingFakeRepo) CreateConversation(_ context.Context, dmKey *string) (sqlcgen.CreateConversationRow, error) {
	if _, ok := f.convByKey[*dmKey]; ok {
		return sqlcgen.CreateConversationRow{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
	}
	id := uuid.New()
	now := time.Now()
	f.convByKey[*dmKey] = id
	f.convs[id] = now
	return sqlcgen.CreateConversationRow{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

func (f *messagingFakeRepo) GetConversationByDMKey(_ context.Context, dmKey *string) (sqlcgen.GetConversationByDMKeyRow, error) {
	id, ok := f.convByKey[*dmKey]
	if !ok {
		return sqlcgen.GetConversationByDMKeyRow{}, pgx.ErrNoRows
	}
	updated := f.convs[id]
	return sqlcgen.GetConversationByDMKeyRow{ID: id, CreatedAt: updated, UpdatedAt: updated}, nil
}

func (f *messagingFakeRepo) AddConversationParticipant(_ context.Context, a sqlcgen.AddConversationParticipantParams) error {
	if f.participants[a.ConversationID] == nil {
		f.participants[a.ConversationID] = map[uuid.UUID]bool{}
	}
	f.participants[a.ConversationID][a.UserID] = true
	return nil
}

func (f *messagingFakeRepo) IsConversationParticipant(_ context.Context, a sqlcgen.IsConversationParticipantParams) (bool, error) {
	return f.participants[a.ConversationID][a.UserID], nil
}

func (f *messagingFakeRepo) CreateMessage(_ context.Context, a sqlcgen.CreateMessageParams) (sqlcgen.Message, error) {
	m := sqlcgen.Message{
		ID: uuid.New(), ConversationID: a.ConversationID, SenderID: a.SenderID,
		Body: a.Body, CreatedAt: time.Now(),
	}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *messagingFakeRepo) TouchConversation(_ context.Context, id uuid.UUID) error {
	if _, ok := f.convs[id]; ok {
		f.convs[id] = time.Now()
	}
	return nil
}

func (f *messagingFakeRepo) ListMessages(ctx context.Context, a sqlcgen.ListMessagesParams) ([]sqlcgen.ListMessagesRow, error) {
	var rows []sqlcgen.ListMessagesRow
	for i := len(f.messages) - 1; i >= 0; i-- { // newest first
		m := f.messages[i]
		if m.ConversationID != a.ConversationID {
			continue
		}
		sender, _ := f.auth.GetUserByID(ctx, m.SenderID)
		rows = append(rows, sqlcgen.ListMessagesRow{
			ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID, Body: m.Body,
			CreatedAt: m.CreatedAt, SenderUsername: sender.Username, SenderDisplayName: sender.DisplayName,
		})
	}
	return rows, nil
}

func (f *messagingFakeRepo) ListConversations(ctx context.Context, a sqlcgen.ListConversationsParams) ([]sqlcgen.ListConversationsRow, error) {
	var rows []sqlcgen.ListConversationsRow
	for id, updated := range f.convs {
		if !f.participants[id][a.UserID] {
			continue
		}
		var other uuid.UUID
		for uid := range f.participants[id] {
			if uid != a.UserID {
				other = uid
			}
		}
		u, _ := f.auth.GetUserByID(ctx, other)
		row := sqlcgen.ListConversationsRow{
			ID: id, UpdatedAt: updated, OtherUserID: other,
			OtherUsername: u.Username, OtherDisplayName: u.DisplayName, LastMessageAt: updated,
		}
		for i := len(f.messages) - 1; i >= 0; i-- {
			if f.messages[i].ConversationID == id {
				row.LastMessageBody = f.messages[i].Body
				row.LastMessageAt = f.messages[i].CreatedAt
				break
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// TestMessagingFlow drives the full 1:1 DM lifecycle over HTTP: start (idempotent),
// send (sender identity echoed), list messages, inbox with the other participant +
// last message; plus authz (non-participant 404), self (422), unknown recipient
// (404), and anonymous (401).
func TestMessagingFlow(t *testing.T) {
	srv := videoServer(t)
	adaTok, adaID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Anonymous cannot start a conversation.
	if rec := postTo(srv, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon start = %d, want 401", rec.Code)
	}
	// Messaging yourself → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+adaID+`"}`, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self start = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Unknown recipient → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+uuid.NewString()+`"}`, adaTok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown recipient = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// Ada starts a conversation with Bob.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var conv conversationView
	if err := json.Unmarshal(rec.Body.Bytes(), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v", err)
	}

	// Bob starting with Ada returns the SAME conversation (idempotent create-or-get).
	rec2 := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+adaID+`"}`, bobTok)
	var conv2 conversationView
	_ = json.Unmarshal(rec2.Body.Bytes(), &conv2)
	if conv2.ID != conv.ID {
		t.Fatalf("start-or-get mismatch: %s vs %s", conv.ID, conv2.ID)
	}

	// Ada sends a message; the response echoes the sender's identity.
	msgRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"body":"hi bob"}`, adaTok)
	if msgRec.Code != http.StatusCreated {
		t.Fatalf("send = %d, want 201; body=%s", msgRec.Code, msgRec.Body.String())
	}
	var mv messageView
	_ = json.Unmarshal(msgRec.Body.Bytes(), &mv)
	if mv.Body != "hi bob" || mv.SenderUsername != "ada" {
		t.Fatalf("send view = %+v, want body 'hi bob' sender 'ada'", mv)
	}

	// A non-participant is 404 on both read and send (existence not leaked).
	carolTok, _ := registerAndUser(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/conversations/"+conv.ID+"/messages", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-participant read = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"body":"sneak"}`, carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-participant send = %d, want 404", rec.Code)
	}

	// Ada lists the conversation's messages.
	var msgs messageListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/conversations/"+conv.ID+"/messages", adaTok).Body.Bytes(), &msgs)
	if len(msgs.Messages) != 1 || msgs.Messages[0].Body != "hi bob" {
		t.Fatalf("messages = %+v, want one 'hi bob'", msgs)
	}

	// Bob's inbox shows the conversation with Ada and the last message.
	var inbox conversationListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/conversations", bobTok).Body.Bytes(), &inbox)
	if len(inbox.Conversations) != 1 || inbox.Conversations[0].OtherUsername != "ada" || inbox.Conversations[0].LastMessageBody != "hi bob" {
		t.Fatalf("inbox = %+v, want one conversation with ada, last 'hi bob'", inbox)
	}
}

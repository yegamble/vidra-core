package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeConv struct {
	id        uuid.UUID
	dmKey     string
	createdAt time.Time
	updatedAt time.Time
}

type fakeMsg struct {
	id        uuid.UUID
	convID    uuid.UUID
	senderID  uuid.UUID
	body      string
	createdAt time.Time
}

// fakeRepo is an in-memory messaging.Repository.
type fakeRepo struct {
	users        map[uuid.UUID]bool
	convByKey    map[string]uuid.UUID
	convs        map[uuid.UUID]*fakeConv
	participants map[uuid.UUID]map[uuid.UUID]bool
	messages     []fakeMsg
}

func newFakeRepo(users ...uuid.UUID) *fakeRepo {
	f := &fakeRepo{
		users:        map[uuid.UUID]bool{},
		convByKey:    map[string]uuid.UUID{},
		convs:        map[uuid.UUID]*fakeConv{},
		participants: map[uuid.UUID]map[uuid.UUID]bool{},
	}
	for _, u := range users {
		f.users[u] = true
	}
	return f
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	if !f.users[id] {
		return sqlcgen.User{}, pgx.ErrNoRows
	}
	return sqlcgen.User{ID: id, Username: "u-" + id.String()[:8]}, nil
}

func (f *fakeRepo) CreateConversation(_ context.Context, dmKey *string) (sqlcgen.CreateConversationRow, error) {
	if _, ok := f.convByKey[*dmKey]; ok {
		return sqlcgen.CreateConversationRow{}, pgx.ErrNoRows // conflict
	}
	c := &fakeConv{id: uuid.New(), dmKey: *dmKey, createdAt: time.Now(), updatedAt: time.Now()}
	f.convs[c.id] = c
	f.convByKey[*dmKey] = c.id
	return sqlcgen.CreateConversationRow{ID: c.id, CreatedAt: c.createdAt, UpdatedAt: c.updatedAt}, nil
}

func (f *fakeRepo) GetConversationByDMKey(_ context.Context, dmKey *string) (sqlcgen.GetConversationByDMKeyRow, error) {
	id, ok := f.convByKey[*dmKey]
	if !ok {
		return sqlcgen.GetConversationByDMKeyRow{}, pgx.ErrNoRows
	}
	c := f.convs[id]
	return sqlcgen.GetConversationByDMKeyRow{ID: c.id, CreatedAt: c.createdAt, UpdatedAt: c.updatedAt}, nil
}

func (f *fakeRepo) AddConversationParticipant(_ context.Context, a sqlcgen.AddConversationParticipantParams) error {
	if f.participants[a.ConversationID] == nil {
		f.participants[a.ConversationID] = map[uuid.UUID]bool{}
	}
	f.participants[a.ConversationID][a.UserID] = true
	return nil
}

func (f *fakeRepo) IsConversationParticipant(_ context.Context, a sqlcgen.IsConversationParticipantParams) (bool, error) {
	return f.participants[a.ConversationID][a.UserID], nil
}

func (f *fakeRepo) CreateMessage(_ context.Context, a sqlcgen.CreateMessageParams) (sqlcgen.Message, error) {
	m := fakeMsg{id: uuid.New(), convID: a.ConversationID, senderID: a.SenderID, body: a.Body, createdAt: time.Now()}
	f.messages = append(f.messages, m)
	return sqlcgen.Message{ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body, CreatedAt: m.createdAt}, nil
}

func (f *fakeRepo) TouchConversation(_ context.Context, id uuid.UUID) error {
	if c := f.convs[id]; c != nil {
		c.updatedAt = time.Now()
	}
	return nil
}

func (f *fakeRepo) ListMessages(_ context.Context, a sqlcgen.ListMessagesParams) ([]sqlcgen.ListMessagesRow, error) {
	var rows []sqlcgen.ListMessagesRow
	for i := len(f.messages) - 1; i >= 0; i-- { // newest first
		m := f.messages[i]
		if m.convID != a.ConversationID {
			continue
		}
		rows = append(rows, sqlcgen.ListMessagesRow{
			ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body,
			CreatedAt: m.createdAt, SenderUsername: "u-" + m.senderID.String()[:8],
		})
	}
	return rows, nil
}

func (f *fakeRepo) ListConversations(_ context.Context, a sqlcgen.ListConversationsParams) ([]sqlcgen.ListConversationsRow, error) {
	var rows []sqlcgen.ListConversationsRow
	for id, c := range f.convs {
		if !f.participants[id][a.UserID] {
			continue
		}
		var other uuid.UUID
		for uid := range f.participants[id] {
			if uid != a.UserID {
				other = uid
			}
		}
		row := sqlcgen.ListConversationsRow{
			ID: c.id, UpdatedAt: c.updatedAt, OtherUserID: other,
			OtherUsername: "u-" + other.String()[:8], LastMessageAt: c.updatedAt,
		}
		for i := len(f.messages) - 1; i >= 0; i-- {
			if f.messages[i].convID == id {
				row.LastMessageBody = f.messages[i].body
				row.LastMessageAt = f.messages[i].createdAt
				break
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func TestStartConversationCreatesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()
	svc := NewService(newFakeRepo(ada, bob))

	c1, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	// Idempotent: the reverse pair returns the same conversation.
	c2, err := svc.StartConversation(ctx, bob, ada)
	if err != nil {
		t.Fatalf("StartConversation (reverse): %v", err)
	}
	if c1.ID != c2.ID {
		t.Errorf("start-or-get returned different conversations: %s vs %s", c1.ID, c2.ID)
	}
}

func TestStartConversationErrors(t *testing.T) {
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()
	svc := NewService(newFakeRepo(ada, bob))

	if _, err := svc.StartConversation(ctx, ada, ada); err != ErrCannotMessageSelf {
		t.Errorf("self err = %v, want ErrCannotMessageSelf", err)
	}
	if _, err := svc.StartConversation(ctx, ada, uuid.New()); err != ErrRecipientNotFound {
		t.Errorf("unknown recipient err = %v, want ErrRecipientNotFound", err)
	}
}

func TestSendAndListMessages(t *testing.T) {
	ctx := context.Background()
	ada, bob, carol := uuid.New(), uuid.New(), uuid.New()
	svc := NewService(newFakeRepo(ada, bob, carol))

	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if _, err := svc.SendMessage(ctx, ada, conv.ID, "hi bob"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, err := svc.SendMessage(ctx, bob, conv.ID, "hi ada"); err != nil {
		t.Fatalf("SendMessage (bob): %v", err)
	}

	// A non-participant cannot send or read.
	if _, err := svc.SendMessage(ctx, carol, conv.ID, "sneaky"); err != ErrNotParticipant {
		t.Errorf("non-participant send err = %v, want ErrNotParticipant", err)
	}
	if _, err := svc.ListMessages(ctx, carol, conv.ID, 20, 0); err != ErrNotParticipant {
		t.Errorf("non-participant list err = %v, want ErrNotParticipant", err)
	}

	msgs, err := svc.ListMessages(ctx, ada, conv.ID, 20, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Body != "hi ada" { // newest first
		t.Fatalf("messages = %+v, want 2 newest-first (hi ada)", msgs)
	}

	// The conversation appears in each participant's inbox with the last message.
	inbox, err := svc.ListConversations(ctx, ada, 20, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(inbox) != 1 || inbox[0].OtherUserID != bob || inbox[0].LastMessageBody != "hi ada" {
		t.Fatalf("inbox = %+v, want one conversation with bob, last 'hi ada'", inbox)
	}
}

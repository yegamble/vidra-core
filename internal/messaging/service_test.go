package messaging

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	deleted   bool
	linkHash  string
}

type fakeAttachment struct {
	id, convID, uploaderID uuid.UUID
	messageID              uuid.UUID // uuid.Nil = unlinked
	kind, ct, filename     string
	size                   int64
	key                    string
	width, height          *int32
}

// fakeRepo is an in-memory messaging.Repository.
type fakeRepo struct {
	users        map[uuid.UUID]bool
	convByKey    map[string]uuid.UUID
	convs        map[uuid.UUID]*fakeConv
	participants map[uuid.UUID]map[uuid.UUID]bool
	messages     []fakeMsg
	attachments  []fakeAttachment
	previews     map[string]sqlcgen.LinkPreview
	watermark    map[uuid.UUID]map[uuid.UUID]uuid.UUID // conv -> user -> last read msg
	prefs        map[uuid.UUID]map[string]bool         // user -> type -> enabled
	avatars      map[uuid.UUID]bool                    // user -> has avatar (D3 flag source)
}

func newFakeRepo(users ...uuid.UUID) *fakeRepo {
	f := &fakeRepo{
		users:        map[uuid.UUID]bool{},
		convByKey:    map[string]uuid.UUID{},
		convs:        map[uuid.UUID]*fakeConv{},
		participants: map[uuid.UUID]map[uuid.UUID]bool{},
		previews:     map[string]sqlcgen.LinkPreview{},
		watermark:    map[uuid.UUID]map[uuid.UUID]uuid.UUID{},
		prefs:        map[uuid.UUID]map[string]bool{},
		avatars:      map[uuid.UUID]bool{},
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

// GetUserByUsername mirrors the real query (case-insensitive, active-only)
// against the fake's synthetic "u-<id[:8]>" usernames. A miss is pgx.ErrNoRows.
func (f *fakeRepo) GetUserByUsername(_ context.Context, lower string) (sqlcgen.User, error) {
	name := strings.TrimSpace(lower)
	for id, active := range f.users {
		if active && strings.EqualFold("u-"+id.String()[:8], name) {
			return sqlcgen.User{ID: id, Username: "u-" + id.String()[:8]}, nil
		}
	}
	return sqlcgen.User{}, pgx.ErrNoRows
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

func (f *fakeRepo) CreateMessage(_ context.Context, a sqlcgen.CreateMessageParams) (sqlcgen.CreateMessageRow, error) {
	m := fakeMsg{id: uuid.New(), convID: a.ConversationID, senderID: a.SenderID, body: a.Body, createdAt: time.Now()}
	f.messages = append(f.messages, m)
	return sqlcgen.CreateMessageRow{ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body, CreatedAt: m.createdAt}, nil
}

func (f *fakeRepo) GetOtherParticipant(_ context.Context, a sqlcgen.GetOtherParticipantParams) (uuid.UUID, error) {
	for uid := range f.participants[a.ConversationID] {
		if uid != a.UserID {
			return uid, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
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
		row := sqlcgen.ListMessagesRow{
			ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body,
			CreatedAt: m.createdAt, Deleted: m.deleted, SenderUsername: "u-" + m.senderID.String()[:8],
			SenderHasAvatar: f.avatars[m.senderID],
		}
		if m.linkHash != "" {
			if lp, ok := f.previews[m.linkHash]; ok && lp.State == "ready" {
				url, title, desc, img := lp.Url, lp.Title, lp.Description, lp.ImageUrl
				row.PreviewUrl, row.PreviewTitle, row.PreviewDescription, row.PreviewImage = &url, &title, &desc, &img
			}
		}
		rows = append(rows, row)
	}
	// Honour offset then limit (mirrors the SQL LIMIT/OFFSET).
	if int(a.ResultOffset) < len(rows) {
		rows = rows[a.ResultOffset:]
	} else {
		rows = nil
	}
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

// ListMessagesBefore mirrors the keyset SQL: messages in the conversation
// strictly older than the (before_created_at, before_id) cursor by row-value
// comparison, ordered created_at DESC, id DESC (Postgres compares uuids
// bytewise), limited.
func (f *fakeRepo) ListMessagesBefore(_ context.Context, a sqlcgen.ListMessagesBeforeParams) ([]sqlcgen.ListMessagesBeforeRow, error) {
	var msgs []fakeMsg
	for _, m := range f.messages {
		if m.convID != a.ConversationID {
			continue
		}
		// (created_at, id) < (before_created_at, before_id)
		if m.createdAt.Before(a.BeforeCreatedAt) ||
			(m.createdAt.Equal(a.BeforeCreatedAt) && bytes.Compare(m.id[:], a.BeforeID[:]) < 0) {
			msgs = append(msgs, m)
		}
	}
	sort.Slice(msgs, func(i, j int) bool {
		if !msgs[i].createdAt.Equal(msgs[j].createdAt) {
			return msgs[i].createdAt.After(msgs[j].createdAt) // created_at DESC
		}
		return bytes.Compare(msgs[i].id[:], msgs[j].id[:]) > 0 // id DESC
	})
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(msgs) {
		msgs = msgs[:a.ResultLimit]
	}
	rows := make([]sqlcgen.ListMessagesBeforeRow, 0, len(msgs))
	for _, m := range msgs {
		row := sqlcgen.ListMessagesBeforeRow{
			ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body,
			CreatedAt: m.createdAt, Deleted: m.deleted, SenderUsername: "u-" + m.senderID.String()[:8],
			SenderHasAvatar: f.avatars[m.senderID],
		}
		if m.linkHash != "" {
			if lp, ok := f.previews[m.linkHash]; ok && lp.State == "ready" {
				url, title, desc, img := lp.Url, lp.Title, lp.Description, lp.ImageUrl
				row.PreviewUrl, row.PreviewTitle, row.PreviewDescription, row.PreviewImage = &url, &title, &desc, &img
			}
		}
		rows = append(rows, row)
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
			OtherHasAvatar: f.avatars[other],
		}
		var wmAt time.Time
		if w, ok := f.watermark[id][a.UserID]; ok {
			for _, m := range f.messages {
				if m.id == w {
					wmAt = m.createdAt
				}
			}
		}
		lastSet := false
		for i := len(f.messages) - 1; i >= 0; i-- {
			m := f.messages[i]
			if m.convID != id {
				continue
			}
			if !lastSet { // newest-first iteration: first match is the latest
				row.LastMessageBody = m.body
				row.LastMessageAt = m.createdAt
				lastSet = true
			}
			if m.senderID != a.UserID && m.createdAt.After(wmAt) {
				row.UnreadCount++
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// fakeBlocker is a messaging.Blocker whose verdict is fixed.
type fakeBlocker struct{ blocked bool }

func (b fakeBlocker) IsBlockedBetween(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return b.blocked, nil
}

// --- DM completeness (§14) fake methods ---

func (f *fakeRepo) msg(id uuid.UUID) (*fakeMsg, bool) {
	for i := range f.messages {
		if f.messages[i].id == id {
			return &f.messages[i], true
		}
	}
	return nil, false
}

func (f *fakeRepo) GetMessage(_ context.Context, id uuid.UUID) (sqlcgen.GetMessageRow, error) {
	m, ok := f.msg(id)
	if !ok {
		return sqlcgen.GetMessageRow{}, pgx.ErrNoRows
	}
	row := sqlcgen.GetMessageRow{ID: m.id, ConversationID: m.convID, SenderID: m.senderID, Body: m.body, CreatedAt: m.createdAt}
	if m.deleted {
		row.DeletedAt = pgtype.Timestamptz{Time: m.createdAt, Valid: true}
	}
	return row, nil
}

func (f *fakeRepo) GetLatestMessageID(_ context.Context, convID uuid.UUID) (uuid.UUID, error) {
	var latest *fakeMsg
	for i := range f.messages {
		if f.messages[i].convID == convID && (latest == nil || f.messages[i].createdAt.After(latest.createdAt)) {
			latest = &f.messages[i]
		}
	}
	if latest == nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return latest.id, nil
}

func (f *fakeRepo) SetLastReadMessage(_ context.Context, a sqlcgen.SetLastReadMessageParams) error {
	if f.watermark[a.ConversationID] == nil {
		f.watermark[a.ConversationID] = map[uuid.UUID]uuid.UUID{}
	}
	f.watermark[a.ConversationID][a.UserID] = a.MessageID.Bytes
	return nil
}

func (f *fakeRepo) GetPeerReadWatermark(_ context.Context, a sqlcgen.GetPeerReadWatermarkParams) (pgtype.UUID, error) {
	for uid, w := range f.watermark[a.ConversationID] {
		if uid != a.UserID {
			return pgtype.UUID{Bytes: w, Valid: w != uuid.Nil}, nil
		}
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

func (f *fakeRepo) TombstoneMessage(_ context.Context, a sqlcgen.TombstoneMessageParams) (int64, error) {
	m, ok := f.msg(a.ID)
	if !ok || m.senderID != a.SenderID || m.deleted {
		return 0, nil
	}
	m.body = "[deleted]"
	m.deleted = true
	m.linkHash = ""
	return 1, nil
}

func (f *fakeRepo) CreateMessageAttachment(_ context.Context, a sqlcgen.CreateMessageAttachmentParams) (sqlcgen.MessageAttachment, error) {
	att := fakeAttachment{
		id: a.ID, convID: a.ConversationID, uploaderID: a.UploaderID,
		kind: a.Kind, ct: a.ContentType, filename: a.Filename, size: a.SizeBytes, key: a.StorageKey,
		width: a.Width, height: a.Height,
	}
	f.attachments = append(f.attachments, att)
	return sqlcgen.MessageAttachment{
		ID: att.id, ConversationID: att.convID, UploaderID: att.uploaderID,
		Kind: att.kind, ContentType: att.ct, Filename: att.filename, SizeBytes: att.size, StorageKey: att.key,
		Width: att.width, Height: att.height,
	}, nil
}

func (f *fakeRepo) GetMessageAttachment(_ context.Context, id uuid.UUID) (sqlcgen.MessageAttachment, error) {
	for _, a := range f.attachments {
		if a.id == id {
			row := sqlcgen.MessageAttachment{
				ID: a.id, ConversationID: a.convID, UploaderID: a.uploaderID,
				Kind: a.kind, ContentType: a.ct, Filename: a.filename, SizeBytes: a.size, StorageKey: a.key,
			}
			if a.messageID != uuid.Nil {
				row.MessageID = pgtype.UUID{Bytes: a.messageID, Valid: true}
			}
			return row, nil
		}
	}
	return sqlcgen.MessageAttachment{}, pgx.ErrNoRows
}

func (f *fakeRepo) ListOwnedUnlinkedAttachments(_ context.Context, a sqlcgen.ListOwnedUnlinkedAttachmentsParams) ([]sqlcgen.ListOwnedUnlinkedAttachmentsRow, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range a.Ids {
		want[id] = true
	}
	var out []sqlcgen.ListOwnedUnlinkedAttachmentsRow
	for _, at := range f.attachments {
		if want[at.id] && at.uploaderID == a.UploaderID && at.convID == a.ConversationID && at.messageID == uuid.Nil {
			out = append(out, sqlcgen.ListOwnedUnlinkedAttachmentsRow{ID: at.id, StorageKey: at.key})
		}
	}
	return out, nil
}

func (f *fakeRepo) LinkAttachmentsToMessage(_ context.Context, a sqlcgen.LinkAttachmentsToMessageParams) error {
	want := map[uuid.UUID]bool{}
	for _, id := range a.Ids {
		want[id] = true
	}
	for i := range f.attachments {
		at := &f.attachments[i]
		if want[at.id] && at.uploaderID == a.UploaderID && at.convID == a.ConversationID && at.messageID == uuid.Nil {
			at.messageID = a.MessageID.Bytes
		}
	}
	return nil
}

func (f *fakeRepo) ListAttachmentsForMessages(_ context.Context, ids []uuid.UUID) ([]sqlcgen.ListAttachmentsForMessagesRow, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []sqlcgen.ListAttachmentsForMessagesRow
	for _, at := range f.attachments {
		if at.messageID != uuid.Nil && want[at.messageID] {
			out = append(out, sqlcgen.ListAttachmentsForMessagesRow{
				ID: at.id, MessageID: pgtype.UUID{Bytes: at.messageID, Valid: true},
				Kind: at.kind, ContentType: at.ct, Filename: at.filename, SizeBytes: at.size,
				Width: at.width, Height: at.height,
			})
		}
	}
	return out, nil
}

func (f *fakeRepo) ListAttachmentKeysByMessage(_ context.Context, mid pgtype.UUID) ([]sqlcgen.ListAttachmentKeysByMessageRow, error) {
	var out []sqlcgen.ListAttachmentKeysByMessageRow
	for _, at := range f.attachments {
		if at.messageID == mid.Bytes {
			out = append(out, sqlcgen.ListAttachmentKeysByMessageRow{ID: at.id, StorageKey: at.key})
		}
	}
	return out, nil
}

func (f *fakeRepo) DeleteAttachmentsByMessage(_ context.Context, mid pgtype.UUID) error {
	kept := f.attachments[:0]
	for _, at := range f.attachments {
		if at.messageID != mid.Bytes {
			kept = append(kept, at)
		}
	}
	f.attachments = kept
	return nil
}

func (f *fakeRepo) UpsertPendingLinkPreview(_ context.Context, a sqlcgen.UpsertPendingLinkPreviewParams) error {
	if _, ok := f.previews[a.UrlHash]; !ok {
		f.previews[a.UrlHash] = sqlcgen.LinkPreview{UrlHash: a.UrlHash, Url: a.Url, State: "pending"}
	}
	return nil
}

func (f *fakeRepo) SetMessageLinkPreview(_ context.Context, a sqlcgen.SetMessageLinkPreviewParams) error {
	if m, ok := f.msg(a.ID); ok && a.UrlHash != nil {
		m.linkHash = *a.UrlHash
	}
	return nil
}

func (f *fakeRepo) GetLinkPreview(_ context.Context, urlHash string) (sqlcgen.LinkPreview, error) {
	if lp, ok := f.previews[urlHash]; ok {
		return lp, nil
	}
	return sqlcgen.LinkPreview{}, pgx.ErrNoRows
}

func (f *fakeRepo) ResolveLinkPreview(_ context.Context, a sqlcgen.ResolveLinkPreviewParams) error {
	lp := f.previews[a.UrlHash]
	lp.UrlHash = a.UrlHash
	lp.State = a.State
	lp.Title = a.Title
	lp.Description = a.Description
	lp.ImageUrl = a.ImageUrl
	f.previews[a.UrlHash] = lp
	return nil
}

func (f *fakeRepo) IsNotificationTypeEnabled(_ context.Context, a sqlcgen.IsNotificationTypeEnabledParams) (bool, error) {
	if m, ok := f.prefs[a.UserID]; ok {
		if v, ok := m[a.Type]; ok {
			return v, nil
		}
	}
	return true, nil // default enabled
}

func (f *fakeRepo) UpsertNotificationPref(_ context.Context, a sqlcgen.UpsertNotificationPrefParams) error {
	if f.prefs[a.UserID] == nil {
		f.prefs[a.UserID] = map[string]bool{}
	}
	f.prefs[a.UserID][a.Type] = a.Enabled
	return nil
}

func TestStartConversationRefusedWhenBlocked(t *testing.T) {
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()
	svc := NewService(newFakeRepo(ada, bob), WithBlocker(fakeBlocker{blocked: true}))

	if _, err := svc.StartConversation(ctx, ada, bob); err != ErrBlocked {
		t.Errorf("blocked StartConversation err = %v, want ErrBlocked", err)
	}
}

func TestSendMessageRefusedWhenBlocked(t *testing.T) {
	ctx := context.Background()
	ada, bob, carol := uuid.New(), uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob, carol)

	// Create the conversation with no block in force.
	conv, err := NewService(repo).StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	// Now a block exists between the participants → sends are refused.
	blocked := NewService(repo, WithBlocker(fakeBlocker{blocked: true}))
	if _, err := blocked.SendMessage(ctx, ada, conv.ID, "hi", nil); err != ErrBlocked {
		t.Errorf("blocked SendMessage err = %v, want ErrBlocked", err)
	}
	// A non-participant is still ErrNotParticipant (participant check precedes the
	// block check, so existence isn't leaked as a 403).
	if _, err := blocked.SendMessage(ctx, carol, conv.ID, "hi", nil); err != ErrNotParticipant {
		t.Errorf("non-participant err = %v, want ErrNotParticipant", err)
	}
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

func TestResolveRecipientUsername(t *testing.T) {
	ctx := context.Background()
	ada := uuid.New()
	svc := NewService(newFakeRepo(ada))

	// Resolves case-insensitively to the user's id.
	name := "u-" + ada.String()[:8]
	got, err := svc.ResolveRecipientUsername(ctx, strings.ToUpper(name))
	if err != nil || got != ada {
		t.Fatalf("resolve = (%s, %v), want (%s, nil)", got, err, ada)
	}
	// Unknown username → ErrRecipientNotFound (same as an unknown id).
	if _, err := svc.ResolveRecipientUsername(ctx, "nobody"); err != ErrRecipientNotFound {
		t.Errorf("unknown username err = %v, want ErrRecipientNotFound", err)
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
	// The other participant (recipient of ada's messages) is bob.
	if other, err := svc.OtherParticipant(ctx, conv.ID, ada); err != nil || other != bob {
		t.Fatalf("OtherParticipant = (%s, %v), want (%s, nil)", other, err, bob)
	}
	if _, err := svc.SendMessage(ctx, ada, conv.ID, "hi bob", nil); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, err := svc.SendMessage(ctx, bob, conv.ID, "hi ada", nil); err != nil {
		t.Fatalf("SendMessage (bob): %v", err)
	}

	// A non-participant cannot send or read.
	if _, err := svc.SendMessage(ctx, carol, conv.ID, "sneaky", nil); err != ErrNotParticipant {
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

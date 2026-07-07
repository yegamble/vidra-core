//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedConversation seeds two users and a 1:1 conversation with both as
// participants, returning the ids and a cleanup that cascades everything.
func seedConversation(t *testing.T, st *Store) (convID, ada, bob uuid.UUID, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	mk := func(tag string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			"m2-"+tag+"-"+suffix, "m2-"+tag+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", tag, err)
		}
		return id
	}
	ada, bob = mk("ada"), mk("bob")
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO conversations (dm_key) VALUES ($1) RETURNING id`, "m2-"+suffix,
	).Scan(&convID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	for _, u := range []uuid.UUID{ada, bob} {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO conversation_participants (conversation_id, user_id) VALUES ($1, $2)`, convID, u); err != nil {
			t.Fatalf("seed participant: %v", err)
		}
	}
	cleanup = func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM conversations WHERE id = $1`, convID)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{ada, bob})
	}
	return convID, ada, bob, cleanup
}

// insertMessage inserts a message with an explicit created_at so keyset ordering
// is deterministic, returning its id.
func insertMessage(t *testing.T, st *Store, convID, sender uuid.UUID, body string, at time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := st.Pool.QueryRow(context.Background(),
		`INSERT INTO messages (conversation_id, sender_id, body, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		convID, sender, body, at,
	).Scan(&id); err != nil {
		t.Fatalf("insert message %q: %v", body, err)
	}
	return id
}

func i32(v int32) *int32 { return &v }

// TestMessagingV2AttachmentDimensionsPersist proves D1: width/height round-trip
// through CreateMessageAttachment, GetMessageAttachment, and (after linking)
// ListAttachmentsForMessages against real PostgreSQL, and that the CHECK (> 0)
// constraint holds.
func TestMessagingV2AttachmentDimensionsPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	convID, ada, _, cleanup := seedConversation(t, st)
	defer cleanup()

	// Create an image attachment carrying probed dimensions.
	attID := uuid.New()
	att, err := q.CreateMessageAttachment(ctx, sqlcgen.CreateMessageAttachmentParams{
		ID: attID, ConversationID: convID, UploaderID: ada, Kind: "image",
		ContentType: "image/png", Filename: "p.png", SizeBytes: 123,
		StorageKey: "dm-attachments/x/p.png", Width: i32(640), Height: i32(480),
	})
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	if att.Width == nil || *att.Width != 640 || att.Height == nil || *att.Height != 480 {
		t.Fatalf("create returned dims (%v,%v), want (640,480)", att.Width, att.Height)
	}
	// Re-read proves persistence.
	got, err := q.GetMessageAttachment(ctx, attID)
	if err != nil || got.Width == nil || *got.Width != 640 || got.Height == nil || *got.Height != 480 {
		t.Fatalf("get dims = (%v,%v,%v), want (640,480)", got.Width, got.Height, err)
	}

	// A non-image attachment stores NULL dimensions.
	pdfID := uuid.New()
	if _, err := q.CreateMessageAttachment(ctx, sqlcgen.CreateMessageAttachmentParams{
		ID: pdfID, ConversationID: convID, UploaderID: ada, Kind: "pdf",
		ContentType: "application/pdf", Filename: "d.pdf", SizeBytes: 9,
		StorageKey: "dm-attachments/x/d.pdf",
	}); err != nil {
		t.Fatalf("create pdf: %v", err)
	}

	// Link the image to a message; the list projection carries the dimensions.
	msgID := insertMessage(t, st, convID, ada, "look", time.Now())
	if err := q.LinkAttachmentsToMessage(ctx, sqlcgen.LinkAttachmentsToMessageParams{
		MessageID: pgtype.UUID{Bytes: msgID, Valid: true}, Ids: []uuid.UUID{attID}, UploaderID: ada, ConversationID: convID,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	rows, err := q.ListAttachmentsForMessages(ctx, []uuid.UUID{msgID})
	if err != nil || len(rows) != 1 || rows[0].Width == nil || *rows[0].Width != 640 || rows[0].Height == nil || *rows[0].Height != 480 {
		t.Fatalf("list dims = %+v (err %v), want one row 640x480", rows, err)
	}

	// The CHECK (> 0) constraint rejects zero dimensions.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO message_attachments (conversation_id, uploader_id, kind, content_type, filename, size_bytes, storage_key, width, height)
		 VALUES ($1,$2,'image','image/png','z.png',1,'k',0,1)`, convID, ada); err == nil {
		t.Fatal("expected CHECK violation for width = 0, got nil")
	}
}

// TestMessagingV2DocKindPersists proves D6: kind='doc' passes the widened CHECK
// constraint (migration 0070), round-trips through Create/Get/List against real
// PostgreSQL, and a linked attachment is removed by the tombstone-cleanup query —
// while a bogus kind is still rejected by the constraint.
func TestMessagingV2DocKindPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	convID, ada, _, cleanup := seedConversation(t, st)
	defer cleanup()

	// A doc-kind attachment persists (the CHECK now accepts 'doc').
	attID := uuid.New()
	att, err := q.CreateMessageAttachment(ctx, sqlcgen.CreateMessageAttachmentParams{
		ID: attID, ConversationID: convID, UploaderID: ada, Kind: "doc",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Filename:    "report.docx", SizeBytes: 2048, StorageKey: "dm-attachments/x/report.docx",
	})
	if err != nil {
		t.Fatalf("create doc attachment: %v", err)
	}
	if att.Kind != "doc" {
		t.Fatalf("create kind = %q, want doc", att.Kind)
	}
	got, err := q.GetMessageAttachment(ctx, attID)
	if err != nil || got.Kind != "doc" {
		t.Fatalf("get kind = %q (err %v), want doc", got.Kind, err)
	}

	// Link to a message; the list projection carries kind=doc.
	msgID := insertMessage(t, st, convID, ada, "here is the doc", time.Now())
	if err := q.LinkAttachmentsToMessage(ctx, sqlcgen.LinkAttachmentsToMessageParams{
		MessageID: pgtype.UUID{Bytes: msgID, Valid: true}, Ids: []uuid.UUID{attID}, UploaderID: ada, ConversationID: convID,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	rows, err := q.ListAttachmentsForMessages(ctx, []uuid.UUID{msgID})
	if err != nil || len(rows) != 1 || rows[0].Kind != "doc" {
		t.Fatalf("list = %+v (err %v), want one doc row", rows, err)
	}

	// Tombstone cleanup: keys are enumerated then the rows deleted (DB-proof that a
	// tombstone removes a doc attachment).
	keys, err := q.ListAttachmentKeysByMessage(ctx, pgtype.UUID{Bytes: msgID, Valid: true})
	if err != nil || len(keys) != 1 || keys[0].StorageKey != "dm-attachments/x/report.docx" {
		t.Fatalf("keys = %+v (err %v), want the one doc key", keys, err)
	}
	if err := q.DeleteAttachmentsByMessage(ctx, pgtype.UUID{Bytes: msgID, Valid: true}); err != nil {
		t.Fatalf("delete by message: %v", err)
	}
	if after, err := q.ListAttachmentsForMessages(ctx, []uuid.UUID{msgID}); err != nil || len(after) != 0 {
		t.Fatalf("after tombstone delete = %+v (err %v), want none", after, err)
	}

	// The widened CHECK still rejects an unknown kind.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO message_attachments (conversation_id, uploader_id, kind, content_type, filename, size_bytes, storage_key)
		 VALUES ($1,$2,'exe','application/octet-stream','x',1,'k')`, convID, ada); err == nil {
		t.Fatal("expected CHECK violation for kind='exe', got nil")
	}
}

// TestMessagingV2KeysetStablePaging proves D2: the real row-value keyset query
// pages stably even when a new message arrives between pages (no duplicate/skip),
// where an offset page would have shifted.
func TestMessagingV2KeysetStablePaging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	convID, ada, _, cleanup := seedConversation(t, st)
	defer cleanup()

	base := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, 4)
	for i := 0; i < 4; i++ {
		ids[i] = insertMessage(t, st, convID, ada, string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute))
	}
	// ids[0] oldest … ids[3] newest.

	// Page 1: newest two via offset paging.
	p1, err := q.ListMessages(ctx, sqlcgen.ListMessagesParams{ConversationID: convID, ResultLimit: 2, ResultOffset: 0})
	if err != nil || len(p1) != 2 || p1[0].ID != ids[3] || p1[1].ID != ids[2] {
		t.Fatalf("page1 = %+v (err %v), want [d c]", p1, err)
	}

	// A new message arrives (would shift an offset page).
	insertMessage(t, st, convID, ada, "z", base.Add(10*time.Minute))

	// Keyset page 2 from the oldest of page 1 (ids[2]): stable, older-only.
	cur, err := q.GetMessage(ctx, ids[2])
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	p2, err := q.ListMessagesBefore(ctx, sqlcgen.ListMessagesBeforeParams{
		ConversationID: convID, BeforeCreatedAt: cur.CreatedAt, BeforeID: cur.ID, ResultLimit: 2,
	})
	if err != nil || len(p2) != 2 || p2[0].ID != ids[1] || p2[1].ID != ids[0] {
		t.Fatalf("keyset page2 = %+v (err %v), want [b a] (no dup/skip despite z insert)", p2, err)
	}

	// Boundary: before the oldest message is an empty page.
	oldest, _ := q.GetMessage(ctx, ids[0])
	if empty, err := q.ListMessagesBefore(ctx, sqlcgen.ListMessagesBeforeParams{
		ConversationID: convID, BeforeCreatedAt: oldest.CreatedAt, BeforeID: oldest.ID, ResultLimit: 2,
	}); err != nil || len(empty) != 0 {
		t.Fatalf("before oldest = %+v (err %v), want empty", empty, err)
	}
}

// TestMessagingV2AvatarFlags proves D3: the EXISTS-join avatar flags reflect a
// real user_images row for both ListConversations and ListMessages.
func TestMessagingV2AvatarFlags(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	convID, ada, bob, cleanup := seedConversation(t, st)
	defer cleanup()
	insertMessage(t, st, convID, ada, "hi from ada", time.Now())
	insertMessage(t, st, convID, bob, "hi from bob", time.Now().Add(time.Second))

	// No avatars yet → both flags false.
	msgs, err := q.ListMessages(ctx, sqlcgen.ListMessagesParams{ConversationID: convID, ResultLimit: 10, ResultOffset: 0})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	for _, m := range msgs {
		if m.SenderHasAvatar {
			t.Fatalf("sender_has_avatar = true before any avatar set")
		}
	}

	// Give ada an avatar.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO user_images (user_id, kind, storage_key, size_bytes) VALUES ($1,'avatar','avatars/users/a.png',1)`, ada); err != nil {
		t.Fatalf("seed avatar: %v", err)
	}

	// Bob's inbox: the other participant (ada) now shows has_avatar.
	convs, err := q.ListConversations(ctx, sqlcgen.ListConversationsParams{UserID: bob, ResultLimit: 10, ResultOffset: 0})
	if err != nil || len(convs) != 1 || !convs[0].OtherHasAvatar {
		t.Fatalf("bob inbox other_has_avatar = %+v (err %v), want true", convs, err)
	}
	// Ada's inbox: the other participant (bob) has no avatar.
	convsA, err := q.ListConversations(ctx, sqlcgen.ListConversationsParams{UserID: ada, ResultLimit: 10, ResultOffset: 0})
	if err != nil || len(convsA) != 1 || convsA[0].OtherHasAvatar {
		t.Fatalf("ada inbox other_has_avatar = %+v (err %v), want false", convsA, err)
	}
	// Messages: ada's sender_has_avatar true, bob's false.
	msgs, err = q.ListMessages(ctx, sqlcgen.ListMessagesParams{ConversationID: convID, ResultLimit: 10, ResultOffset: 0})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	for _, m := range msgs {
		want := m.SenderID == ada
		if m.SenderHasAvatar != want {
			t.Fatalf("message from %v sender_has_avatar = %v, want %v", m.SenderID, m.SenderHasAvatar, want)
		}
	}
}

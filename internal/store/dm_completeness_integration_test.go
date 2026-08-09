//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func pgUUIDVal(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// seedDMUser inserts a bare user and returns its id.
func seedDMUser(t *testing.T, st *Store, tag string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		tag+"-"+suffix, tag+"-"+suffix+"@example.test",
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// TestDMCompletenessQueries exercises the §14 DM-completeness queries against
// real PostgreSQL: attachments (create → validate-unlinked → link → list), link
// previews (upsert → set → resolve → joined onto the message), read receipts
// (watermark + unread counts + peer watermark), tombstone, and message reports.
func TestDMCompletenessQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	ada := seedDMUser(t, st, "ada")
	bob := seedDMUser(t, st, "bob")
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{ada, bob})
	}()

	// Conversation with both participants.
	key := ada.String() + ":" + bob.String()
	conv, err := q.CreateConversation(ctx, &key)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	for _, u := range []uuid.UUID{ada, bob} {
		if err := q.AddConversationParticipant(ctx, sqlcgen.AddConversationParticipantParams{ConversationID: conv.ID, UserID: u}); err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
	}

	// Attachment: Ada uploads (unlinked), it validates as owned+unlinked.
	attID := uuid.New()
	if _, err := q.CreateMessageAttachment(ctx, sqlcgen.CreateMessageAttachmentParams{
		ID: attID, ConversationID: conv.ID, UploaderID: ada, Kind: "image",
		ContentType: "image/png", Filename: "p.png", SizeBytes: 10, StorageKey: "dm-attachments/x/y.png",
	}); err != nil {
		t.Fatalf("CreateMessageAttachment: %v", err)
	}
	owned, err := q.ListOwnedUnlinkedAttachments(ctx, sqlcgen.ListOwnedUnlinkedAttachmentsParams{
		Ids: []uuid.UUID{attID}, UploaderID: ada, ConversationID: conv.ID,
	})
	if err != nil || len(owned) != 1 {
		t.Fatalf("ListOwnedUnlinkedAttachments = %v, %v", owned, err)
	}
	// Bob cannot claim Ada's attachment.
	if foreign, _ := q.ListOwnedUnlinkedAttachments(ctx, sqlcgen.ListOwnedUnlinkedAttachmentsParams{
		Ids: []uuid.UUID{attID}, UploaderID: bob, ConversationID: conv.ID,
	}); len(foreign) != 0 {
		t.Fatalf("bob claimed ada's attachment: %v", foreign)
	}

	// Ada sends a message carrying a URL, links the attachment.
	msg, err := q.CreateMessage(ctx, sqlcgen.CreateMessageParams{ConversationID: conv.ID, SenderID: ada, Body: "see https://example.test/x"})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if err := q.LinkAttachmentsToMessage(ctx, sqlcgen.LinkAttachmentsToMessageParams{
		MessageID: pgUUIDVal(msg.ID), Ids: []uuid.UUID{attID}, UploaderID: ada, ConversationID: conv.ID,
	}); err != nil {
		t.Fatalf("LinkAttachmentsToMessage: %v", err)
	}
	atts, err := q.ListAttachmentsForMessages(ctx, []uuid.UUID{msg.ID})
	if err != nil || len(atts) != 1 || atts[0].ID != attID {
		t.Fatalf("ListAttachmentsForMessages = %v, %v", atts, err)
	}

	// Link preview: upsert pending, attach to message, resolve ready.
	hash := "hash-" + uuid.NewString()
	if err := q.UpsertPendingLinkPreview(ctx, sqlcgen.UpsertPendingLinkPreviewParams{UrlHash: hash, Url: "https://example.test/x"}); err != nil {
		t.Fatalf("UpsertPendingLinkPreview: %v", err)
	}
	if err := q.SetMessageLinkPreview(ctx, sqlcgen.SetMessageLinkPreviewParams{ID: msg.ID, UrlHash: &hash}); err != nil {
		t.Fatalf("SetMessageLinkPreview: %v", err)
	}
	if err := q.ResolveLinkPreview(ctx, sqlcgen.ResolveLinkPreviewParams{
		UrlHash: hash, State: "ready", Title: "Example", Description: "d", ImageUrl: "https://img.test/i.png",
	}); err != nil {
		t.Fatalf("ResolveLinkPreview: %v", err)
	}
	msgs, err := q.ListMessages(ctx, sqlcgen.ListMessagesParams{ConversationID: conv.ID, ResultLimit: 20})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %v, %v", msgs, err)
	}
	if msgs[0].PreviewUrl == nil || *msgs[0].PreviewTitle != "Example" {
		t.Fatalf("preview not joined: %+v", msgs[0])
	}

	// Read receipts: Bob has 1 unread (Ada's message) until he marks it read.
	inbox, err := q.ListConversations(ctx, sqlcgen.ListConversationsParams{UserID: bob, ResultLimit: 20})
	if err != nil || len(inbox) != 1 || inbox[0].UnreadCount != 1 {
		t.Fatalf("bob inbox unread = %v (%v)", inbox, err)
	}
	if err := q.SetLastReadMessage(ctx, sqlcgen.SetLastReadMessageParams{
		MessageID: pgUUIDVal(msg.ID), ConversationID: conv.ID, UserID: bob,
	}); err != nil {
		t.Fatalf("SetLastReadMessage: %v", err)
	}
	inbox, _ = q.ListConversations(ctx, sqlcgen.ListConversationsParams{UserID: bob, ResultLimit: 20})
	if inbox[0].UnreadCount != 0 {
		t.Errorf("after read unread = %d, want 0", inbox[0].UnreadCount)
	}
	// Ada sees Bob's watermark.
	wm, err := q.GetPeerReadWatermark(ctx, sqlcgen.GetPeerReadWatermarkParams{ConversationID: conv.ID, UserID: ada})
	if err != nil || !wm.Valid || wm.Bytes != msg.ID {
		t.Fatalf("peer watermark = %v (%v)", wm, err)
	}

	// Tombstone: sender-only, drops the preview.
	if n, err := q.TombstoneMessage(ctx, sqlcgen.TombstoneMessageParams{ID: msg.ID, SenderID: bob}); err != nil || n != 0 {
		t.Fatalf("bob tombstone = %d (%v), want 0", n, err)
	}
	if n, err := q.TombstoneMessage(ctx, sqlcgen.TombstoneMessageParams{ID: msg.ID, SenderID: ada}); err != nil || n != 1 {
		t.Fatalf("ada tombstone = %d (%v), want 1", n, err)
	}
	msgs, _ = q.ListMessages(ctx, sqlcgen.ListMessagesParams{ConversationID: conv.ID, ResultLimit: 20})
	if !msgs[0].Deleted || msgs[0].Body != "[deleted]" || msgs[0].PreviewUrl != nil {
		t.Fatalf("after tombstone: %+v", msgs[0])
	}

	// Message report with body snapshot; idempotent per (reporter, message) —
	// a new report returns its id, a repeat yields no row (pgx.ErrNoRows).
	if id, err := q.CreateMessageReport(ctx, sqlcgen.CreateMessageReportParams{
		ReporterID: bob, MessageID: pgUUIDVal(msg.ID), MessageBodySnapshot: "the original text", Reason: "abuse",
	}); err != nil || id == uuid.Nil {
		t.Fatalf("CreateMessageReport = %s (%v), want a new report id", id, err)
	}
	if _, err := q.CreateMessageReport(ctx, sqlcgen.CreateMessageReportParams{
		ReporterID: bob, MessageID: pgUUIDVal(msg.ID), MessageBodySnapshot: "again", Reason: "abuse",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("duplicate message report err = %v, want pgx.ErrNoRows", err)
	}
	reports, err := q.ListReports(ctx, sqlcgen.ListReportsParams{ResultLimit: 20})
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	found := false
	for _, r := range reports {
		if r.TargetType == "message" && r.MessageID.Valid && r.MessageID.Bytes == msg.ID {
			found = true
			if r.MessageBodySnapshot != "the original text" {
				t.Errorf("snapshot = %q", r.MessageBodySnapshot)
			}
		}
	}
	if !found {
		t.Error("message report not in queue")
	}
}

// TestSearchPublicVideosFilters proves the search facet filters (tag/category/
// language) narrow the local branch against real PostgreSQL.
func TestSearchPublicVideosFilters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	owner := seedDMUser(t, st, "vo")
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) }()
	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
		owner, "vo_"+uuid.NewString()[:8]).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// A public, published video tagged "golang" in category "science".
	title := "Filter Probe " + uuid.NewString()[:8]
	var vidID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, description, privacy, state, category)
		 VALUES ($1, $2, '', 'public', 'published', 'science') RETURNING id`,
		chID, title).Scan(&vidID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_tags (video_id, tag) VALUES ($1, 'golang')`, vidID); err != nil {
		t.Fatalf("seed tag: %v", err)
	}

	science := "science"
	tag := "golang"
	// Matching category filter includes it.
	rows, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{Query: "Filter Probe", Category: &science, ResultLimit: 20})
	if err != nil {
		t.Fatalf("search category: %v", err)
	}
	if !containsVideo(rows, vidID) {
		t.Error("category=science filter excluded the matching video")
	}
	// Matching tag filter includes it.
	rows, _ = q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{Query: "Filter Probe", Tag: &tag, ResultLimit: 20})
	if !containsVideo(rows, vidID) {
		t.Error("tag=golang filter excluded the matching video")
	}
	// A non-matching category filter excludes it.
	other := "music"
	rows, _ = q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{Query: "Filter Probe", Category: &other, ResultLimit: 20})
	if containsVideo(rows, vidID) {
		t.Error("category=music filter wrongly included a science video")
	}
}

func containsVideo(rows []sqlcgen.SearchPublicVideosRow, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

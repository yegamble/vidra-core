package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	encrypted    map[uuid.UUID]bool      // conversation id -> encrypted flag (set by the e2ee fake)
	participants map[uuid.UUID]map[uuid.UUID]bool
	messages     []sqlcgen.Message
	attachments  []sqlcgen.MessageAttachment
	previews     map[string]sqlcgen.LinkPreview
	watermark    map[uuid.UUID]map[uuid.UUID]uuid.UUID
	prefs        map[uuid.UUID]map[string]bool
}

func newMessagingFakeRepo(auth *authFakeRepo) *messagingFakeRepo {
	return &messagingFakeRepo{
		auth:         auth,
		convByKey:    map[string]uuid.UUID{},
		convs:        map[uuid.UUID]time.Time{},
		encrypted:    map[uuid.UUID]bool{},
		participants: map[uuid.UUID]map[uuid.UUID]bool{},
		previews:     map[string]sqlcgen.LinkPreview{},
		watermark:    map[uuid.UUID]map[uuid.UUID]uuid.UUID{},
		prefs:        map[uuid.UUID]map[string]bool{},
	}
}

func (f *messagingFakeRepo) GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, err := f.auth.GetUserByID(ctx, id)
	if err != nil {
		return sqlcgen.User{}, pgx.ErrNoRows
	}
	return u, nil
}

// GetUserByUsername mirrors the real query: case-insensitive, active accounts
// only. A miss (unknown OR inactive) is pgx.ErrNoRows so the service maps it to
// ErrRecipientNotFound (→ 404).
func (f *messagingFakeRepo) GetUserByUsername(_ context.Context, lower string) (sqlcgen.User, error) {
	for _, u := range f.auth.users {
		if u.IsActive && strings.EqualFold(u.Username, strings.TrimSpace(lower)) {
			return u, nil
		}
	}
	return sqlcgen.User{}, pgx.ErrNoRows
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

func (f *messagingFakeRepo) CreateMessage(_ context.Context, a sqlcgen.CreateMessageParams) (sqlcgen.CreateMessageRow, error) {
	m := sqlcgen.Message{
		ID: uuid.New(), ConversationID: a.ConversationID, SenderID: a.SenderID,
		Body: a.Body, CreatedAt: time.Now(),
	}
	f.messages = append(f.messages, m)
	return sqlcgen.CreateMessageRow{ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID, Body: m.Body, CreatedAt: m.CreatedAt}, nil
}

func (f *messagingFakeRepo) mmsg(id uuid.UUID) *sqlcgen.Message {
	for i := range f.messages {
		if f.messages[i].ID == id {
			return &f.messages[i]
		}
	}
	return nil
}

func (f *messagingFakeRepo) GetMessage(_ context.Context, id uuid.UUID) (sqlcgen.GetMessageRow, error) {
	m := f.mmsg(id)
	if m == nil {
		return sqlcgen.GetMessageRow{}, pgx.ErrNoRows
	}
	return sqlcgen.GetMessageRow{
		ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID,
		Body: m.Body, CreatedAt: m.CreatedAt, DeletedAt: m.DeletedAt,
	}, nil
}

func (f *messagingFakeRepo) GetLatestMessageID(_ context.Context, convID uuid.UUID) (uuid.UUID, error) {
	var latest *sqlcgen.Message
	for i := range f.messages {
		if f.messages[i].ConversationID == convID && (latest == nil || f.messages[i].CreatedAt.After(latest.CreatedAt)) {
			latest = &f.messages[i]
		}
	}
	if latest == nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return latest.ID, nil
}

func (f *messagingFakeRepo) SetLastReadMessage(_ context.Context, a sqlcgen.SetLastReadMessageParams) error {
	if f.watermark[a.ConversationID] == nil {
		f.watermark[a.ConversationID] = map[uuid.UUID]uuid.UUID{}
	}
	f.watermark[a.ConversationID][a.UserID] = a.MessageID.Bytes
	return nil
}

func (f *messagingFakeRepo) GetPeerReadWatermark(_ context.Context, a sqlcgen.GetPeerReadWatermarkParams) (pgtype.UUID, error) {
	for uid, w := range f.watermark[a.ConversationID] {
		if uid != a.UserID {
			return pgtype.UUID{Bytes: w, Valid: w != uuid.Nil}, nil
		}
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

func (f *messagingFakeRepo) TombstoneMessage(_ context.Context, a sqlcgen.TombstoneMessageParams) (int64, error) {
	m := f.mmsg(a.ID)
	if m == nil || m.SenderID != a.SenderID || m.DeletedAt.Valid {
		return 0, nil
	}
	m.Body = "[deleted]"
	m.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	m.LinkPreviewUrlHash = nil
	return 1, nil
}

func (f *messagingFakeRepo) CreateMessageAttachment(_ context.Context, a sqlcgen.CreateMessageAttachmentParams) (sqlcgen.MessageAttachment, error) {
	att := sqlcgen.MessageAttachment{
		ID: a.ID, ConversationID: a.ConversationID, UploaderID: a.UploaderID,
		Kind: a.Kind, ContentType: a.ContentType, Filename: a.Filename, SizeBytes: a.SizeBytes,
		StorageKey: a.StorageKey, CreatedAt: time.Now(),
	}
	f.attachments = append(f.attachments, att)
	return att, nil
}

func (f *messagingFakeRepo) GetMessageAttachment(_ context.Context, id uuid.UUID) (sqlcgen.MessageAttachment, error) {
	for _, a := range f.attachments {
		if a.ID == id {
			return a, nil
		}
	}
	return sqlcgen.MessageAttachment{}, pgx.ErrNoRows
}

func (f *messagingFakeRepo) ListOwnedUnlinkedAttachments(_ context.Context, a sqlcgen.ListOwnedUnlinkedAttachmentsParams) ([]sqlcgen.ListOwnedUnlinkedAttachmentsRow, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range a.Ids {
		want[id] = true
	}
	var out []sqlcgen.ListOwnedUnlinkedAttachmentsRow
	for _, at := range f.attachments {
		if want[at.ID] && at.UploaderID == a.UploaderID && at.ConversationID == a.ConversationID && !at.MessageID.Valid {
			out = append(out, sqlcgen.ListOwnedUnlinkedAttachmentsRow{ID: at.ID, StorageKey: at.StorageKey})
		}
	}
	return out, nil
}

func (f *messagingFakeRepo) LinkAttachmentsToMessage(_ context.Context, a sqlcgen.LinkAttachmentsToMessageParams) error {
	want := map[uuid.UUID]bool{}
	for _, id := range a.Ids {
		want[id] = true
	}
	for i := range f.attachments {
		at := &f.attachments[i]
		if want[at.ID] && at.UploaderID == a.UploaderID && at.ConversationID == a.ConversationID && !at.MessageID.Valid {
			at.MessageID = a.MessageID
		}
	}
	return nil
}

func (f *messagingFakeRepo) ListAttachmentsForMessages(_ context.Context, ids []uuid.UUID) ([]sqlcgen.ListAttachmentsForMessagesRow, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []sqlcgen.ListAttachmentsForMessagesRow
	for _, at := range f.attachments {
		if at.MessageID.Valid && want[at.MessageID.Bytes] {
			out = append(out, sqlcgen.ListAttachmentsForMessagesRow{
				ID: at.ID, MessageID: at.MessageID, Kind: at.Kind,
				ContentType: at.ContentType, Filename: at.Filename, SizeBytes: at.SizeBytes,
			})
		}
	}
	return out, nil
}

func (f *messagingFakeRepo) ListAttachmentKeysByMessage(_ context.Context, mid pgtype.UUID) ([]sqlcgen.ListAttachmentKeysByMessageRow, error) {
	var out []sqlcgen.ListAttachmentKeysByMessageRow
	for _, at := range f.attachments {
		if at.MessageID == mid {
			out = append(out, sqlcgen.ListAttachmentKeysByMessageRow{ID: at.ID, StorageKey: at.StorageKey})
		}
	}
	return out, nil
}

func (f *messagingFakeRepo) DeleteAttachmentsByMessage(_ context.Context, mid pgtype.UUID) error {
	kept := f.attachments[:0]
	for _, at := range f.attachments {
		if at.MessageID != mid {
			kept = append(kept, at)
		}
	}
	f.attachments = kept
	return nil
}

func (f *messagingFakeRepo) UpsertPendingLinkPreview(_ context.Context, a sqlcgen.UpsertPendingLinkPreviewParams) error {
	if _, ok := f.previews[a.UrlHash]; !ok {
		f.previews[a.UrlHash] = sqlcgen.LinkPreview{UrlHash: a.UrlHash, Url: a.Url, State: "pending"}
	}
	return nil
}

func (f *messagingFakeRepo) SetMessageLinkPreview(_ context.Context, a sqlcgen.SetMessageLinkPreviewParams) error {
	if m := f.mmsg(a.ID); m != nil {
		m.LinkPreviewUrlHash = a.UrlHash
	}
	return nil
}

func (f *messagingFakeRepo) GetLinkPreview(_ context.Context, urlHash string) (sqlcgen.LinkPreview, error) {
	if lp, ok := f.previews[urlHash]; ok {
		return lp, nil
	}
	return sqlcgen.LinkPreview{}, pgx.ErrNoRows
}

func (f *messagingFakeRepo) ResolveLinkPreview(_ context.Context, a sqlcgen.ResolveLinkPreviewParams) error {
	lp := f.previews[a.UrlHash]
	lp.UrlHash, lp.State, lp.Title, lp.Description, lp.ImageUrl = a.UrlHash, a.State, a.Title, a.Description, a.ImageUrl
	f.previews[a.UrlHash] = lp
	return nil
}

func (f *messagingFakeRepo) IsNotificationTypeEnabled(_ context.Context, a sqlcgen.IsNotificationTypeEnabledParams) (bool, error) {
	if m, ok := f.prefs[a.UserID]; ok {
		if v, ok := m[a.Type]; ok {
			return v, nil
		}
	}
	return true, nil
}

func (f *messagingFakeRepo) UpsertNotificationPref(_ context.Context, a sqlcgen.UpsertNotificationPrefParams) error {
	if f.prefs[a.UserID] == nil {
		f.prefs[a.UserID] = map[string]bool{}
	}
	f.prefs[a.UserID][a.Type] = a.Enabled
	return nil
}

func (f *messagingFakeRepo) GetOtherParticipant(_ context.Context, a sqlcgen.GetOtherParticipantParams) (uuid.UUID, error) {
	for uid := range f.participants[a.ConversationID] {
		if uid != a.UserID {
			return uid, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
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
		row := sqlcgen.ListMessagesRow{
			ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID, Body: m.Body,
			CreatedAt: m.CreatedAt, Deleted: m.DeletedAt.Valid,
			SenderUsername: sender.Username, SenderDisplayName: sender.DisplayName,
		}
		if m.LinkPreviewUrlHash != nil {
			if lp, ok := f.previews[*m.LinkPreviewUrlHash]; ok && lp.State == "ready" {
				url, title, desc, img := lp.Url, lp.Title, lp.Description, lp.ImageUrl
				row.PreviewUrl, row.PreviewTitle, row.PreviewDescription, row.PreviewImage = &url, &title, &desc, &img
			}
		}
		rows = append(rows, row)
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
			ID: id, UpdatedAt: updated, Encrypted: f.encrypted[id], OtherUserID: other,
			OtherUsername: u.Username, OtherDisplayName: u.DisplayName, LastMessageAt: updated,
		}
		var wmAt time.Time
		if w, ok := f.watermark[id][a.UserID]; ok {
			if wm := f.mmsg(w); wm != nil {
				wmAt = wm.CreatedAt
			}
		}
		lastSet := false
		for i := len(f.messages) - 1; i >= 0; i-- {
			m := f.messages[i]
			if m.ConversationID != id {
				continue
			}
			if !lastSet {
				row.LastMessageBody = m.Body
				row.LastMessageAt = m.CreatedAt
				lastSet = true
			}
			if m.SenderID != a.UserID && m.CreatedAt.After(wmAt) {
				row.UnreadCount++
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// TestStartConversationByUsername covers naming the recipient by username as an
// alternative to recipient_id: the same conversation is created/returned as by
// id (idempotent, case-insensitive), and the exactly-one-of rule plus the same
// self/unknown/blocked outcomes as the id path hold.
func TestStartConversationByUsername(t *testing.T) {
	srv := videoServer(t)
	adaTok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Both recipient_id AND recipient_username → 422 (exactly one required).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","recipient_username":"bob"}`, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("both fields = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Neither field → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{}`, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("neither field = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Unknown username → 404 (same as an unknown recipient_id; existence not leaked).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_username":"nobody"}`, adaTok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown username = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// Username resolving to the caller → 422 (cannot message yourself).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_username":"ada"}`, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self by username = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// Ada starts with Bob by id.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start by id = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var byID conversationView
	_ = json.Unmarshal(rec.Body.Bytes(), &byID)

	// Ada starts with Bob by username (case-insensitive) → the SAME conversation.
	rec2 := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_username":"BOB"}`, adaTok)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("start by username = %d, want 201; body=%s", rec2.Code, rec2.Body.String())
	}
	var byName conversationView
	_ = json.Unmarshal(rec2.Body.Bytes(), &byName)
	if byName.ID != byID.ID {
		t.Fatalf("start-by-username mismatch: %s (id) vs %s (username)", byID.ID, byName.ID)
	}

	// Blocked-by-username → 403: a block between the two users applies after
	// resolution, exactly as it does for recipient_id.
	carolTok, _ := registerAndUser(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	_, daveID := registerAndUser(t, srv, `{"username":"dave","email":"dave@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+daveID, ``, carolTok); rec.Code != http.StatusNoContent {
		t.Fatalf("block dave = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_username":"dave"}`, carolTok); rec.Code != http.StatusForbidden {
		t.Fatalf("blocked by username = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
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

	// Bob was notified of the new message (actor ada, linking the conversation).
	var notifs notificationListResponse
	_ = json.Unmarshal(listNotifications(srv, bobTok).Body.Bytes(), &notifs)
	var msgNotif *notificationView
	for i := range notifs.Notifications {
		if notifs.Notifications[i].Type == "message" {
			msgNotif = &notifs.Notifications[i]
		}
	}
	if msgNotif == nil {
		t.Fatalf("bob has no message notification; notifications = %+v", notifs.Notifications)
	}
	if msgNotif.ConversationID != conv.ID || msgNotif.Actor == nil || msgNotif.Actor.Username != "ada" {
		t.Fatalf("message notification = %+v, want conversation %s from ada", msgNotif, conv.ID)
	}
	// Ada (the sender) is NOT notified of her own message.
	var adaNotifs notificationListResponse
	_ = json.Unmarshal(listNotifications(srv, adaTok).Body.Bytes(), &adaNotifs)
	for _, n := range adaNotifs.Notifications {
		if n.Type == "message" {
			t.Fatalf("ada should not be notified of her own message; got %+v", n)
		}
	}
}

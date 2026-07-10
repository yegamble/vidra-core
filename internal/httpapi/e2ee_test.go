package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// e2eeFakeRepo is an in-memory e2ee.Repository for handler tests. Conversation
// and participant state is shared with the messaging fake (mirroring the real
// schema, where both services read the same tables), so the type-branching in
// the conversation handlers is exercised exactly as in production.
type e2eeFakeRepo struct {
	auth    *authFakeRepo
	msg     *messagingFakeRepo
	devices map[uuid.UUID]sqlcgen.E2eeDevice
	otks    []*sqlcgen.E2eeOneTimeKey
	msgs    []sqlcgen.E2eeMessage
}

func newE2EEFakeRepo(auth *authFakeRepo, msg *messagingFakeRepo) *e2eeFakeRepo {
	return &e2eeFakeRepo{auth: auth, msg: msg, devices: map[uuid.UUID]sqlcgen.E2eeDevice{}}
}

func (f *e2eeFakeRepo) GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error) {
	return f.msg.GetUserByID(ctx, id)
}

func (f *e2eeFakeRepo) CreateE2EEConversation(_ context.Context, dmKey *string) (sqlcgen.CreateE2EEConversationRow, error) {
	if _, ok := f.msg.convByKey[*dmKey]; ok {
		return sqlcgen.CreateE2EEConversationRow{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
	}
	id := uuid.New()
	now := time.Now()
	f.msg.convByKey[*dmKey] = id
	f.msg.convs[id] = now
	f.msg.encrypted[id] = true
	return sqlcgen.CreateE2EEConversationRow{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

func (f *e2eeFakeRepo) GetE2EEConversationByDMKey(_ context.Context, dmKey *string) (sqlcgen.GetE2EEConversationByDMKeyRow, error) {
	id, ok := f.msg.convByKey[*dmKey]
	if !ok || !f.msg.encrypted[id] {
		return sqlcgen.GetE2EEConversationByDMKeyRow{}, pgx.ErrNoRows
	}
	updated := f.msg.convs[id]
	return sqlcgen.GetE2EEConversationByDMKeyRow{ID: id, CreatedAt: updated, UpdatedAt: updated}, nil
}

func (f *e2eeFakeRepo) AddConversationParticipant(ctx context.Context, a sqlcgen.AddConversationParticipantParams) error {
	return f.msg.AddConversationParticipant(ctx, a)
}

func (f *e2eeFakeRepo) GetConversationForParticipant(_ context.Context, a sqlcgen.GetConversationForParticipantParams) (bool, error) {
	if !f.msg.participants[a.ConversationID][a.UserID] {
		return false, pgx.ErrNoRows
	}
	return f.msg.encrypted[a.ConversationID], nil
}

func (f *e2eeFakeRepo) UsersShareConversation(_ context.Context, a sqlcgen.UsersShareConversationParams) (bool, error) {
	for _, members := range f.msg.participants {
		if members[a.UserA] && members[a.UserB] {
			return true, nil
		}
	}
	return false, nil
}

func (f *e2eeFakeRepo) GetOtherParticipant(ctx context.Context, a sqlcgen.GetOtherParticipantParams) (uuid.UUID, error) {
	return f.msg.GetOtherParticipant(ctx, a)
}

func (f *e2eeFakeRepo) TouchConversation(ctx context.Context, id uuid.UUID) error {
	return f.msg.TouchConversation(ctx, id)
}

func (f *e2eeFakeRepo) CreateE2EEDevice(_ context.Context, a sqlcgen.CreateE2EEDeviceParams) (sqlcgen.E2eeDevice, error) {
	d := sqlcgen.E2eeDevice{
		ID: uuid.New(), UserID: a.UserID, DeviceName: a.DeviceName,
		IdentityKey: a.IdentityKey, SigningKey: a.SigningKey,
		CreatedAt: time.Now(), LastSeenAt: time.Now(),
	}
	f.devices[d.ID] = d
	return d, nil
}

func (f *e2eeFakeRepo) GetE2EEDevice(_ context.Context, id uuid.UUID) (sqlcgen.E2eeDevice, error) {
	d, ok := f.devices[id]
	if !ok {
		return sqlcgen.E2eeDevice{}, pgx.ErrNoRows
	}
	return d, nil
}

func (f *e2eeFakeRepo) ListE2EEDevicesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.E2eeDevice, error) {
	var out []sqlcgen.E2eeDevice
	for _, d := range f.devices {
		if d.UserID == userID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *e2eeFakeRepo) CountE2EEDevicesByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, _ := f.ListE2EEDevicesByUser(ctx, userID)
	return int64(len(rows)), nil
}

func (f *e2eeFakeRepo) DeleteE2EEDevice(_ context.Context, a sqlcgen.DeleteE2EEDeviceParams) (int64, error) {
	d, ok := f.devices[a.ID]
	if !ok || d.UserID != a.UserID {
		return 0, nil
	}
	delete(f.devices, a.ID)
	// ON DELETE CASCADE
	kept := f.otks[:0]
	for _, k := range f.otks {
		if k.DeviceID != a.ID {
			kept = append(kept, k)
		}
	}
	f.otks = kept
	msgs := f.msgs[:0]
	for _, m := range f.msgs {
		if m.SenderDeviceID != a.ID && m.RecipientDeviceID != a.ID {
			msgs = append(msgs, m)
		}
	}
	f.msgs = msgs
	return 1, nil
}

func (f *e2eeFakeRepo) TouchE2EEDevice(_ context.Context, id uuid.UUID) error {
	if d, ok := f.devices[id]; ok {
		d.LastSeenAt = time.Now()
		f.devices[id] = d
	}
	return nil
}

func (f *e2eeFakeRepo) ListE2EEDevicesForConversation(_ context.Context, convID uuid.UUID) ([]sqlcgen.ListE2EEDevicesForConversationRow, error) {
	var out []sqlcgen.ListE2EEDevicesForConversationRow
	for _, d := range f.devices {
		if f.msg.participants[convID][d.UserID] {
			out = append(out, sqlcgen.ListE2EEDevicesForConversationRow{ID: d.ID, UserID: d.UserID})
		}
	}
	return out, nil
}

func (f *e2eeFakeRepo) InsertE2EEOneTimeKey(_ context.Context, a sqlcgen.InsertE2EEOneTimeKeyParams) error {
	for _, k := range f.otks {
		if k.DeviceID == a.DeviceID && k.KeyID == a.KeyID {
			return nil // ON CONFLICT DO NOTHING
		}
	}
	f.otks = append(f.otks, &sqlcgen.E2eeOneTimeKey{
		ID: uuid.New(), DeviceID: a.DeviceID, KeyID: a.KeyID, Key: a.Key, CreatedAt: time.Now(),
	})
	return nil
}

func (f *e2eeFakeRepo) CountUnclaimedE2EEOneTimeKeys(_ context.Context, deviceID uuid.UUID) (int64, error) {
	var n int64
	for _, k := range f.otks {
		if k.DeviceID == deviceID && !k.ClaimedAt.Valid {
			n++
		}
	}
	return n, nil
}

func (f *e2eeFakeRepo) ClaimE2EEOneTimeKey(_ context.Context, deviceID uuid.UUID) (sqlcgen.ClaimE2EEOneTimeKeyRow, error) {
	for _, k := range f.otks { // slice order == insertion order == oldest first
		if k.DeviceID == deviceID && !k.ClaimedAt.Valid {
			k.ClaimedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return sqlcgen.ClaimE2EEOneTimeKeyRow{ID: k.ID, DeviceID: k.DeviceID, KeyID: k.KeyID, Key: k.Key}, nil
		}
	}
	return sqlcgen.ClaimE2EEOneTimeKeyRow{}, pgx.ErrNoRows
}

func (f *e2eeFakeRepo) CreateE2EEMessage(_ context.Context, a sqlcgen.CreateE2EEMessageParams) (sqlcgen.E2eeMessage, error) {
	m := sqlcgen.E2eeMessage{
		ID: uuid.New(), ConversationID: a.ConversationID,
		SenderDeviceID: a.SenderDeviceID, RecipientDeviceID: a.RecipientDeviceID,
		MessageType: a.MessageType, Ciphertext: a.Ciphertext,
		CreatedAt: time.Now(), ExpiresAt: a.ExpiresAt,
	}
	f.msgs = append(f.msgs, m)
	return m, nil
}

func (f *e2eeFakeRepo) ListE2EEMessagesForRecipient(_ context.Context, a sqlcgen.ListE2EEMessagesForRecipientParams) ([]sqlcgen.ListE2EEMessagesForRecipientRow, error) {
	var rows []sqlcgen.ListE2EEMessagesForRecipientRow
	now := time.Now()
	for i := len(f.msgs) - 1; i >= 0; i-- { // newest first
		m := f.msgs[i]
		if m.ConversationID != a.ConversationID {
			continue
		}
		rd, ok := f.devices[m.RecipientDeviceID]
		if !ok || rd.UserID != a.UserID {
			continue
		}
		if m.ExpiresAt.Valid && !m.ExpiresAt.Time.After(now) {
			continue // expired between sweeps
		}
		rows = append(rows, sqlcgen.ListE2EEMessagesForRecipientRow{
			ID: m.ID, ConversationID: m.ConversationID,
			SenderDeviceID: m.SenderDeviceID, SenderUserID: f.devices[m.SenderDeviceID].UserID,
			RecipientDeviceID: m.RecipientDeviceID, MessageType: m.MessageType,
			Ciphertext: m.Ciphertext, CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt,
		})
	}
	return rows, nil
}

func (f *e2eeFakeRepo) GetE2EEMessageCursor(_ context.Context, a sqlcgen.GetE2EEMessageCursorParams) (time.Time, error) {
	for _, m := range f.msgs {
		if m.ID != a.ID || m.ConversationID != a.ConversationID {
			continue
		}
		if rd, ok := f.devices[m.RecipientDeviceID]; ok && rd.UserID == a.UserID {
			return m.CreatedAt, nil
		}
	}
	return time.Time{}, pgx.ErrNoRows
}

func (f *e2eeFakeRepo) ListE2EEMessagesForRecipientBefore(_ context.Context, a sqlcgen.ListE2EEMessagesForRecipientBeforeParams) ([]sqlcgen.ListE2EEMessagesForRecipientBeforeRow, error) {
	var matches []sqlcgen.E2eeMessage
	now := time.Now()
	for _, m := range f.msgs {
		if m.ConversationID != a.ConversationID {
			continue
		}
		rd, ok := f.devices[m.RecipientDeviceID]
		if !ok || rd.UserID != a.UserID {
			continue
		}
		if m.ExpiresAt.Valid && !m.ExpiresAt.Time.After(now) {
			continue
		}
		if m.CreatedAt.Before(a.BeforeCreatedAt) ||
			(m.CreatedAt.Equal(a.BeforeCreatedAt) && bytes.Compare(m.ID[:], a.BeforeID[:]) < 0) {
			matches = append(matches, m)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].CreatedAt.After(matches[j].CreatedAt)
		}
		return bytes.Compare(matches[i].ID[:], matches[j].ID[:]) > 0
	})
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(matches) {
		matches = matches[:a.ResultLimit]
	}
	rows := make([]sqlcgen.ListE2EEMessagesForRecipientBeforeRow, 0, len(matches))
	for _, m := range matches {
		rows = append(rows, sqlcgen.ListE2EEMessagesForRecipientBeforeRow{
			ID: m.ID, ConversationID: m.ConversationID,
			SenderDeviceID: m.SenderDeviceID, SenderUserID: f.devices[m.SenderDeviceID].UserID,
			RecipientDeviceID: m.RecipientDeviceID, MessageType: m.MessageType,
			Ciphertext: m.Ciphertext, CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt,
		})
	}
	return rows, nil
}

func (f *e2eeFakeRepo) SweepExpiredE2EEMessages(_ context.Context, _ int32) (int64, error) {
	now := time.Now()
	var removed int64
	kept := f.msgs[:0]
	for _, m := range f.msgs {
		if m.ExpiresAt.Valid && !m.ExpiresAt.Time.After(now) {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	f.msgs = kept
	return removed, nil
}

// --- helpers ---

// registerE2EEDevice registers a device over HTTP and returns its id.
func registerE2EEDevice(t *testing.T, srv *Server, token, name string) string {
	t.Helper()
	body := fmt.Sprintf(`{"device_name":%q,"identity_key":"idk-%s","signing_key":"sgk-%s"}`, name, name, name)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/e2ee/devices", body, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register device %s = %d; body=%s", name, rec.Code, rec.Body.String())
	}
	var v e2eeDeviceView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal device: %v", err)
	}
	return v.ID
}

// uploadOTKs uploads n one-time keys for a device.
func uploadOTKs(t *testing.T, srv *Server, token, deviceID string, n int) {
	t.Helper()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		keys = append(keys, fmt.Sprintf(`{"key_id":"AAAA%02d","key":"otk-%s-%02d"}`, i, deviceID[:8], i))
	}
	body := `{"one_time_keys":[` + strings.Join(keys, ",") + `]}`
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/e2ee/devices/"+deviceID+"/one-time-keys", body, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload OTKs = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// TestE2EEFullRoundTrip drives the complete two-user flow over HTTP with fake
// ciphertext: register devices, upload one-time keys, start an ENCRYPTED
// conversation, list the peer's devices, claim keys, send per-device
// envelopes, and read them back — asserting the §3 storage invariant that the
// ciphertext returned is byte-identical to what was posted, and that each
// reader sees ONLY their own devices' envelopes.
func TestE2EEFullRoundTrip(t *testing.T) {
	srv := videoServer(t)
	adaTok, adaID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev1 := registerE2EEDevice(t, srv, bobTok, "bob-phone")
	bobDev2 := registerE2EEDevice(t, srv, bobTok, "bob-tablet")
	uploadOTKs(t, srv, bobTok, bobDev1, 3)

	// Own device listing (also the frontend's availability probe).
	var mine e2eeDeviceListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/e2ee/devices", adaTok).Body.Bytes(), &mine)
	if len(mine.Devices) != 1 || mine.Devices[0].DeviceName != "ada-laptop" {
		t.Fatalf("ada devices = %+v, want one ada-laptop", mine.Devices)
	}

	// Start an ENCRYPTED conversation. Idempotent, flagged encrypted, and
	// distinct from any plaintext conversation between the same pair.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start encrypted = %d; body=%s", rec.Code, rec.Body.String())
	}
	var conv conversationView
	_ = json.Unmarshal(rec.Body.Bytes(), &conv)
	if !conv.Encrypted {
		t.Fatalf("conversation not flagged encrypted: %+v", conv)
	}
	if conv.OtherUserID != bobID {
		t.Fatalf("ada encrypted start other_user_id = %q, want bob %q", conv.OtherUserID, bobID)
	}
	rec2 := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+adaID+`","encrypted":true}`, bobTok)
	var conv2 conversationView
	_ = json.Unmarshal(rec2.Body.Bytes(), &conv2)
	if conv2.ID != conv.ID {
		t.Fatalf("encrypted start-or-get mismatch: %s vs %s", conv.ID, conv2.ID)
	}
	if conv2.OtherUserID != adaID {
		t.Fatalf("bob encrypted start other_user_id = %q, want ada %q", conv2.OtherUserID, adaID)
	}
	recP := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, adaTok)
	var plain conversationView
	_ = json.Unmarshal(recP.Body.Bytes(), &plain)
	if plain.ID == conv.ID || plain.Encrypted {
		t.Fatalf("plaintext conversation should be distinct and unflagged; plain=%+v encrypted=%+v", plain, conv)
	}
	if plain.OtherUserID != bobID {
		t.Fatalf("ada plaintext start other_user_id = %q, want bob %q", plain.OtherUserID, bobID)
	}

	// Ada lists Bob's devices (participant-gated) and claims one OTK per device.
	var bobDevices e2eeDeviceListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/users/"+bobID+"/e2ee/devices", adaTok).Body.Bytes(), &bobDevices)
	if len(bobDevices.Devices) != 2 {
		t.Fatalf("bob devices = %d, want 2", len(bobDevices.Devices))
	}
	claimRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+bobID+"/e2ee/claim", "", adaTok)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim = %d; body=%s", claimRec.Code, claimRec.Body.String())
	}
	var claims e2eeClaimResponse
	_ = json.Unmarshal(claimRec.Body.Bytes(), &claims)
	if len(claims.Claims) != 2 {
		t.Fatalf("claims = %d, want 2 (one per device)", len(claims.Claims))
	}
	claimedByDevice := map[string]*oneTimeKeyIn{}
	for _, cl := range claims.Claims {
		claimedByDevice[cl.DeviceID] = cl.OneTimeKey
	}
	if claimedByDevice[bobDev1] == nil {
		t.Fatalf("bobDev1 has keys uploaded; claim should return one: %+v", claims.Claims)
	}
	if claimedByDevice[bobDev2] != nil {
		t.Fatalf("bobDev2 has no keys; claim should return null, got %+v", claimedByDevice[bobDev2])
	}
	// Bob's unclaimed count dropped from 3 to 2.
	var count oneTimeKeyCountResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/e2ee/devices/"+bobDev1+"/one-time-keys/count", bobTok).Body.Bytes(), &count)
	if count.Count != 2 {
		t.Fatalf("unclaimed after claim = %d, want 2", count.Count)
	}

	// Ada sends: one envelope per Bob device (fake ciphertext, incl. exotic
	// bytes that must come back BYTE-IDENTICAL).
	cipher1 := `pk~!@#$%^&*()_+ ciphertext for bob-phone -ish`
	cipher2 := "normal ciphertext for bob-tablet ~~ 🙈 base64+/=="
	sendBody := fmt.Sprintf(
		`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":0,"ciphertext":%q},{"recipient_device_id":%q,"message_type":1,"ciphertext":%q}]}`,
		adaDev, bobDev1, cipher1, bobDev2, cipher2)
	sendRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", sendBody, adaTok)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("encrypted send = %d; body=%s", sendRec.Code, sendRec.Body.String())
	}
	var sent sendEncryptedResponse
	_ = json.Unmarshal(sendRec.Body.Bytes(), &sent)
	if sent.EnvelopeCount != 2 || sent.ExpiresAt != nil {
		t.Fatalf("send result = %+v, want 2 envelopes, no expiry", sent)
	}

	// Bob reads: BOTH his devices' envelopes, ciphertext byte-identical.
	var bobRead encryptedMessageListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/conversations/"+conv.ID+"/messages", bobTok).Body.Bytes(), &bobRead)
	if len(bobRead.Envelopes) != 2 {
		t.Fatalf("bob envelopes = %d, want 2", len(bobRead.Envelopes))
	}
	got := map[string]encryptedMessageView{}
	for _, e := range bobRead.Envelopes {
		got[e.RecipientDeviceID] = e
	}
	if got[bobDev1].Ciphertext != cipher1 || got[bobDev1].MessageType != 0 {
		t.Fatalf("bobDev1 envelope = %+v, want byte-identical ciphertext %q type 0", got[bobDev1], cipher1)
	}
	if got[bobDev2].Ciphertext != cipher2 || got[bobDev2].MessageType != 1 {
		t.Fatalf("bobDev2 envelope = %+v, want byte-identical ciphertext %q type 1", got[bobDev2], cipher2)
	}
	if got[bobDev1].SenderUserID != adaID || got[bobDev1].SenderDeviceID != adaDev {
		t.Fatalf("envelope sender = %+v, want ada/%s", got[bobDev1], adaDev)
	}

	// Ada reads: none of Bob's envelopes are hers (she has no envelope
	// addressed to her own device in this send).
	var adaRead encryptedMessageListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/conversations/"+conv.ID+"/messages", adaTok).Body.Bytes(), &adaRead)
	if len(adaRead.Envelopes) != 0 {
		t.Fatalf("ada envelopes = %d, want 0 (reads return only the caller's devices')", len(adaRead.Envelopes))
	}

	// Bob was notified (metadata only), mirroring plaintext sends.
	var notifs notificationListResponse
	_ = json.Unmarshal(listNotifications(srv, bobTok).Body.Bytes(), &notifs)
	found := false
	for _, n := range notifs.Notifications {
		if n.Type == "message" && n.ConversationID == conv.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("bob has no message notification for the encrypted conversation; got %+v", notifs.Notifications)
	}

	// The inbox flags the encrypted conversation and leaks no preview.
	var inbox conversationListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/conversations", bobTok).Body.Bytes(), &inbox)
	for _, cs := range inbox.Conversations {
		if cs.ID == conv.ID {
			if !cs.Encrypted || cs.LastMessageBody != "" {
				t.Fatalf("encrypted inbox entry = %+v, want encrypted=true, empty preview", cs)
			}
		}
	}
}

// TestE2EEShapeMismatch422s proves the conversation-type shape rules in BOTH
// directions: {body} on an encrypted conversation is 422, {envelopes} on a
// plaintext conversation is 422 — and the plaintext path is otherwise
// completely unaffected.
func TestE2EEShapeMismatch422s(t *testing.T) {
	srv := videoServer(t)
	adaTok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev := registerE2EEDevice(t, srv, bobTok, "bob-phone")

	var enc, plain conversationView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok).Body.Bytes(), &enc)
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, adaTok).Body.Bytes(), &plain)

	// {body} on the ENCRYPTED conversation → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+enc.ID+"/messages", `{"body":"plaintext leak"}`, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("body on encrypted = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// {envelopes} on the PLAINTEXT conversation → 422.
	envBody := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, adaDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+plain.ID+"/messages", envBody, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("envelopes on plaintext = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Mixing shapes is 422 regardless of type.
	mixed := fmt.Sprintf(`{"body":"hi","sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, adaDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+enc.ID+"/messages", mixed, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed shape = %d, want 422", rec.Code)
	}
	// Plaintext send still works exactly as before.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+plain.ID+"/messages", `{"body":"hi bob"}`, adaTok); rec.Code != http.StatusCreated {
		t.Fatalf("plaintext send = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// Encrypted send with an oversized ciphertext (>64KiB) → 422 (bounded opaque).
	big := strings.Repeat("A", 64*1024+1)
	tooBig := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":%q}]}`, adaDev, bobDev, big)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+enc.ID+"/messages", tooBig, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized ciphertext = %d, want 422", rec.Code)
	}
	// Bad expiry bounds → 422.
	badExpiry := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}],"expires_in_seconds":5}`, adaDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+enc.ID+"/messages", badExpiry, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expiry below 30s = %d, want 422", rec.Code)
	}
	// A valid disappearing send stamps expires_at.
	okExpiry := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}],"expires_in_seconds":30}`, adaDev, bobDev)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+enc.ID+"/messages", okExpiry, adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("disappearing send = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var sent sendEncryptedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sent)
	if sent.ExpiresAt == nil {
		t.Fatalf("disappearing send result = %+v, want expires_at set", sent)
	}
}

// TestE2EEAuthAndGating proves the auth and participant gates: anonymous 401s
// everywhere, device ownership 404s, and the peer key-directory endpoints
// (device listing, claim) are 404 for users who share no conversation.
func TestE2EEAuthAndGating(t *testing.T) {
	srv := videoServer(t)
	adaTok, adaID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	carolTok, _ := registerAndUser(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev := registerE2EEDevice(t, srv, bobTok, "bob-phone")

	// Anonymous → 401 on every E2EE route.
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/e2ee/devices"},
		{http.MethodGet, "/api/v1/e2ee/devices"},
		{http.MethodDelete, "/api/v1/e2ee/devices/" + adaDev},
		{http.MethodPost, "/api/v1/e2ee/devices/" + adaDev + "/one-time-keys"},
		{http.MethodGet, "/api/v1/e2ee/devices/" + adaDev + "/one-time-keys/count"},
		{http.MethodGet, "/api/v1/users/" + adaID + "/e2ee/devices"},
		{http.MethodPost, "/api/v1/users/" + adaID + "/e2ee/claim"},
	} {
		if rec := sendJSONAuth(srv, probe.method, probe.path, "{}", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s = %d, want 401", probe.method, probe.path, rec.Code)
		}
	}

	// Device ownership: Bob cannot delete, upload to, or count Ada's device.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/e2ee/devices/"+adaDev, "", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("delete other's device = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/e2ee/devices/"+adaDev+"/one-time-keys", `{"one_time_keys":[{"key_id":"a","key":"b"}]}`, bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("upload to other's device = %d, want 404", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/e2ee/devices/"+adaDev+"/one-time-keys/count", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("count other's device = %d, want 404", rec.Code)
	}

	// Peer gating: Carol shares no conversation with Ada → 404 (no enumeration).
	if rec := getWithAuth(srv, "/api/v1/users/"+adaID+"/e2ee/devices", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("ungated peer device listing = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+adaID+"/e2ee/claim", "", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("ungated claim = %d, want 404", rec.Code)
	}

	// Once Ada and Bob share an (encrypted) conversation, the gate opens for
	// Bob — but still not for Carol.
	var conv conversationView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok).Body.Bytes(), &conv)
	if rec := getWithAuth(srv, "/api/v1/users/"+adaID+"/e2ee/devices", bobTok); rec.Code != http.StatusOK {
		t.Fatalf("gated peer device listing after sharing = %d, want 200", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/users/"+adaID+"/e2ee/devices", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("carol still ungated = %d, want 404", rec.Code)
	}

	// Non-participant reads/sends on the encrypted conversation stay 404.
	if rec := getWithAuth(srv, "/api/v1/conversations/"+conv.ID+"/messages", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-participant encrypted read = %d, want 404", rec.Code)
	}
	sneak := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, bobDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", sneak, carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-participant encrypted send = %d, want 404", rec.Code)
	}

	// A sender device that isn't the caller's → 422; a recipient device outside
	// the conversation → 422.
	darlaTok, _ := registerAndUser(t, srv, `{"username":"darla","email":"darla@example.test","password":"supersecret"}`)
	darlaDev := registerE2EEDevice(t, srv, darlaTok, "darla-pc")
	wrongSender := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, bobDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", wrongSender, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign sender device = %d, want 422", rec.Code)
	}
	outsideRecipient := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, adaDev, darlaDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", outsideRecipient, adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("outside recipient device = %d, want 422", rec.Code)
	}

	// Device delete works for the owner and is 404 afterwards.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/e2ee/devices/"+adaDev, "", adaTok); rec.Code != http.StatusNoContent {
		t.Fatalf("own device delete = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/e2ee/devices/"+adaDev, "", adaTok); rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete = %d, want 404", rec.Code)
	}
}

// TestE2EEBlocksApplyUnchanged proves the user-block integration: a block
// (either direction) refuses starting an encrypted conversation and sending
// into an existing one, exactly like plaintext messaging — and unblocking
// restores both.
func TestE2EEBlocksApplyUnchanged(t *testing.T) {
	srv := videoServer(t)
	adaTok, adaID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev := registerE2EEDevice(t, srv, bobTok, "bob-phone")

	var conv conversationView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok).Body.Bytes(), &conv)

	// Bob blocks Ada → Ada can neither start (a new encrypted thread with him)
	// nor send into the existing one; Bob (the blocker) is refused too.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+adaID, "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok); rec.Code != http.StatusForbidden {
		t.Fatalf("blocked encrypted start = %d, want 403", rec.Code)
	}
	env := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, adaDev, bobDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", env, adaTok); rec.Code != http.StatusForbidden {
		t.Fatalf("blocked encrypted send = %d, want 403", rec.Code)
	}
	envFromBob := fmt.Sprintf(`{"sender_device_id":%q,"envelopes":[{"recipient_device_id":%q,"message_type":1,"ciphertext":"zzz"}]}`, bobDev, adaDev)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", envFromBob, bobTok); rec.Code != http.StatusForbidden {
		t.Fatalf("blocker's encrypted send = %d, want 403", rec.Code)
	}
	// The peer key directory is refused while blocked, too.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+bobID+"/e2ee/claim", "", adaTok); rec.Code != http.StatusForbidden {
		t.Fatalf("blocked claim = %d, want 403", rec.Code)
	}

	// Unblock → everything works again.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+adaID, "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", env, adaTok); rec.Code != http.StatusCreated {
		t.Fatalf("post-unblock encrypted send = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

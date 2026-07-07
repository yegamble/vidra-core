package e2ee

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository. Its clock is injectable so expiry
// filtering and sweeping are testable deterministically.
type fakeRepo struct {
	users        map[uuid.UUID]sqlcgen.User
	convByKey    map[string]uuid.UUID
	encrypted    map[uuid.UUID]bool
	participants map[uuid.UUID]map[uuid.UUID]bool
	devices      map[uuid.UUID]sqlcgen.E2eeDevice
	otks         []*sqlcgen.E2eeOneTimeKey
	msgs         []sqlcgen.E2eeMessage
	now          func() time.Time
}

func newFakeRepo(now func() time.Time) *fakeRepo {
	return &fakeRepo{
		users:        map[uuid.UUID]sqlcgen.User{},
		convByKey:    map[string]uuid.UUID{},
		encrypted:    map[uuid.UUID]bool{},
		participants: map[uuid.UUID]map[uuid.UUID]bool{},
		devices:      map[uuid.UUID]sqlcgen.E2eeDevice{},
		now:          now,
	}
}

func (f *fakeRepo) addUser() uuid.UUID {
	id := uuid.New()
	f.users[id] = sqlcgen.User{ID: id, Username: "u" + id.String()[:8]}
	return id
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlcgen.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeRepo) CreateE2EEConversation(_ context.Context, dmKey *string) (sqlcgen.CreateE2EEConversationRow, error) {
	if _, ok := f.convByKey[*dmKey]; ok {
		return sqlcgen.CreateE2EEConversationRow{}, pgx.ErrNoRows
	}
	id := uuid.New()
	f.convByKey[*dmKey] = id
	f.encrypted[id] = true
	now := f.now()
	return sqlcgen.CreateE2EEConversationRow{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

func (f *fakeRepo) GetE2EEConversationByDMKey(_ context.Context, dmKey *string) (sqlcgen.GetE2EEConversationByDMKeyRow, error) {
	id, ok := f.convByKey[*dmKey]
	if !ok || !f.encrypted[id] {
		return sqlcgen.GetE2EEConversationByDMKeyRow{}, pgx.ErrNoRows
	}
	now := f.now()
	return sqlcgen.GetE2EEConversationByDMKeyRow{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

// addPlaintextConversation seeds a NON-encrypted conversation (for shape tests).
func (f *fakeRepo) addPlaintextConversation(a, b uuid.UUID) uuid.UUID {
	id := uuid.New()
	f.encrypted[id] = false
	f.participants[id] = map[uuid.UUID]bool{a: true, b: true}
	return id
}

func (f *fakeRepo) AddConversationParticipant(_ context.Context, a sqlcgen.AddConversationParticipantParams) error {
	if f.participants[a.ConversationID] == nil {
		f.participants[a.ConversationID] = map[uuid.UUID]bool{}
	}
	f.participants[a.ConversationID][a.UserID] = true
	return nil
}

func (f *fakeRepo) GetConversationForParticipant(_ context.Context, a sqlcgen.GetConversationForParticipantParams) (bool, error) {
	if !f.participants[a.ConversationID][a.UserID] {
		return false, pgx.ErrNoRows
	}
	return f.encrypted[a.ConversationID], nil
}

func (f *fakeRepo) UsersShareConversation(_ context.Context, a sqlcgen.UsersShareConversationParams) (bool, error) {
	for _, members := range f.participants {
		if members[a.UserA] && members[a.UserB] {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) GetOtherParticipant(_ context.Context, a sqlcgen.GetOtherParticipantParams) (uuid.UUID, error) {
	for uid := range f.participants[a.ConversationID] {
		if uid != a.UserID {
			return uid, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *fakeRepo) TouchConversation(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeRepo) CreateE2EEDevice(_ context.Context, a sqlcgen.CreateE2EEDeviceParams) (sqlcgen.E2eeDevice, error) {
	d := sqlcgen.E2eeDevice{
		ID: uuid.New(), UserID: a.UserID, DeviceName: a.DeviceName,
		IdentityKey: a.IdentityKey, SigningKey: a.SigningKey,
		CreatedAt: f.now(), LastSeenAt: f.now(),
	}
	f.devices[d.ID] = d
	return d, nil
}

func (f *fakeRepo) GetE2EEDevice(_ context.Context, id uuid.UUID) (sqlcgen.E2eeDevice, error) {
	d, ok := f.devices[id]
	if !ok {
		return sqlcgen.E2eeDevice{}, pgx.ErrNoRows
	}
	return d, nil
}

func (f *fakeRepo) ListE2EEDevicesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.E2eeDevice, error) {
	var out []sqlcgen.E2eeDevice
	for _, d := range f.devices {
		if d.UserID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeRepo) CountE2EEDevicesByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, _ := f.ListE2EEDevicesByUser(ctx, userID)
	return int64(len(rows)), nil
}

func (f *fakeRepo) DeleteE2EEDevice(_ context.Context, a sqlcgen.DeleteE2EEDeviceParams) (int64, error) {
	d, ok := f.devices[a.ID]
	if !ok || d.UserID != a.UserID {
		return 0, nil
	}
	delete(f.devices, a.ID)
	return 1, nil
}

func (f *fakeRepo) TouchE2EEDevice(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeRepo) ListE2EEDevicesForConversation(_ context.Context, convID uuid.UUID) ([]sqlcgen.ListE2EEDevicesForConversationRow, error) {
	var out []sqlcgen.ListE2EEDevicesForConversationRow
	for _, d := range f.devices {
		if f.participants[convID][d.UserID] {
			out = append(out, sqlcgen.ListE2EEDevicesForConversationRow{ID: d.ID, UserID: d.UserID})
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertE2EEOneTimeKey(_ context.Context, a sqlcgen.InsertE2EEOneTimeKeyParams) error {
	for _, k := range f.otks {
		if k.DeviceID == a.DeviceID && k.KeyID == a.KeyID {
			return nil
		}
	}
	f.otks = append(f.otks, &sqlcgen.E2eeOneTimeKey{
		ID: uuid.New(), DeviceID: a.DeviceID, KeyID: a.KeyID, Key: a.Key, CreatedAt: f.now(),
	})
	return nil
}

func (f *fakeRepo) CountUnclaimedE2EEOneTimeKeys(_ context.Context, deviceID uuid.UUID) (int64, error) {
	var n int64
	for _, k := range f.otks {
		if k.DeviceID == deviceID && !k.ClaimedAt.Valid {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ClaimE2EEOneTimeKey(_ context.Context, deviceID uuid.UUID) (sqlcgen.ClaimE2EEOneTimeKeyRow, error) {
	for _, k := range f.otks {
		if k.DeviceID == deviceID && !k.ClaimedAt.Valid {
			k.ClaimedAt = pgtype.Timestamptz{Time: f.now(), Valid: true}
			return sqlcgen.ClaimE2EEOneTimeKeyRow{ID: k.ID, DeviceID: k.DeviceID, KeyID: k.KeyID, Key: k.Key}, nil
		}
	}
	return sqlcgen.ClaimE2EEOneTimeKeyRow{}, pgx.ErrNoRows
}

func (f *fakeRepo) CreateE2EEMessage(_ context.Context, a sqlcgen.CreateE2EEMessageParams) (sqlcgen.E2eeMessage, error) {
	m := sqlcgen.E2eeMessage{
		ID: uuid.New(), ConversationID: a.ConversationID,
		SenderDeviceID: a.SenderDeviceID, RecipientDeviceID: a.RecipientDeviceID,
		MessageType: a.MessageType, Ciphertext: a.Ciphertext,
		CreatedAt: f.now(), ExpiresAt: a.ExpiresAt,
	}
	f.msgs = append(f.msgs, m)
	return m, nil
}

func (f *fakeRepo) ListE2EEMessagesForRecipient(_ context.Context, a sqlcgen.ListE2EEMessagesForRecipientParams) ([]sqlcgen.ListE2EEMessagesForRecipientRow, error) {
	var rows []sqlcgen.ListE2EEMessagesForRecipientRow
	now := f.now()
	for i := len(f.msgs) - 1; i >= 0; i-- {
		m := f.msgs[i]
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
		rows = append(rows, sqlcgen.ListE2EEMessagesForRecipientRow{
			ID: m.ID, ConversationID: m.ConversationID,
			SenderDeviceID: m.SenderDeviceID, SenderUserID: f.devices[m.SenderDeviceID].UserID,
			RecipientDeviceID: m.RecipientDeviceID, MessageType: m.MessageType,
			Ciphertext: m.Ciphertext, CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt,
		})
	}
	return rows, nil
}

func (f *fakeRepo) GetE2EEMessageCursor(_ context.Context, a sqlcgen.GetE2EEMessageCursorParams) (time.Time, error) {
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

func (f *fakeRepo) ListE2EEMessagesForRecipientBefore(_ context.Context, a sqlcgen.ListE2EEMessagesForRecipientBeforeParams) ([]sqlcgen.ListE2EEMessagesForRecipientBeforeRow, error) {
	var matches []sqlcgen.E2eeMessage
	now := f.now()
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

func (f *fakeRepo) SweepExpiredE2EEMessages(_ context.Context, _ int32) (int64, error) {
	now := f.now()
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

// fakeBlocker blocks a fixed pair (both directions).
type fakeBlocker struct{ a, b uuid.UUID }

func (f fakeBlocker) IsBlockedBetween(_ context.Context, x, y uuid.UUID) (bool, error) {
	return (x == f.a && y == f.b) || (x == f.b && y == f.a), nil
}

// mustDevice registers a valid device or fails the test.
func mustDevice(t *testing.T, svc *Service, userID uuid.UUID, name string) Device {
	t.Helper()
	d, err := svc.RegisterDevice(context.Background(), userID, name, "idk-"+name, "sgk-"+name)
	if err != nil {
		t.Fatalf("RegisterDevice(%s): %v", name, err)
	}
	return d
}

func TestStartConversationEncrypted(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(time.Now)
	svc := NewService(repo)
	ada, bob := repo.addUser(), repo.addUser()

	if _, err := svc.StartConversation(ctx, ada, ada); !errors.Is(err, ErrCannotMessageSelf) {
		t.Fatalf("self = %v, want ErrCannotMessageSelf", err)
	}
	if _, err := svc.StartConversation(ctx, ada, uuid.New()); !errors.Is(err, ErrRecipientNotFound) {
		t.Fatalf("unknown = %v, want ErrRecipientNotFound", err)
	}

	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Idempotent from either side.
	again, err := svc.StartConversation(ctx, bob, ada)
	if err != nil || again.ID != conv.ID {
		t.Fatalf("start-or-get = (%v, %v), want same id %v", again.ID, err, conv.ID)
	}
	// The dm_key lives in the "e2ee:" namespace, distinct from plaintext.
	for key := range repo.convByKey {
		if !strings.HasPrefix(key, "e2ee:") {
			t.Fatalf("dm_key %q not in the e2ee: namespace", key)
		}
	}
	// Both are participants; the conversation reports encrypted.
	for _, u := range []uuid.UUID{ada, bob} {
		enc, err := svc.ConversationEncrypted(ctx, u, conv.ID)
		if err != nil || !enc {
			t.Fatalf("ConversationEncrypted(%v) = (%v, %v), want (true, nil)", u, enc, err)
		}
	}
	// An outsider is ErrNotParticipant (no existence leak).
	carol := repo.addUser()
	if _, err := svc.ConversationEncrypted(ctx, carol, conv.ID); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("outsider = %v, want ErrNotParticipant", err)
	}

	// Blocks refuse the start (either direction).
	blocked := NewService(repo, WithBlocker(fakeBlocker{a: ada, b: bob}))
	if _, err := blocked.StartConversation(ctx, ada, bob); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked start = %v, want ErrBlocked", err)
	}
}

func TestDeviceRegistrationRules(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(time.Now)
	svc := NewService(repo)
	ada := repo.addUser()

	var invalid *InvalidError
	if _, err := svc.RegisterDevice(ctx, ada, "", "idk", "sgk"); !errors.As(err, &invalid) {
		t.Fatalf("empty name = %v, want InvalidError", err)
	}
	if _, err := svc.RegisterDevice(ctx, ada, "d", strings.Repeat("k", maxKeyMaterialLen+1), "sgk"); !errors.As(err, &invalid) {
		t.Fatalf("oversized identity key = %v, want InvalidError", err)
	}

	for i := 0; i < maxDevicesPerUser; i++ {
		mustDevice(t, svc, ada, fmt.Sprintf("dev-%d", i))
	}
	if _, err := svc.RegisterDevice(ctx, ada, "one-too-many", "idk", "sgk"); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("device cap = %v, want ErrTooManyDevices", err)
	}

	// Delete is owner-scoped.
	devices, _ := svc.ListOwnDevices(ctx, ada)
	bob := repo.addUser()
	if err := svc.DeleteDevice(ctx, bob, devices[0].ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("foreign delete = %v, want ErrDeviceNotFound", err)
	}
	if err := svc.DeleteDevice(ctx, ada, devices[0].ID); err != nil {
		t.Fatalf("own delete = %v", err)
	}
	if err := svc.DeleteDevice(ctx, ada, devices[0].ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("re-delete = %v, want ErrDeviceNotFound", err)
	}
}

func TestOneTimeKeyUploadCountClaim(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(time.Now)
	svc := NewService(repo)
	ada, bob := repo.addUser(), repo.addUser()
	adaDev := mustDevice(t, svc, ada, "ada-laptop")

	// Ownership gates upload/count.
	if _, err := svc.UploadOneTimeKeys(ctx, bob, adaDev.ID, []OneTimeKey{{KeyID: "a", Key: "k"}}); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("foreign upload = %v, want ErrDeviceNotFound", err)
	}
	if _, err := svc.CountOneTimeKeys(ctx, bob, adaDev.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("foreign count = %v, want ErrDeviceNotFound", err)
	}

	// Batch bounds.
	var invalid *InvalidError
	big := make([]OneTimeKey, maxOTKBatch+1)
	for i := range big {
		big[i] = OneTimeKey{KeyID: fmt.Sprintf("k%d", i), Key: "v"}
	}
	if _, err := svc.UploadOneTimeKeys(ctx, ada, adaDev.ID, big); !errors.As(err, &invalid) {
		t.Fatalf("oversized batch = %v, want InvalidError", err)
	}

	// Upload is idempotent per key_id.
	keys := []OneTimeKey{{KeyID: "AAAA", Key: "k1"}, {KeyID: "AAAB", Key: "k2"}}
	if n, err := svc.UploadOneTimeKeys(ctx, ada, adaDev.ID, keys); err != nil || n != 2 {
		t.Fatalf("upload = (%d, %v), want (2, nil)", n, err)
	}
	if n, err := svc.UploadOneTimeKeys(ctx, ada, adaDev.ID, keys); err != nil || n != 2 {
		t.Fatalf("re-upload = (%d, %v), want (2, nil) — idempotent", n, err)
	}

	// The unclaimed-pool cap refuses uploads that would exceed it.
	fill := make([]OneTimeKey, maxOTKBatch)
	for i := range fill {
		fill[i] = OneTimeKey{KeyID: fmt.Sprintf("fill-%d", i), Key: "v"}
	}
	for i := 0; i < (maxUnclaimedOTKs-2)/maxOTKBatch; i++ {
		for j := range fill {
			fill[j].KeyID = fmt.Sprintf("fill-%d-%d", i, j)
		}
		if _, err := svc.UploadOneTimeKeys(ctx, ada, adaDev.ID, fill); err != nil {
			t.Fatalf("fill upload %d: %v", i, err)
		}
	}
	if _, err := svc.UploadOneTimeKeys(ctx, ada, adaDev.ID, fill); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("pool cap = %v, want ErrTooManyKeys", err)
	}

	// Claim is participant-gated: bob shares no conversation with ada.
	if _, err := svc.ClaimKeys(ctx, bob, ada); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("ungated claim = %v, want ErrNotAuthorized", err)
	}
	if _, err := svc.ListUserDevices(ctx, bob, ada); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("ungated device listing = %v, want ErrNotAuthorized", err)
	}
	// After they share a conversation, claiming works and is single-use.
	if _, err := svc.StartConversation(ctx, ada, bob); err != nil {
		t.Fatalf("start: %v", err)
	}
	before, _ := svc.CountOneTimeKeys(ctx, ada, adaDev.ID)
	claims, err := svc.ClaimKeys(ctx, bob, ada)
	if err != nil || len(claims) != 1 || claims[0].OneTimeKey == nil {
		t.Fatalf("claim = (%+v, %v), want one claim with a key", claims, err)
	}
	after, _ := svc.CountOneTimeKeys(ctx, ada, adaDev.ID)
	if after != before-1 {
		t.Fatalf("unclaimed %d -> %d, want single-use decrement", before, after)
	}
	// Claims are FIFO and never repeat.
	seen := map[string]bool{claims[0].OneTimeKey.KeyID: true}
	for i := int64(0); i < after; i++ {
		cl, err := svc.ClaimKeys(ctx, bob, ada)
		if err != nil || cl[0].OneTimeKey == nil {
			t.Fatalf("claim %d = (%+v, %v)", i, cl, err)
		}
		if seen[cl[0].OneTimeKey.KeyID] {
			t.Fatalf("key %q claimed twice", cl[0].OneTimeKey.KeyID)
		}
		seen[cl[0].OneTimeKey.KeyID] = true
	}
	// Exhausted → nil OneTimeKey, not an error.
	cl, err := svc.ClaimKeys(ctx, bob, ada)
	if err != nil || len(cl) != 1 || cl[0].OneTimeKey != nil {
		t.Fatalf("exhausted claim = (%+v, %v), want nil key", cl, err)
	}
	// Self-claim (own other devices) is allowed without a shared conversation.
	carol := repo.addUser()
	mustDevice(t, svc, carol, "carol-1")
	if _, err := svc.ClaimKeys(ctx, carol, carol); err != nil {
		t.Fatalf("self-claim = %v, want nil", err)
	}
}

func TestSendValidationAndShapes(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(time.Now)
	svc := NewService(repo)
	ada, bob := repo.addUser(), repo.addUser()
	adaDev := mustDevice(t, svc, ada, "ada-laptop")
	bobDev := mustDevice(t, svc, bob, "bob-phone")
	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	env := []Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 1, Ciphertext: "zzz"}}

	var invalid *InvalidError
	// Sending into a PLAINTEXT conversation with the envelope shape is invalid.
	plain := repo.addPlaintextConversation(ada, bob)
	if _, err := svc.Send(ctx, ada, plain, adaDev.ID, env, nil); !errors.As(err, &invalid) {
		t.Fatalf("send to plaintext = %v, want InvalidError", err)
	}
	// Non-participant → ErrNotParticipant.
	carol := repo.addUser()
	carolDev := mustDevice(t, svc, carol, "carol-pc")
	if _, err := svc.Send(ctx, carol, conv.ID, carolDev.ID, env, nil); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("outsider send = %v, want ErrNotParticipant", err)
	}
	// Foreign sender device / outside recipient device / duplicates / bad type /
	// oversized ciphertext are all invalid.
	if _, err := svc.Send(ctx, ada, conv.ID, bobDev.ID, env, nil); !errors.As(err, &invalid) {
		t.Fatalf("foreign sender device = %v, want InvalidError", err)
	}
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, []Envelope{{RecipientDeviceID: carolDev.ID, MessageType: 1, Ciphertext: "z"}}, nil); !errors.As(err, &invalid) {
		t.Fatalf("outside recipient = %v, want InvalidError", err)
	}
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, append(env, env[0]), nil); !errors.As(err, &invalid) {
		t.Fatalf("duplicate recipient = %v, want InvalidError", err)
	}
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, []Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 7, Ciphertext: "z"}}, nil); !errors.As(err, &invalid) {
		t.Fatalf("bad message_type = %v, want InvalidError", err)
	}
	huge := strings.Repeat("A", MaxCiphertextBytes+1)
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, []Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 1, Ciphertext: huge}}, nil); !errors.As(err, &invalid) {
		t.Fatalf("oversized ciphertext = %v, want InvalidError", err)
	}
	// Expiry bounds.
	for _, bad := range []int64{0, MinExpirySeconds - 1, MaxExpirySeconds + 1, -5} {
		v := bad
		if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, env, &v); !errors.As(err, &invalid) {
			t.Fatalf("expiry %d = %v, want InvalidError", bad, err)
		}
	}

	// A valid send fans out one row per recipient device, byte-identical.
	res, err := svc.Send(ctx, ada, conv.ID, adaDev.ID, env, nil)
	if err != nil || res.EnvelopeCount != 1 || res.ExpiresAt != nil {
		t.Fatalf("send = (%+v, %v)", res, err)
	}
	got, err := svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if err != nil || len(got) != 1 || got[0].Ciphertext != "zzz" || got[0].SenderUserID != ada {
		t.Fatalf("read = (%+v, %v), want the byte-identical envelope from ada", got, err)
	}
	// Reads are per-recipient-device: ada sees nothing (no envelope for her).
	mine, err := svc.ListMessages(ctx, ada, conv.ID, 10, 0)
	if err != nil || len(mine) != 0 {
		t.Fatalf("sender read = (%+v, %v), want empty", mine, err)
	}

	// Blocks refuse the send (either direction).
	blocked := NewService(repo, WithBlocker(fakeBlocker{a: ada, b: bob}))
	if _, err := blocked.Send(ctx, ada, conv.ID, adaDev.ID, env, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked send = %v, want ErrBlocked", err)
	}
}

// TestExpiryFilterAndSweeper proves both disappearing-message paths with a
// deterministic clock: reads filter an expired envelope immediately (between
// sweeps), and the sweeper hard-deletes it.
func TestExpiryFilterAndSweeper(t *testing.T) {
	ctx := context.Background()
	t0 := time.Now()
	current := t0
	clock := func() time.Time { return current }
	repo := newFakeRepo(clock)
	svc := NewService(repo, withClock(clock))
	ada, bob := repo.addUser(), repo.addUser()
	adaDev := mustDevice(t, svc, ada, "ada-laptop")
	bobDev := mustDevice(t, svc, bob, "bob-phone")
	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	expiry := int64(MinExpirySeconds)
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID,
		[]Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 1, Ciphertext: "vanishes"}}, &expiry); err != nil {
		t.Fatalf("disappearing send: %v", err)
	}
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID,
		[]Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 1, Ciphertext: "stays"}}, nil); err != nil {
		t.Fatalf("permanent send: %v", err)
	}

	// Before expiry: both visible, expires_at stamped on the disappearing one.
	got, _ := svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if len(got) != 2 {
		t.Fatalf("pre-expiry read = %d envelopes, want 2", len(got))
	}
	var sawExpiring bool
	for _, m := range got {
		if m.Ciphertext == "vanishes" {
			sawExpiring = true
			if m.ExpiresAt == nil || !m.ExpiresAt.Equal(t0.Add(time.Duration(expiry)*time.Second)) {
				t.Fatalf("expires_at = %v, want t0+%ds", m.ExpiresAt, expiry)
			}
		}
	}
	if !sawExpiring {
		t.Fatal("disappearing envelope missing from pre-expiry read")
	}

	// After expiry, BEFORE any sweep: the read filters it (correct between sweeps).
	current = t0.Add(time.Duration(expiry+1) * time.Second)
	got, _ = svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if len(got) != 1 || got[0].Ciphertext != "stays" {
		t.Fatalf("post-expiry read = %+v, want only the permanent envelope", got)
	}

	// The sweeper hard-deletes exactly the expired row.
	n, err := svc.SweepExpired(ctx, 100)
	if err != nil || n != 1 {
		t.Fatalf("sweep = (%d, %v), want (1, nil)", n, err)
	}
	if n, _ := svc.SweepExpired(ctx, 100); n != 0 {
		t.Fatalf("re-sweep = %d, want 0", n)
	}
	if len(repo.msgs) != 1 || repo.msgs[0].Ciphertext != "stays" {
		t.Fatalf("stored rows after sweep = %+v, want only the permanent envelope", repo.msgs)
	}
}

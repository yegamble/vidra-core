// Package e2ee implements the ciphertext-only encrypted-messaging backend
// (fix_plan P11.2, .ralph/specs/e2ee.md): a device key directory, one-time
// prekey upload/claim, and per-recipient-device encrypted envelopes with
// optional disappearing-message expiry. ALL cryptography is client-side (Olm);
// this package treats every payload as opaque bounded strings and stores no
// plaintext and no private keys. It is HTTP-agnostic and testable without a
// server.
package e2ee

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Bounds. Server-side sanity limits only — never cryptographic decisions.
const (
	// MaxCiphertextBytes bounds a single envelope's opaque ciphertext (§3).
	MaxCiphertextBytes = 64 * 1024
	// MinExpirySeconds / MaxExpirySeconds bound disappearing-message timers
	// (30 seconds to 90 days, §4).
	MinExpirySeconds = 30
	MaxExpirySeconds = 90 * 24 * 60 * 60

	maxDeviceName     = 100
	maxKeyMaterialLen = 512 // identity/signing/one-time public keys (base64)
	maxKeyIDLen       = 64
	maxDevicesPerUser = 20
	maxOTKBatch       = 100
	maxUnclaimedOTKs  = 500
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrCannotMessageSelf means a user tried to start an encrypted
	// conversation with themselves.
	ErrCannotMessageSelf = errors.New("e2ee: cannot message yourself")
	// ErrRecipientNotFound means the target account does not exist.
	ErrRecipientNotFound = errors.New("e2ee: recipient not found")
	// ErrNotParticipant means the caller is not a member of the conversation.
	ErrNotParticipant = errors.New("e2ee: not a participant")
	// ErrBlocked means a block exists between the two users (either direction).
	ErrBlocked = errors.New("e2ee: blocked")
	// ErrDeviceNotFound means the device does not exist or is not the caller's.
	ErrDeviceNotFound = errors.New("e2ee: device not found")
	// ErrNotAuthorized gates the peer key-directory endpoints: the caller does
	// not share a conversation with the target user (or the user is unknown).
	// Mapped to 404 so accounts/devices cannot be enumerated.
	ErrNotAuthorized = errors.New("e2ee: not authorized")
	// ErrTooManyDevices means the per-user device cap is reached.
	ErrTooManyDevices = errors.New("e2ee: too many devices")
	// ErrTooManyKeys means the per-device unclaimed one-time-key cap is reached.
	ErrTooManyKeys = errors.New("e2ee: too many one-time keys")
)

// InvalidError is a client-fixable validation problem (HTTP 422) with a safe,
// non-sensitive reason. It never echoes key material or ciphertext.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "e2ee: invalid: " + e.Reason }

// Repository is the data access the E2EE service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)

	CreateE2EEConversation(ctx context.Context, dmKey *string) (sqlcgen.CreateE2EEConversationRow, error)
	GetE2EEConversationByDMKey(ctx context.Context, dmKey *string) (sqlcgen.GetE2EEConversationByDMKeyRow, error)
	AddConversationParticipant(ctx context.Context, arg sqlcgen.AddConversationParticipantParams) error
	GetConversationForParticipant(ctx context.Context, arg sqlcgen.GetConversationForParticipantParams) (bool, error)
	UsersShareConversation(ctx context.Context, arg sqlcgen.UsersShareConversationParams) (bool, error)
	GetOtherParticipant(ctx context.Context, arg sqlcgen.GetOtherParticipantParams) (uuid.UUID, error)
	TouchConversation(ctx context.Context, id uuid.UUID) error

	CreateE2EEDevice(ctx context.Context, arg sqlcgen.CreateE2EEDeviceParams) (sqlcgen.E2eeDevice, error)
	GetE2EEDevice(ctx context.Context, id uuid.UUID) (sqlcgen.E2eeDevice, error)
	ListE2EEDevicesByUser(ctx context.Context, userID uuid.UUID) ([]sqlcgen.E2eeDevice, error)
	CountE2EEDevicesByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteE2EEDevice(ctx context.Context, arg sqlcgen.DeleteE2EEDeviceParams) (int64, error)
	TouchE2EEDevice(ctx context.Context, id uuid.UUID) error
	ListE2EEDevicesForConversation(ctx context.Context, conversationID uuid.UUID) ([]sqlcgen.ListE2EEDevicesForConversationRow, error)

	InsertE2EEOneTimeKey(ctx context.Context, arg sqlcgen.InsertE2EEOneTimeKeyParams) error
	CountUnclaimedE2EEOneTimeKeys(ctx context.Context, deviceID uuid.UUID) (int64, error)
	ClaimE2EEOneTimeKey(ctx context.Context, deviceID uuid.UUID) (sqlcgen.ClaimE2EEOneTimeKeyRow, error)

	CreateE2EEMessage(ctx context.Context, arg sqlcgen.CreateE2EEMessageParams) (sqlcgen.E2eeMessage, error)
	ListE2EEMessagesForRecipient(ctx context.Context, arg sqlcgen.ListE2EEMessagesForRecipientParams) ([]sqlcgen.ListE2EEMessagesForRecipientRow, error)
	ListE2EEMessagesForRecipientBefore(ctx context.Context, arg sqlcgen.ListE2EEMessagesForRecipientBeforeParams) ([]sqlcgen.ListE2EEMessagesForRecipientBeforeRow, error)
	GetE2EEMessageCursor(ctx context.Context, arg sqlcgen.GetE2EEMessageCursorParams) (time.Time, error)
	SweepExpiredE2EEMessages(ctx context.Context, resultLimit int32) (int64, error)
}

// Service holds the E2EE application logic.
type Service struct {
	repo    Repository
	blocker messaging.Blocker // optional; nil disables block enforcement
	now     func() time.Time  // injectable clock for expiry tests
}

// Option configures the E2EE service.
type Option func(*Service)

// WithBlocker enables block enforcement, reusing the messaging.Blocker
// contract unchanged: a block (either direction) refuses starting an
// encrypted conversation, sending, claiming keys, and listing peer devices.
func WithBlocker(b messaging.Blocker) Option {
	return func(s *Service) { s.blocker = b }
}

// withClock overrides the clock (tests only).
func withClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// NewService builds the E2EE service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Conversation is an encrypted 1:1 thread.
type Conversation struct {
	ID        uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Device is a registered E2EE device: user-labelled name plus PUBLIC key
// material (Curve25519 identity key, Ed25519 signing key).
type Device struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	DeviceName  string
	IdentityKey string
	SigningKey  string
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// OneTimeKey is a single public one-time prekey (client-assigned key_id).
type OneTimeKey struct {
	KeyID string
	Key   string
}

// Claim is the result of claiming a one-time key for one device of a peer:
// the device's public identity, plus one atomically claimed prekey —
// OneTimeKey is nil when the device has none left (the client falls back per
// its protocol rules).
type Claim struct {
	DeviceID    uuid.UUID
	IdentityKey string
	SigningKey  string
	OneTimeKey  *OneTimeKey
}

// Envelope is one per-recipient-device ciphertext blob of an encrypted send.
type Envelope struct {
	RecipientDeviceID uuid.UUID
	MessageType       int32 // Olm: 0 = prekey, 1 = normal
	Ciphertext        string
}

// EncryptedMessage is a stored envelope as returned to the recipient. The
// ciphertext is byte-identical to what the sender posted (§3 invariant).
type EncryptedMessage struct {
	ID                uuid.UUID
	ConversationID    uuid.UUID
	SenderUserID      uuid.UUID
	SenderDeviceID    uuid.UUID
	RecipientDeviceID uuid.UUID
	MessageType       int32
	Ciphertext        string
	CreatedAt         time.Time
	ExpiresAt         *time.Time
}

// SendResult summarises an encrypted send (the envelopes themselves are not
// echoed back; the sender already has them).
type SendResult struct {
	ConversationID uuid.UUID
	EnvelopeCount  int
	CreatedAt      time.Time
	ExpiresAt      *time.Time
}

// isBlocked reports whether a block exists between the two users. Always false
// when no Blocker is wired.
func (s *Service) isBlocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if s.blocker == nil {
		return false, nil
	}
	return s.blocker.IsBlockedBetween(ctx, a, b)
}

// dmKey is the canonical, order-independent key for an encrypted 1:1
// conversation. The "e2ee:" namespace keeps it distinct from the plaintext
// conversation between the same pair, so a pair can have both.
func dmKey(a, b uuid.UUID) string {
	x, y := a.String(), b.String()
	if x > y {
		x, y = y, x
	}
	return "e2ee:" + x + ":" + y
}

// StartConversation returns the encrypted 1:1 conversation between the caller
// and the recipient, creating it (with both participants) if it does not
// exist. Mirrors messaging.StartConversation, in the encrypted dm_key
// namespace. Messaging yourself → ErrCannotMessageSelf; unknown recipient →
// ErrRecipientNotFound; a block either direction → ErrBlocked.
func (s *Service) StartConversation(ctx context.Context, meID, recipientID uuid.UUID) (Conversation, error) {
	if meID == recipientID {
		return Conversation{}, ErrCannotMessageSelf
	}
	if _, err := s.repo.GetUserByID(ctx, recipientID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrRecipientNotFound
		}
		return Conversation{}, err
	}
	if blocked, err := s.isBlocked(ctx, meID, recipientID); err != nil {
		return Conversation{}, err
	} else if blocked {
		return Conversation{}, ErrBlocked
	}

	key := dmKey(meID, recipientID)
	var conv Conversation
	row, err := s.repo.CreateE2EEConversation(ctx, &key)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // already exists
		existing, gerr := s.repo.GetE2EEConversationByDMKey(ctx, &key)
		if gerr != nil {
			return Conversation{}, gerr
		}
		conv = Conversation{ID: existing.ID, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt}
	case err != nil:
		return Conversation{}, err
	default:
		conv = Conversation{ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}

	for _, uid := range []uuid.UUID{meID, recipientID} {
		if err := s.repo.AddConversationParticipant(ctx, sqlcgen.AddConversationParticipantParams{
			ConversationID: conv.ID,
			UserID:         uid,
		}); err != nil {
			return Conversation{}, err
		}
	}
	return conv, nil
}

// ConversationEncrypted reports whether a conversation the caller participates
// in is encrypted. A non-participant (or unknown conversation) gets
// ErrNotParticipant so existence is not leaked.
func (s *Service) ConversationEncrypted(ctx context.Context, meID, conversationID uuid.UUID) (bool, error) {
	enc, err := s.repo.GetConversationForParticipant(ctx, sqlcgen.GetConversationForParticipantParams{
		UserID:         meID,
		ConversationID: conversationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotParticipant
	}
	return enc, err
}

// RegisterDevice registers a new E2EE device for the caller: a user-visible
// name plus the device's PUBLIC identity (Curve25519) and signing (Ed25519)
// keys. Key material is opaque to the server — bounded, never validated
// cryptographically, never logged.
func (s *Service) RegisterDevice(ctx context.Context, userID uuid.UUID, name, identityKey, signingKey string) (Device, error) {
	switch {
	case name == "" || len(name) > maxDeviceName:
		return Device{}, &InvalidError{Reason: fmt.Sprintf("device_name must be 1-%d characters", maxDeviceName)}
	case identityKey == "" || len(identityKey) > maxKeyMaterialLen:
		return Device{}, &InvalidError{Reason: "identity_key is required and bounded"}
	case signingKey == "" || len(signingKey) > maxKeyMaterialLen:
		return Device{}, &InvalidError{Reason: "signing_key is required and bounded"}
	}
	n, err := s.repo.CountE2EEDevicesByUser(ctx, userID)
	if err != nil {
		return Device{}, err
	}
	if n >= maxDevicesPerUser {
		return Device{}, ErrTooManyDevices
	}
	row, err := s.repo.CreateE2EEDevice(ctx, sqlcgen.CreateE2EEDeviceParams{
		UserID:      userID,
		DeviceName:  name,
		IdentityKey: identityKey,
		SigningKey:  signingKey,
	})
	if err != nil {
		return Device{}, err
	}
	return deviceFromRow(row), nil
}

// ListOwnDevices returns the caller's registered devices, oldest first.
func (s *Service) ListOwnDevices(ctx context.Context, userID uuid.UUID) ([]Device, error) {
	rows, err := s.repo.ListE2EEDevicesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, deviceFromRow(r))
	}
	return out, nil
}

// DeleteDevice removes one of the caller's devices (cascading its one-time
// keys and envelopes). Unknown-or-not-owned → ErrDeviceNotFound (no leak).
func (s *Service) DeleteDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	n, err := s.repo.DeleteE2EEDevice(ctx, sqlcgen.DeleteE2EEDeviceParams{ID: deviceID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// ListUserDevices returns the devices (public keys) of a peer. Participant-
// gated: unless the caller IS the target, they must share at least one
// conversation, else ErrNotAuthorized (mapped to 404 — accounts and device
// counts cannot be enumerated). A block either direction → ErrBlocked.
func (s *Service) ListUserDevices(ctx context.Context, callerID, userID uuid.UUID) ([]Device, error) {
	if callerID != userID {
		if err := s.gatePeerAccess(ctx, callerID, userID); err != nil {
			return nil, err
		}
	}
	rows, err := s.repo.ListE2EEDevicesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, deviceFromRow(r))
	}
	return out, nil
}

// gatePeerAccess enforces the shared-conversation gate plus block enforcement
// for the peer key-directory endpoints (device listing, claim).
func (s *Service) gatePeerAccess(ctx context.Context, callerID, userID uuid.UUID) error {
	share, err := s.repo.UsersShareConversation(ctx, sqlcgen.UsersShareConversationParams{
		UserA: callerID,
		UserB: userID,
	})
	if err != nil {
		return err
	}
	if !share {
		return ErrNotAuthorized
	}
	if blocked, err := s.isBlocked(ctx, callerID, userID); err != nil {
		return err
	} else if blocked {
		return ErrBlocked
	}
	return nil
}

// ownDevice resolves a device and verifies the caller owns it, else
// ErrDeviceNotFound (ownership is not leaked).
func (s *Service) ownDevice(ctx context.Context, userID, deviceID uuid.UUID) (sqlcgen.E2eeDevice, error) {
	row, err := s.repo.GetE2EEDevice(ctx, deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.E2eeDevice{}, ErrDeviceNotFound
	}
	if err != nil {
		return sqlcgen.E2eeDevice{}, err
	}
	if row.UserID != userID {
		return sqlcgen.E2eeDevice{}, ErrDeviceNotFound
	}
	return row, nil
}

// UploadOneTimeKeys stores a batch of public one-time prekeys for one of the
// caller's devices (idempotent per key_id) and returns the device's unclaimed
// count afterwards. Bounded: batch ≤ 100, unclaimed pool ≤ 500.
func (s *Service) UploadOneTimeKeys(ctx context.Context, userID, deviceID uuid.UUID, keys []OneTimeKey) (int64, error) {
	if _, err := s.ownDevice(ctx, userID, deviceID); err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, &InvalidError{Reason: "one_time_keys must not be empty"}
	}
	if len(keys) > maxOTKBatch {
		return 0, &InvalidError{Reason: fmt.Sprintf("at most %d one_time_keys per upload", maxOTKBatch)}
	}
	for _, k := range keys {
		if k.KeyID == "" || len(k.KeyID) > maxKeyIDLen || k.Key == "" || len(k.Key) > maxKeyMaterialLen {
			return 0, &InvalidError{Reason: "each one-time key needs a bounded key_id and key"}
		}
	}
	unclaimed, err := s.repo.CountUnclaimedE2EEOneTimeKeys(ctx, deviceID)
	if err != nil {
		return 0, err
	}
	if unclaimed+int64(len(keys)) > maxUnclaimedOTKs {
		return 0, ErrTooManyKeys
	}
	for _, k := range keys {
		if err := s.repo.InsertE2EEOneTimeKey(ctx, sqlcgen.InsertE2EEOneTimeKeyParams{
			DeviceID: deviceID,
			KeyID:    k.KeyID,
			Key:      k.Key,
		}); err != nil {
			return 0, err
		}
	}
	// Uploading keys proves the device is alive; failures are non-fatal.
	_ = s.repo.TouchE2EEDevice(ctx, deviceID)
	return s.repo.CountUnclaimedE2EEOneTimeKeys(ctx, deviceID)
}

// CountOneTimeKeys returns the unclaimed one-time-key count of one of the
// caller's devices (so the client knows when to replenish).
func (s *Service) CountOneTimeKeys(ctx context.Context, userID, deviceID uuid.UUID) (int64, error) {
	if _, err := s.ownDevice(ctx, userID, deviceID); err != nil {
		return 0, err
	}
	return s.repo.CountUnclaimedE2EEOneTimeKeys(ctx, deviceID)
}

// ClaimKeys atomically claims one one-time key per device of the target user
// (for establishing an Olm session per device pair). Participant-gated like
// ListUserDevices; claiming your own devices' keys is allowed (a user's other
// devices need sessions too). Devices with no keys left appear with a nil
// OneTimeKey. Callers audit the claim with counts only — never key material.
func (s *Service) ClaimKeys(ctx context.Context, callerID, userID uuid.UUID) ([]Claim, error) {
	if callerID != userID {
		if err := s.gatePeerAccess(ctx, callerID, userID); err != nil {
			return nil, err
		}
	}
	devices, err := s.repo.ListE2EEDevicesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Claim, 0, len(devices))
	for _, d := range devices {
		claim := Claim{DeviceID: d.ID, IdentityKey: d.IdentityKey, SigningKey: d.SigningKey}
		row, err := s.repo.ClaimE2EEOneTimeKey(ctx, d.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// exhausted — claim.OneTimeKey stays nil
		case err != nil:
			return nil, err
		default:
			claim.OneTimeKey = &OneTimeKey{KeyID: row.KeyID, Key: row.Key}
		}
		out = append(out, claim)
	}
	return out, nil
}

// Send stores an encrypted message: one opaque envelope per recipient device,
// fanned out by the CLIENT (the server never re-encrypts). The caller must be
// a participant (else ErrNotParticipant → 404); blocks apply unchanged
// (ErrBlocked → 403); the sender device must be one of the caller's; every
// recipient device must belong to a participant of this conversation.
// expiresInSeconds (optional) bounds 30s–90d and stamps expires_at.
func (s *Service) Send(ctx context.Context, meID, conversationID, senderDeviceID uuid.UUID, envelopes []Envelope, expiresInSeconds *int64) (SendResult, error) {
	encrypted, err := s.ConversationEncrypted(ctx, meID, conversationID)
	if err != nil {
		return SendResult{}, err
	}
	if !encrypted {
		return SendResult{}, &InvalidError{Reason: "conversation is not encrypted"}
	}
	// Refuse to send if a block exists between the sender and the other
	// participant (either direction) — same rule as plaintext messaging.
	if s.blocker != nil {
		if other, oerr := s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
			ConversationID: conversationID,
			UserID:         meID,
		}); oerr == nil {
			if blocked, berr := s.blocker.IsBlockedBetween(ctx, meID, other); berr != nil {
				return SendResult{}, berr
			} else if blocked {
				return SendResult{}, ErrBlocked
			}
		}
	}
	if len(envelopes) == 0 {
		return SendResult{}, &InvalidError{Reason: "envelopes must not be empty"}
	}

	var expiresAt pgtype.Timestamptz
	var expiresPtr *time.Time
	if expiresInSeconds != nil {
		if *expiresInSeconds < MinExpirySeconds || *expiresInSeconds > MaxExpirySeconds {
			return SendResult{}, &InvalidError{Reason: "expires_in_seconds must be between 30 seconds and 90 days"}
		}
		t := s.now().Add(time.Duration(*expiresInSeconds) * time.Second)
		expiresAt = pgtype.Timestamptz{Time: t, Valid: true}
		expiresPtr = &t
	}

	// Validate the sender and every recipient against the conversation's
	// participants' devices.
	convDevices, err := s.repo.ListE2EEDevicesForConversation(ctx, conversationID)
	if err != nil {
		return SendResult{}, err
	}
	deviceOwner := make(map[uuid.UUID]uuid.UUID, len(convDevices))
	for _, d := range convDevices {
		deviceOwner[d.ID] = d.UserID
	}
	if deviceOwner[senderDeviceID] != meID {
		return SendResult{}, &InvalidError{Reason: "sender_device_id must be one of your registered devices"}
	}
	seen := make(map[uuid.UUID]bool, len(envelopes))
	for _, env := range envelopes {
		if _, ok := deviceOwner[env.RecipientDeviceID]; !ok {
			return SendResult{}, &InvalidError{Reason: "recipient_device_id does not belong to a participant of this conversation"}
		}
		if seen[env.RecipientDeviceID] {
			return SendResult{}, &InvalidError{Reason: "duplicate recipient_device_id"}
		}
		seen[env.RecipientDeviceID] = true
		if env.MessageType != 0 && env.MessageType != 1 {
			return SendResult{}, &InvalidError{Reason: "message_type must be 0 (prekey) or 1 (normal)"}
		}
		if env.Ciphertext == "" || len(env.Ciphertext) > MaxCiphertextBytes {
			return SendResult{}, &InvalidError{Reason: "ciphertext is required and at most 64KiB"}
		}
	}

	var createdAt time.Time
	for _, env := range envelopes {
		row, err := s.repo.CreateE2EEMessage(ctx, sqlcgen.CreateE2EEMessageParams{
			ConversationID:    conversationID,
			SenderDeviceID:    senderDeviceID,
			RecipientDeviceID: env.RecipientDeviceID,
			MessageType:       env.MessageType,
			Ciphertext:        env.Ciphertext,
			ExpiresAt:         expiresAt,
		})
		if err != nil {
			return SendResult{}, err
		}
		createdAt = row.CreatedAt
	}
	// Sending proves the device is alive; bump the conversation so it sorts to
	// the top of both inboxes. Both best-effort.
	_ = s.repo.TouchE2EEDevice(ctx, senderDeviceID)
	_ = s.repo.TouchConversation(ctx, conversationID)
	return SendResult{
		ConversationID: conversationID,
		EnvelopeCount:  len(envelopes),
		CreatedAt:      createdAt,
		ExpiresAt:      expiresPtr,
	}, nil
}

// ListMessages returns the conversation's envelopes addressed to the CALLER's
// devices only, newest first, with expired disappearing messages filtered
// (correct between sweeper runs). Non-participant → ErrNotParticipant.
func (s *Service) ListMessages(ctx context.Context, meID, conversationID uuid.UUID, limit, offset int32) ([]EncryptedMessage, error) {
	if _, err := s.ConversationEncrypted(ctx, meID, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListE2EEMessagesForRecipient(ctx, sqlcgen.ListE2EEMessagesForRecipientParams{
		UserID:         meID,
		ConversationID: conversationID,
		ResultLimit:    limit,
		ResultOffset:   offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]EncryptedMessage, 0, len(rows))
	for _, r := range rows {
		m := EncryptedMessage{
			ID:                r.ID,
			ConversationID:    r.ConversationID,
			SenderUserID:      r.SenderUserID,
			SenderDeviceID:    r.SenderDeviceID,
			RecipientDeviceID: r.RecipientDeviceID,
			MessageType:       r.MessageType,
			Ciphertext:        r.Ciphertext,
			CreatedAt:         r.CreatedAt,
		}
		if r.ExpiresAt.Valid {
			t := r.ExpiresAt.Time
			m.ExpiresAt = &t
		}
		out = append(out, m)
	}
	return out, nil
}

// ListMessagesBefore returns a keyset (cursor) page of the conversation's
// envelopes addressed to the CALLER's devices, strictly OLDER than beforeID,
// newest first, with expired disappearing messages filtered — so encrypted
// threads page identically to plaintext ones. Non-participant → ErrNotParticipant;
// a cursor that is unknown or not one of the caller's envelopes in this
// conversation → *InvalidError (422).
func (s *Service) ListMessagesBefore(ctx context.Context, meID, conversationID, beforeID uuid.UUID, limit int32) ([]EncryptedMessage, error) {
	if _, err := s.ConversationEncrypted(ctx, meID, conversationID); err != nil {
		return nil, err
	}
	cursorAt, err := s.repo.GetE2EEMessageCursor(ctx, sqlcgen.GetE2EEMessageCursorParams{
		UserID:         meID,
		ID:             beforeID,
		ConversationID: conversationID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &InvalidError{Reason: "before_id is not a message in this conversation"}
		}
		return nil, err
	}
	rows, err := s.repo.ListE2EEMessagesForRecipientBefore(ctx, sqlcgen.ListE2EEMessagesForRecipientBeforeParams{
		UserID:          meID,
		ConversationID:  conversationID,
		BeforeCreatedAt: cursorAt,
		BeforeID:        beforeID,
		ResultLimit:     limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]EncryptedMessage, 0, len(rows))
	for _, r := range rows {
		m := EncryptedMessage{
			ID:                r.ID,
			ConversationID:    r.ConversationID,
			SenderUserID:      r.SenderUserID,
			SenderDeviceID:    r.SenderDeviceID,
			RecipientDeviceID: r.RecipientDeviceID,
			MessageType:       r.MessageType,
			Ciphertext:        r.Ciphertext,
			CreatedAt:         r.CreatedAt,
		}
		if r.ExpiresAt.Valid {
			t := r.ExpiresAt.Time
			m.ExpiresAt = &t
		}
		out = append(out, m)
	}
	return out, nil
}

// OtherParticipant returns the other member of an encrypted 1:1 conversation.
// Used to address a new-message notification (metadata only — no content).
func (s *Service) OtherParticipant(ctx context.Context, conversationID, meID uuid.UUID) (uuid.UUID, error) {
	return s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
}

// SweepExpired hard-deletes up to limit expired disappearing messages and
// returns how many rows were removed. Intended to be called on a ticker by a
// single worker (mirrors the transcode/account-export workers).
func (s *Service) SweepExpired(ctx context.Context, limit int) (int, error) {
	n, err := s.repo.SweepExpiredE2EEMessages(ctx, int32(limit)) //nolint:gosec // limit is a small constant
	return int(n), err
}

func deviceFromRow(r sqlcgen.E2eeDevice) Device {
	return Device{
		ID:          r.ID,
		UserID:      r.UserID,
		DeviceName:  r.DeviceName,
		IdentityKey: r.IdentityKey,
		SigningKey:  r.SigningKey,
		CreatedAt:   r.CreatedAt,
		LastSeenAt:  r.LastSeenAt,
	}
}

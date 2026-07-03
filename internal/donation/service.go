package donation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// challengeTTL bounds how long a verification challenge stays valid once issued.
const challengeTTL = 10 * time.Minute

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no donation address matches the lookup.
	ErrNotFound = errors.New("donation: address not found")
	// ErrForbidden means the caller does not own the address (or channel).
	ErrForbidden = errors.New("donation: not owner")
	// ErrConflict means the same (owner, scope, network, address) already exists.
	ErrConflict = errors.New("donation: address already exists")
	// ErrInvalidNetwork means the network is outside the curated set.
	ErrInvalidNetwork = errors.New("donation: unsupported network")
	// ErrInvalidAddress means the address fails its network's shape check.
	ErrInvalidAddress = errors.New("donation: invalid address for network")
	// ErrChannelNotFound means the referenced channel does not exist.
	ErrChannelNotFound = errors.New("donation: channel not found")
	// ErrVerificationUnsupported means the network has no signing-based
	// proof-of-control path in this version.
	ErrVerificationUnsupported = errors.New("donation: address verification unsupported for this network")
	// ErrNoChallenge means verification was attempted with no active challenge.
	ErrNoChallenge = errors.New("donation: no active verification challenge")
	// ErrChallengeExpired means the challenge existed but has expired.
	ErrChallengeExpired = errors.New("donation: verification challenge expired")
	// ErrBadSignature means the submitted signature is malformed.
	ErrBadSignature = errors.New("donation: signature malformed")
	// ErrSignatureMismatch means a well-formed signature recovered a different
	// address than the one being verified.
	ErrSignatureMismatch = errors.New("donation: signature did not verify for this address")
)

// Repository is the data access the donation service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateDonationAddress(ctx context.Context, arg sqlcgen.CreateDonationAddressParams) (sqlcgen.DonationAddress, error)
	GetDonationAddress(ctx context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error)
	ListDonationAddressesByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error)
	ListAccountDonationAddresses(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error)
	ListChannelDonationAddresses(ctx context.Context, channelID pgtype.UUID) ([]sqlcgen.DonationAddress, error)
	DeleteDonationAddress(ctx context.Context, id uuid.UUID) error
	SetDonationAddressChallenge(ctx context.Context, arg sqlcgen.SetDonationAddressChallengeParams) (sqlcgen.DonationAddress, error)
	MarkDonationAddressVerified(ctx context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error)
	// GetChannelByID resolves a channel so channel-scoped addresses can be
	// authorised against the channel's owner. *sqlcgen.Queries provides it.
	GetChannelByID(ctx context.Context, id uuid.UUID) (sqlcgen.Channel, error)
}

// Service holds the donation-address application logic.
type Service struct {
	repo     Repository
	instance string
	now      func() time.Time
	newNonce func() (string, error)
}

// Option customises the Service.
type Option func(*Service)

// WithClock overrides the time source (tests use it to force challenge expiry).
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithNonceFunc overrides the challenge-nonce generator (tests use it for
// determinism). The default is a 128-bit crypto/rand hex string.
func WithNonceFunc(fn func() (string, error)) Option {
	return func(s *Service) {
		if fn != nil {
			s.newNonce = fn
		}
	}
}

// NewService builds the donation service. instance is a stable identifier for
// this Vidra deployment (its public origin/host); it is embedded verbatim in
// the verification challenge message so a signature captured on one instance
// cannot be replayed to verify the same address on another.
func NewService(repo Repository, instance string, opts ...Option) *Service {
	if strings.TrimSpace(instance) == "" {
		instance = "vidra"
	}
	s := &Service{repo: repo, instance: instance, now: time.Now, newNonce: randomNonce}
	for _, o := range opts {
		o(s)
	}
	return s
}

// AddInput is validated, normalized donation-address input.
type AddInput struct {
	// ChannelID, when non-nil, scopes the address to a channel the caller owns;
	// nil makes it an account-level (profile) address.
	ChannelID *uuid.UUID
	Network   string
	Address   string
	Label     string
}

// Add stores a new donation address for ownerID. It validates the network and
// address shape, and — for a channel-scoped address — requires the caller to
// own the channel. A duplicate maps to ErrConflict.
func (s *Service) Add(ctx context.Context, ownerID uuid.UUID, in AddInput) (sqlcgen.DonationAddress, error) {
	network := strings.ToLower(strings.TrimSpace(in.Network))
	address := strings.TrimSpace(in.Address)
	if !IsKnownNetwork(network) {
		return sqlcgen.DonationAddress{}, ErrInvalidNetwork
	}
	if !ValidAddress(network, address) {
		return sqlcgen.DonationAddress{}, ErrInvalidAddress
	}

	var channelID pgtype.UUID
	if in.ChannelID != nil {
		ch, err := s.repo.GetChannelByID(ctx, *in.ChannelID)
		if err != nil {
			return sqlcgen.DonationAddress{}, ErrChannelNotFound
		}
		if ch.OwnerID != ownerID {
			return sqlcgen.DonationAddress{}, ErrForbidden
		}
		channelID = pgtype.UUID{Bytes: *in.ChannelID, Valid: true}
	}

	row, err := s.repo.CreateDonationAddress(ctx, sqlcgen.CreateDonationAddressParams{
		OwnerID:   ownerID,
		ChannelID: channelID,
		Network:   network,
		Address:   address,
		Label:     strings.TrimSpace(in.Label),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return sqlcgen.DonationAddress{}, ErrConflict
		}
		return sqlcgen.DonationAddress{}, err
	}
	return row, nil
}

// ListOwn returns every donation address owned by the user (account-level and
// channel-scoped), oldest first — the management view.
func (s *Service) ListOwn(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	return s.repo.ListDonationAddressesByOwner(ctx, ownerID)
}

// ListForUser returns the account-level (profile) donation addresses of a user
// — the public projection for a profile page. Channel-scoped addresses are
// excluded; those belong to the channel view.
func (s *Service) ListForUser(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	return s.repo.ListAccountDonationAddresses(ctx, ownerID)
}

// ListForChannel returns a channel's donation addresses — the public projection
// for a channel page.
func (s *Service) ListForChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	return s.repo.ListChannelDonationAddresses(ctx, pgtype.UUID{Bytes: channelID, Valid: true})
}

// Delete removes a donation address. Only the owner may delete; a non-owner
// gets ErrForbidden and an unknown id gets ErrNotFound.
func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	if _, err := s.owned(ctx, ownerID, id); err != nil {
		return err
	}
	return s.repo.DeleteDonationAddress(ctx, id)
}

// Challenge issues a fresh proof-of-control challenge for an address the caller
// owns and returns the message to sign plus its expiry. It errors with
// ErrVerificationUnsupported for networks without a signing path.
func (s *Service) Challenge(ctx context.Context, ownerID, id uuid.UUID) (message string, expiresAt time.Time, err error) {
	row, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if !SupportsVerification(row.Network) {
		return "", time.Time{}, ErrVerificationUnsupported
	}
	nonce, err := s.newNonce()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = s.now().Add(challengeTTL)
	updated, err := s.repo.SetDonationAddressChallenge(ctx, sqlcgen.SetDonationAddressChallengeParams{
		ID:                    id,
		VerificationNonce:     &nonce,
		VerificationExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return s.challengeMessage(updated), expiresAt, nil
}

// Verify checks a signature against an address's active challenge. On success
// it flips the address to verified and clears the challenge; on a valid but
// wrong signature it returns ErrSignatureMismatch, and it never marks an
// address verified without a cryptographic proof.
func (s *Service) Verify(ctx context.Context, ownerID, id uuid.UUID, signature string) (sqlcgen.DonationAddress, error) {
	row, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return sqlcgen.DonationAddress{}, err
	}
	if !SupportsVerification(row.Network) {
		return sqlcgen.DonationAddress{}, ErrVerificationUnsupported
	}
	if row.VerificationNonce == nil || *row.VerificationNonce == "" {
		return sqlcgen.DonationAddress{}, ErrNoChallenge
	}
	if !row.VerificationExpiresAt.Valid || s.now().After(row.VerificationExpiresAt.Time) {
		return sqlcgen.DonationAddress{}, ErrChallengeExpired
	}
	ok, err := verifySignature(row.Network, row.Address, s.challengeMessage(row), signature)
	if err != nil {
		return sqlcgen.DonationAddress{}, err
	}
	if !ok {
		return sqlcgen.DonationAddress{}, ErrSignatureMismatch
	}
	return s.repo.MarkDonationAddressVerified(ctx, id)
}

// owned fetches an address and requires the caller to own it.
func (s *Service) owned(ctx context.Context, ownerID, id uuid.UUID) (sqlcgen.DonationAddress, error) {
	row, err := s.repo.GetDonationAddress(ctx, id)
	if err != nil {
		return sqlcgen.DonationAddress{}, ErrNotFound
	}
	if row.OwnerID != ownerID {
		return sqlcgen.DonationAddress{}, ErrForbidden
	}
	return row, nil
}

// challengeMessage builds the human-readable message the owner signs. It binds
// the proof to this instance, the network, the exact address, and a one-shot
// nonce, so a signature cannot be replayed for a different address, network, or
// instance. The nonce is a PUBLIC random challenge, never key material.
func (s *Service) challengeMessage(row sqlcgen.DonationAddress) string {
	nonce := ""
	if row.VerificationNonce != nil {
		nonce = *row.VerificationNonce
	}
	return fmt.Sprintf(
		"Vidra donation address verification\nInstance: %s\nNetwork: %s\nAddress: %s\nNonce: %s",
		s.instance, row.Network, row.Address, nonce,
	)
}

// randomNonce returns a 128-bit crypto-random hex challenge nonce.
func randomNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

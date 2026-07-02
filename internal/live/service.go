// Package live implements live-stream metadata and stream-key management for
// vidra-core: a channel owner creates a live stream and receives a private stream
// key (returned once, stored only as a SHA-256 hash). RTMP ingestion and HLS
// output are a separate later integration boundary — this package owns the
// metadata lifecycle (create / get / list / delete) and key generation/rotation.
// It is HTTP-agnostic and testable without a server or an RTMP stack.
package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no live stream matches the lookup.
	ErrNotFound = errors.New("live: not found")
)

// streamKeyBytes is the entropy of a raw stream key (256 bits).
const streamKeyBytes = 32

// Repository is the data access the live service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateLiveStream(ctx context.Context, arg sqlcgen.CreateLiveStreamParams) (sqlcgen.CreateLiveStreamRow, error)
	GetLiveStreamByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetLiveStreamByIDRow, error)
	ListLiveStreamsByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.ListLiveStreamsByChannelRow, error)
	UpdateLiveStreamKey(ctx context.Context, arg sqlcgen.UpdateLiveStreamKeyParams) error
	DeleteLiveStream(ctx context.Context, id uuid.UUID) (int64, error)
	GetLiveStreamByKeyHash(ctx context.Context, streamKeyHash string) (sqlcgen.GetLiveStreamByKeyHashRow, error)
	SetLiveStreamState(ctx context.Context, arg sqlcgen.SetLiveStreamStateParams) error
}

// Live-stream states.
const (
	StateOffline = "offline"
	StateLive    = "live"
	StateEnded   = "ended"
)

// Service holds the live-stream application logic.
type Service struct{ repo Repository }

// NewService builds the live service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Stream is a live stream's metadata. OwnerID/ChannelHandle/ChannelDisplayName
// are populated on Get (from the channel join); zero on Create/List.
type Stream struct {
	ID                 uuid.UUID
	ChannelID          uuid.UUID
	Title              string
	Description        string
	Privacy            string
	State              string
	Permanent          bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	OwnerID            uuid.UUID
	ChannelHandle      string
	ChannelDisplayName string
}

// CreateInput is the metadata for a new live stream.
type CreateInput struct {
	Title       string
	Description string
	Privacy     string
	Permanent   bool
}

// Create makes a live stream for a channel and returns it plus the raw stream key
// (shown to the caller exactly once — only its hash is stored). The caller has
// already authorised ownership of channelID.
func (s *Service) Create(ctx context.Context, channelID uuid.UUID, in CreateInput) (Stream, string, error) {
	rawKey, hash, err := generateStreamKey()
	if err != nil {
		return Stream{}, "", err
	}
	privacy := in.Privacy
	if privacy == "" {
		privacy = "public"
	}
	row, err := s.repo.CreateLiveStream(ctx, sqlcgen.CreateLiveStreamParams{
		ChannelID:     channelID,
		Title:         in.Title,
		Description:   in.Description,
		Privacy:       privacy,
		Permanent:     in.Permanent,
		StreamKeyHash: hash,
	})
	if err != nil {
		return Stream{}, "", err
	}
	return Stream{
		ID: row.ID, ChannelID: row.ChannelID, Title: row.Title, Description: row.Description,
		Privacy: row.Privacy, State: row.State, Permanent: row.Permanent,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, rawKey, nil
}

// Get returns a live stream with its owning channel's id and identity. Miss →
// ErrNotFound. Never returns the stream key.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Stream, error) {
	r, err := s.repo.GetLiveStreamByID(ctx, id)
	if err != nil {
		return Stream{}, ErrNotFound
	}
	return Stream{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		OwnerID: r.OwnerID, ChannelHandle: r.ChannelHandle, ChannelDisplayName: r.ChannelDisplayName,
	}, nil
}

// ListByChannel returns a channel's live streams, newest first.
func (s *Service) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]Stream, error) {
	rows, err := s.repo.ListLiveStreamsByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]Stream, 0, len(rows))
	for _, r := range rows {
		out = append(out, Stream{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, Permanent: r.Permanent,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// RegenerateKey rotates a stream's key and returns the new raw key (once). The
// caller has already authorised ownership.
func (s *Service) RegenerateKey(ctx context.Context, id uuid.UUID) (string, error) {
	rawKey, hash, err := generateStreamKey()
	if err != nil {
		return "", err
	}
	if err := s.repo.UpdateLiveStreamKey(ctx, sqlcgen.UpdateLiveStreamKeyParams{
		ID:            id,
		StreamKeyHash: hash,
	}); err != nil {
		return "", err
	}
	return rawKey, nil
}

// Delete removes a live stream (idempotent: deleting a missing one is a no-op).
// The caller has already authorised ownership.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.repo.DeleteLiveStream(ctx, id)
	return err
}

// StartIngest authenticates a publisher by the raw stream key it presents (the
// RTMP ingest boundary): it looks the stream up by key hash and flips it to
// "live". An unknown key → ErrNotFound (deny the publish). The HTTP layer gates
// this behind the ingest shared secret.
func (s *Service) StartIngest(ctx context.Context, rawKey string) (uuid.UUID, error) {
	row, err := s.repo.GetLiveStreamByKeyHash(ctx, hashStreamKey(rawKey))
	if err != nil {
		return uuid.Nil, ErrNotFound
	}
	if err := s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: row.ID, State: StateLive}); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// StopIngest ends a publish session for the stream identified by the raw key: a
// permanent stream returns to "offline" (reusable), a one-shot stream becomes
// "ended". An unknown key → ErrNotFound.
func (s *Service) StopIngest(ctx context.Context, rawKey string) (uuid.UUID, error) {
	row, err := s.repo.GetLiveStreamByKeyHash(ctx, hashStreamKey(rawKey))
	if err != nil {
		return uuid.Nil, ErrNotFound
	}
	next := StateEnded
	if row.Permanent {
		next = StateOffline
	}
	if err := s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: row.ID, State: next}); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

// generateStreamKey returns a new high-entropy opaque stream key and its storage
// hash. The raw key goes to the streamer (OBS) exactly once; only the hash is
// persisted. A fast hash (SHA-256) is correct for a high-entropy random token.
func generateStreamKey() (raw, hash string, err error) {
	b := make([]byte, streamKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashStreamKey(raw), nil
}

// hashStreamKey returns the hex SHA-256 of a raw stream key — the stored form and
// the ingest-lookup key.
func hashStreamKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

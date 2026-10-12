// Package feedtoken issues the per-user secret behind the private subscriptions
// feed. The token grants read access to that one feed and nothing else.
package feedtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// tokenBytes is the entropy of a raw token (256 bits).
const tokenBytes = 32

// Repository is the slice of sqlc queries the service needs.
type Repository interface {
	UpsertUserFeedToken(ctx context.Context, arg sqlcgen.UpsertUserFeedTokenParams) error
	GetActiveUserByFeedTokenHash(ctx context.Context, tokenHash string) (uuid.UUID, error)
	GetUserFeedTokenCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
	DeleteUserFeedToken(ctx context.Context, userID uuid.UUID) error
}

type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Rotate mints a fresh token for the user (replacing any previous one) and
// returns the raw value, which is never retrievable again.
func (s *Service) Rotate(ctx context.Context, userID uuid.UUID) (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	if err := s.repo.UpsertUserFeedToken(ctx, sqlcgen.UpsertUserFeedTokenParams{UserID: userID, TokenHash: hash(raw)}); err != nil {
		return "", err
	}
	return raw, nil
}

// Revoke deletes the user's token (idempotent).
func (s *Service) Revoke(ctx context.Context, userID uuid.UUID) error {
	return s.repo.DeleteUserFeedToken(ctx, userID)
}

// CreatedAt reports when the user's current token was issued; ok=false when none.
func (s *Service) CreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, bool, error) {
	t, err := s.repo.GetUserFeedTokenCreatedAt(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return t, err == nil, err
}

// Owner resolves a raw token to its (active) user; ok=false for any unknown,
// revoked or malformed token.
func (s *Service) Owner(ctx context.Context, raw string) (uuid.UUID, bool, error) {
	if raw == "" || len(raw) > 128 {
		return uuid.Nil, false, nil
	}
	id, err := s.repo.GetActiveUserByFeedTokenHash(ctx, hash(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

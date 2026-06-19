// Package video implements video publishing for vidra-core. This first slice
// covers the metadata lifecycle (create draft, read); files, transcoding, and
// playback land in later slices. It is HTTP-agnostic and testable without a
// server.
package video

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrNotFound means no video matches the lookup.
var ErrNotFound = errors.New("video: not found")

// Repository is the data access the video service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateVideo(ctx context.Context, arg sqlcgen.CreateVideoParams) (sqlcgen.Video, error)
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
}

// Service holds the video application logic.
type Service struct {
	repo Repository
}

// NewService builds the video service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// CreateInput is validated, normalized video-creation data. Privacy must already
// be one of public/unlisted/private (the HTTP layer validates and defaults it).
type CreateInput struct {
	Title       string
	Description string
	Privacy     string
}

// CreateDraft creates a new draft video under the given channel. Ownership is
// enforced by the caller (the HTTP layer checks channel ownership first).
func (s *Service) CreateDraft(ctx context.Context, channelID uuid.UUID, in CreateInput) (sqlcgen.Video, error) {
	return s.repo.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID:   channelID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Privacy:     in.Privacy,
	})
}

// GetByID returns a video joined with its owning account's id (for the caller's
// privacy/authorization decision). Miss → ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.repo.GetVideoByID(ctx, id)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, ErrNotFound
	}
	return v, nil
}

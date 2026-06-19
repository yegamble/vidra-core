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

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no video matches the lookup.
	ErrNotFound = errors.New("video: not found")
	// ErrForbidden means the caller does not own the video.
	ErrForbidden = errors.New("video: not owner")
)

// Repository is the data access the video service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateVideo(ctx context.Context, arg sqlcgen.CreateVideoParams) (sqlcgen.Video, error)
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	ListVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error)
	ListPublicVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error)
	UpdateVideo(ctx context.Context, arg sqlcgen.UpdateVideoParams) (sqlcgen.Video, error)
	DeleteVideo(ctx context.Context, id uuid.UUID) error
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

// UpdateInput is a partial video update: nil fields are left unchanged. Privacy,
// when set, is already validated by the HTTP layer.
type UpdateInput struct {
	Title       *string
	Description *string
	Privacy     *string
}

// Update changes a video's mutable metadata. Only the owner may update; a
// non-owner gets ErrForbidden and an unknown id gets ErrNotFound.
func (s *Service) Update(ctx context.Context, ownerID, id uuid.UUID, in UpdateInput) (sqlcgen.Video, error) {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.Video{}, ErrForbidden
	}
	return s.repo.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID:          id,
		Title:       trimPtr(in.Title),
		Description: trimPtr(in.Description),
		Privacy:     in.Privacy,
	})
}

// Delete removes a video. Only the owner may delete; non-owner → ErrForbidden,
// unknown id → ErrNotFound.
func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if v.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.DeleteVideo(ctx, id)
}

// ListByChannel returns every video in a channel (the owner's view), newest first.
func (s *Service) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	return s.repo.ListVideosByChannel(ctx, channelID)
}

// ListPublicByChannel returns only the channel's public videos (the anonymous
// view), newest first.
func (s *Service) ListPublicByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	return s.repo.ListPublicVideosByChannel(ctx, channelID)
}

// trimPtr trims a non-nil string pointer's value, leaving nil untouched so a
// COALESCE update skips the column.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

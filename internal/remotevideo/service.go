// Package remotevideo exposes the REST read side of federated remote videos
// (.ralph/specs/federation-remote-content.md §4-5): the remote-watch metadata
// surface and the locally cached thumbnail. Ingestion (the write side) lives in
// internal/federation. Remote videos are metadata only — playback uses the
// origin's stream_url / watch_url; Vidra never mirrors the media file. Content
// from admin-blocked instances is excluded at the query. It is HTTP-agnostic
// and testable without a server.
package remotevideo

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrNotFound means no visible remote video (or cached thumbnail) exists.
var ErrNotFound = errors.New("remotevideo: not found")

// Repository is the data access the service needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	GetRemoteVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error)
}

// Service holds the remote-video read logic.
type Service struct {
	repo  Repository
	blobs storage.Backend // serves cached thumbnails; may be nil (no thumbnails)
}

// NewService builds the remote-video read service.
func NewService(repo Repository, blobs storage.Backend) *Service {
	return &Service{repo: repo, blobs: blobs}
}

// RemoteVideo is the remote-watch read model.
type RemoteVideo struct {
	ID              uuid.UUID
	ObjectURL       string
	ActorURL        string
	Domain          string
	Title           string
	Description     string
	DurationSeconds *int32
	PublishedAt     *time.Time
	WatchURL        string
	StreamURL       *string
	HasThumbnail    bool
	FetchedAt       time.Time
	UpdatedAt       time.Time
}

// Get returns one remote video by id. Unknown — or hidden because its origin
// instance is admin-blocked — → ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (RemoteVideo, error) {
	row, err := s.repo.GetRemoteVideoByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RemoteVideo{}, ErrNotFound
		}
		return RemoteVideo{}, err
	}
	rv := RemoteVideo{
		ID:              row.ID,
		ObjectURL:       row.ObjectUrl,
		ActorURL:        row.RemoteActorUrl,
		Domain:          row.Domain,
		Title:           row.Title,
		Description:     row.Description,
		DurationSeconds: row.DurationSeconds,
		WatchURL:        row.WatchUrl,
		StreamURL:       row.StreamUrl,
		HasThumbnail:    row.ThumbnailKey != nil && *row.ThumbnailKey != "",
		FetchedAt:       row.FetchedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.PublishedAt.Valid {
		t := row.PublishedAt.Time
		rv.PublishedAt = &t
	}
	return rv, nil
}

// OpenThumbnail streams the locally cached poster of a remote video. No such
// video, no cached thumbnail, or no blob backend → ErrNotFound. The caller
// must Close the reader.
func (s *Service) OpenThumbnail(ctx context.Context, id uuid.UUID) (io.ReadCloser, error) {
	if s.blobs == nil {
		return nil, ErrNotFound
	}
	row, err := s.repo.GetRemoteVideoByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if row.ThumbnailKey == nil || *row.ThumbnailKey == "" {
		return nil, ErrNotFound
	}
	rc, err := s.blobs.Open(ctx, *row.ThumbnailKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return rc, nil
}

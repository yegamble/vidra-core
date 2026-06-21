// Package rating implements video like/dislike ratings for vidra-core. A user has
// at most one rating per video, which they can set, change, or clear. It is
// HTTP-agnostic; the HTTP layer enforces video visibility (only public, published
// videos are ratable).
package rating

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Rating values.
const (
	Like    = "like"
	Dislike = "dislike"
)

// ErrInvalidRating means the rating value is neither "like" nor "dislike".
var ErrInvalidRating = errors.New("rating: must be like or dislike")

// Repository is the data access the rating service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertVideoRating(ctx context.Context, arg sqlcgen.UpsertVideoRatingParams) error
	DeleteVideoRating(ctx context.Context, arg sqlcgen.DeleteVideoRatingParams) error
	GetVideoRating(ctx context.Context, arg sqlcgen.GetVideoRatingParams) (string, error)
	CountVideoRatings(ctx context.Context, videoID uuid.UUID) (sqlcgen.CountVideoRatingsRow, error)
}

// Service holds the rating application logic.
type Service struct {
	repo Repository
}

// NewService builds the rating service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Summary is a video's like/dislike totals plus the caller's own rating
// ("" = none / not signed in).
type Summary struct {
	Likes    int64
	Dislikes int64
	Mine     string
}

// Set records userID's rating for videoID, replacing any prior rating, and returns
// the fresh summary. An invalid value is ErrInvalidRating.
func (s *Service) Set(ctx context.Context, videoID, userID uuid.UUID, rating string) (Summary, error) {
	if rating != Like && rating != Dislike {
		return Summary{}, ErrInvalidRating
	}
	if err := s.repo.UpsertVideoRating(ctx, sqlcgen.UpsertVideoRatingParams{
		VideoID: videoID, UserID: userID, Rating: rating,
	}); err != nil {
		return Summary{}, err
	}
	return s.summary(ctx, videoID, userID, true)
}

// Clear removes userID's rating for videoID (idempotent) and returns the summary.
func (s *Service) Clear(ctx context.Context, videoID, userID uuid.UUID) (Summary, error) {
	if err := s.repo.DeleteVideoRating(ctx, sqlcgen.DeleteVideoRatingParams{
		UserID: userID, VideoID: videoID,
	}); err != nil {
		return Summary{}, err
	}
	return s.summary(ctx, videoID, userID, true)
}

// Get returns videoID's summary; the caller's own rating is included only when authed.
func (s *Service) Get(ctx context.Context, videoID, userID uuid.UUID, authed bool) (Summary, error) {
	return s.summary(ctx, videoID, userID, authed)
}

func (s *Service) summary(ctx context.Context, videoID, userID uuid.UUID, authed bool) (Summary, error) {
	counts, err := s.repo.CountVideoRatings(ctx, videoID)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Likes: counts.Likes, Dislikes: counts.Dislikes}
	if authed {
		// A missing rating (no row) leaves Mine = "".
		if mine, err := s.repo.GetVideoRating(ctx, sqlcgen.GetVideoRatingParams{
			UserID: userID, VideoID: videoID,
		}); err == nil {
			out.Mine = mine
		}
	}
	return out, nil
}

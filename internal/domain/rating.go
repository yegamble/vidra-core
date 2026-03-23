package domain

import (
	"time"

	"github.com/google/uuid"
)

// RatingValue represents the rating value (-1: dislike, 0: none, 1: like)
type RatingValue int

const (
	RatingDislike RatingValue = -1
	RatingNone    RatingValue = 0
	RatingLike    RatingValue = 1
)

// VideoRating represents a user's rating on a video
type VideoRating struct {
	UserID    uuid.UUID   `json:"user_id" db:"user_id"`
	VideoID   uuid.UUID   `json:"video_id" db:"video_id"`
	Rating    RatingValue `json:"rating" db:"rating"`
	CreatedAt time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt time.Time   `json:"updated_at" db:"updated_at"`
}

// VideoRatingStats represents aggregated rating statistics for a video
type VideoRatingStats struct {
	VideoID       uuid.UUID   `json:"video_id" db:"video_id"`
	LikesCount    int         `json:"likes_count" db:"likes_count"`
	DislikesCount int         `json:"dislikes_count" db:"dislikes_count"`
	UserRating    RatingValue `json:"user_rating,omitempty"` // Current user's rating if available
}

// RateVideoRequest represents a request to rate a video
type RateVideoRequest struct {
	Rating RatingValue `json:"rating" validate:"min=-1,max=1"`
}

// IsValid checks if the rating value is valid
func (r RatingValue) IsValid() bool {
	return r >= -1 && r <= 1
}

// String returns the string representation of the rating
func (r RatingValue) String() string {
	switch r {
	case RatingDislike:
		return "dislike"
	case RatingLike:
		return "like"
	default:
		return "none"
	}
}

package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRatingValue_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		rating RatingValue
		want   bool
	}{
		{"dislike is valid", RatingDislike, true},
		{"none is valid", RatingNone, true},
		{"like is valid", RatingLike, true},
		{"negative 2 is invalid", RatingValue(-2), false},
		{"positive 2 is invalid", RatingValue(2), false},
		{"large negative is invalid", RatingValue(-100), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rating.IsValid())
		})
	}
}

func TestRatingValue_String(t *testing.T) {
	tests := []struct {
		name   string
		rating RatingValue
		want   string
	}{
		{"dislike returns dislike", RatingDislike, "dislike"},
		{"none returns none", RatingNone, "none"},
		{"like returns like", RatingLike, "like"},
		{"out of range defaults to none", RatingValue(99), "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rating.String())
		})
	}
}

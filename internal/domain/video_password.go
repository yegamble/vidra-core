package domain

import "time"

// VideoPassword represents a password required to access a password-protected video.
type VideoPassword struct {
	ID        int64     `json:"id" db:"id"`
	VideoID   string    `json:"videoId" db:"video_id"`
	Password  string    `json:"-" db:"password_hash"` // bcrypt hashed, never serialised
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

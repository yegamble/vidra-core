package domain

import "time"

// PasswordResetToken represents a password reset token stored in the database.
type PasswordResetToken struct {
	ID        string     `db:"id"`
	UserID    string     `db:"user_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	CreatedAt time.Time  `db:"created_at"`
	UsedAt    *time.Time `db:"used_at"`
}

// IsExpired returns true if the token has passed its expiry time.
func (t *PasswordResetToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

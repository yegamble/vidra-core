package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPasswordResetToken_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"expired token (past)", time.Now().Add(-1 * time.Hour), true},
		{"valid token (future)", time.Now().Add(1 * time.Hour), false},
		{"just expired (1 second ago)", time.Now().Add(-1 * time.Second), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &PasswordResetToken{
				ID:        "test-id",
				UserID:    "user-123",
				TokenHash: "hash",
				ExpiresAt: tt.expiresAt,
				CreatedAt: time.Now().Add(-2 * time.Hour),
			}
			assert.Equal(t, tt.want, token.IsExpired())
		})
	}
}

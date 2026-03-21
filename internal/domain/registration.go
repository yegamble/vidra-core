package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserRegistration represents a pending user registration request.
type UserRegistration struct {
	ID                uuid.UUID `json:"id"                           db:"id"`
	Username          string    `json:"username"                     db:"username"`
	Email             string    `json:"email"                        db:"email"`
	ChannelName       string    `json:"channelDisplayName,omitempty" db:"channel_name"`
	Reason            string    `json:"registrationReason,omitempty" db:"reason"`
	Status            string    `json:"state"                        db:"status"`
	ModeratorResponse string    `json:"moderationResponse,omitempty" db:"moderator_response"`
	CreatedAt         time.Time `json:"createdAt"                    db:"created_at"`
}

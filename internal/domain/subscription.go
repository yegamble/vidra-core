package domain

import (
	"time"

	"github.com/google/uuid"
)

// Subscription represents a subscription relationship between a subscriber and a channel
type Subscription struct {
	ID           uuid.UUID `json:"id" db:"id"`
	SubscriberID uuid.UUID `json:"subscriber_id" db:"subscriber_id"`
	ChannelID    uuid.UUID `json:"channel_id" db:"channel_id"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

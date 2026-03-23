package domain

import (
	"time"

	"github.com/google/uuid"
)

// VideoOwnershipChangeStatus represents the state of an ownership change request.
type VideoOwnershipChangeStatus string

const (
	VideoOwnershipChangePending  VideoOwnershipChangeStatus = "waiting"
	VideoOwnershipChangeAccepted VideoOwnershipChangeStatus = "accepted"
	VideoOwnershipChangeRefused  VideoOwnershipChangeStatus = "refused"
)

// VideoOwnershipChange represents a pending video ownership transfer request.
type VideoOwnershipChange struct {
	ID          uuid.UUID                  `db:"id" json:"id"`
	VideoID     string                     `db:"video_id" json:"videoId"`
	InitiatorID string                     `db:"initiator_id" json:"initiatorId"`
	NextOwnerID string                     `db:"next_owner_id" json:"nextOwnerId"`
	Status      VideoOwnershipChangeStatus `db:"status" json:"status"`
	CreatedAt   time.Time                  `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time                  `db:"updated_at" json:"updatedAt"`
}

package domain

import "time"

// ServerFollowingState represents the state of a server following relationship.
type ServerFollowingState string

const (
	ServerFollowingStatePending  ServerFollowingState = "pending"
	ServerFollowingStateAccepted ServerFollowingState = "accepted"
	ServerFollowingStateRejected ServerFollowingState = "rejected"
)

// ServerFollowing represents an instance-to-instance following relationship.
type ServerFollowing struct {
	ID        string               `json:"id"        db:"id"`
	Host      string               `json:"host"      db:"host"`
	State     ServerFollowingState `json:"state"     db:"state"`
	Follower  bool                 `json:"follower"  db:"follower"` // true = they follow us, false = we follow them
	CreatedAt time.Time            `json:"createdAt" db:"created_at"`
}

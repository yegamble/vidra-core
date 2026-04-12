package domain

import (
	"time"

	"github.com/google/uuid"
)

// ChannelActivityAction represents the type of action performed.
type ChannelActivityAction string

const (
	ActivityVideoPublish       ChannelActivityAction = "video:publish"
	ActivityVideoUpdate        ChannelActivityAction = "video:update"
	ActivityVideoDelete        ChannelActivityAction = "video:delete"
	ActivityPlaylistAdd        ChannelActivityAction = "playlist:add"
	ActivityPlaylistRemove     ChannelActivityAction = "playlist:remove"
	ActivityCollaboratorInvite ChannelActivityAction = "collaborator:invite"
	ActivityCollaboratorAccept ChannelActivityAction = "collaborator:accept"
	ActivityCollaboratorReject ChannelActivityAction = "collaborator:reject"
	ActivityCollaboratorRemove ChannelActivityAction = "collaborator:remove"
)

// ChannelActivity represents a logged action within a channel.
type ChannelActivity struct {
	ID         uuid.UUID             `json:"id" db:"id"`
	ChannelID  uuid.UUID             `json:"channel_id" db:"channel_id"`
	UserID     uuid.UUID             `json:"user_id" db:"user_id"`
	ActionType ChannelActivityAction `json:"action_type" db:"action_type"`
	TargetType string                `json:"target_type" db:"target_type"` // e.g., "video", "playlist", "collaborator"
	TargetID   string                `json:"target_id" db:"target_id"`
	Metadata   map[string]string     `json:"metadata,omitempty" db:"metadata"`
	CreatedAt  time.Time             `json:"created_at" db:"created_at"`
}

package domain

import "time"

// ChannelSyncState represents the state of a channel sync job.
type ChannelSyncState int

const (
	ChannelSyncStateSynced          ChannelSyncState = 1
	ChannelSyncStateProcessing      ChannelSyncState = 2
	ChannelSyncStateWaitingFirstRun ChannelSyncState = 3
)

// ChannelSync represents an external channel synchronization configuration.
type ChannelSync struct {
	ID                 int64      `json:"id" db:"id"`
	ChannelID          string     `json:"channelId" db:"channel_id"`
	ExternalChannelURL string     `json:"externalChannelUrl" db:"external_channel_url"`
	State              int        `json:"state" db:"state"`
	LastSyncAt         *time.Time `json:"lastSyncAt" db:"last_sync_at"`
	CreatedAt          time.Time  `json:"createdAt" db:"created_at"`
}

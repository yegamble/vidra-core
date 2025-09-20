package domain

import (
	"database/sql"
	"encoding/json"
	"time"
)

// AbuseReportStatus represents the status of an abuse report
type AbuseReportStatus string

const (
	AbuseReportStatusPending       AbuseReportStatus = "pending"
	AbuseReportStatusAccepted      AbuseReportStatus = "accepted"
	AbuseReportStatusRejected      AbuseReportStatus = "rejected"
	AbuseReportStatusInvestigating AbuseReportStatus = "investigating"
)

// ReportedEntityType represents the type of entity being reported
type ReportedEntityType string

const (
	ReportedEntityVideo   ReportedEntityType = "video"
	ReportedEntityComment ReportedEntityType = "comment"
	ReportedEntityUser    ReportedEntityType = "user"
	ReportedEntityChannel ReportedEntityType = "channel"
)

// AbuseReport represents a report of abusive content or behavior
type AbuseReport struct {
	ID             string             `json:"id" db:"id"`
	ReporterID     string             `json:"reporter_id" db:"reporter_id"`
	Reason         string             `json:"reason" db:"reason"`
	Details        sql.NullString     `json:"details" db:"details"`
	Status         AbuseReportStatus  `json:"status" db:"status"`
	ModeratorNotes sql.NullString     `json:"moderator_notes" db:"moderator_notes"`
	ModeratedBy    sql.NullString     `json:"moderated_by" db:"moderated_by"`
	ModeratedAt    sql.NullTime       `json:"moderated_at" db:"moderated_at"`
	EntityType     ReportedEntityType `json:"entity_type" db:"reported_entity_type"`
	VideoID        sql.NullString     `json:"video_id,omitempty" db:"reported_video_id"`
	CommentID      sql.NullString     `json:"comment_id,omitempty" db:"reported_comment_id"`
	UserID         sql.NullString     `json:"user_id,omitempty" db:"reported_user_id"`
	ChannelID      sql.NullString     `json:"channel_id,omitempty" db:"reported_channel_id"`
	CreatedAt      time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at" db:"updated_at"`
}

// BlockType represents the type of block entry
type BlockType string

const (
	BlockTypeUser   BlockType = "user"
	BlockTypeDomain BlockType = "domain"
	BlockTypeIP     BlockType = "ip"
	BlockTypeEmail  BlockType = "email"
)

// BlocklistEntry represents an entry in the blocklist
type BlocklistEntry struct {
	ID           string         `json:"id" db:"id"`
	BlockType    BlockType      `json:"block_type" db:"block_type"`
	BlockedValue string         `json:"blocked_value" db:"blocked_value"`
	Reason       sql.NullString `json:"reason" db:"reason"`
	BlockedBy    string         `json:"blocked_by" db:"blocked_by"`
	ExpiresAt    sql.NullTime   `json:"expires_at" db:"expires_at"`
	IsActive     bool           `json:"is_active" db:"is_active"`
	CreatedAt    time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at" db:"updated_at"`
}

// InstanceConfig represents a configuration entry for the instance
type InstanceConfig struct {
	Key         string          `json:"key" db:"key"`
	Value       json.RawMessage `json:"value" db:"value"`
	Description sql.NullString  `json:"description,omitempty" db:"description"`
	IsPublic    bool            `json:"is_public" db:"is_public"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

// CreateAbuseReportRequest represents a request to create an abuse report
type CreateAbuseReportRequest struct {
	Reason     string             `json:"reason" validate:"required,min=3,max=1000"`
	Details    string             `json:"details,omitempty" validate:"max=5000"`
	EntityType ReportedEntityType `json:"entity_type" validate:"required,oneof=video comment user channel"`
	EntityID   string             `json:"entity_id" validate:"required,uuid"`
}

// UpdateAbuseReportRequest represents a request to update an abuse report (moderator action)
type UpdateAbuseReportRequest struct {
	Status         AbuseReportStatus `json:"status" validate:"required,oneof=pending accepted rejected investigating"`
	ModeratorNotes string            `json:"moderator_notes,omitempty" validate:"max=5000"`
}

// CreateBlocklistEntryRequest represents a request to create a blocklist entry
type CreateBlocklistEntryRequest struct {
	BlockType    BlockType  `json:"block_type" validate:"required,oneof=user domain ip email"`
	BlockedValue string     `json:"blocked_value" validate:"required,min=1,max=255"`
	Reason       string     `json:"reason,omitempty" validate:"max=1000"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// UpdateInstanceConfigRequest represents a request to update instance configuration
type UpdateInstanceConfigRequest struct {
	Value    json.RawMessage `json:"value" validate:"required"`
	IsPublic bool            `json:"is_public"`
}

// InstanceInfo represents public instance information
type InstanceInfo struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Version            string   `json:"version"`
	ContactEmail       string   `json:"contact_email"`
	TermsURL           string   `json:"terms_url,omitempty"`
	PrivacyURL         string   `json:"privacy_url,omitempty"`
	Rules              []string `json:"rules"`
	Languages          []string `json:"languages"`
	Categories         []string `json:"categories"`
	DefaultNSFWPolicy  string   `json:"default_nsfw_policy"`
	SignupEnabled      bool     `json:"signup_enabled"`
	TotalUsers         int64    `json:"total_users"`
	TotalVideos        int64    `json:"total_videos"`
	TotalLocalVideos   int64    `json:"total_local_videos"`
	TotalInstanceViews int64    `json:"total_instance_views"`
}

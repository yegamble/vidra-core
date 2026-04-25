package domain

import (
	"time"

	"github.com/google/uuid"
)

// NotificationType represents the type of notification
type NotificationType string

const (
	// NotificationNewVideo is sent when a subscribed channel uploads a new video
	NotificationNewVideo NotificationType = "new_video"
	// NotificationVideoProcessed is sent when user's own video finishes processing
	NotificationVideoProcessed NotificationType = "video_processed"
	// NotificationVideoFailed is sent when user's own video fails processing
	NotificationVideoFailed NotificationType = "video_failed"
	// NotificationNewSubscriber is sent when someone subscribes to the user's channel
	NotificationNewSubscriber NotificationType = "new_subscriber"
	// NotificationComment is sent when someone comments on user's video
	NotificationComment NotificationType = "comment"
	// NotificationMention is sent when user is mentioned in a comment
	NotificationMention NotificationType = "mention"
	// NotificationSystem is for system-wide announcements
	NotificationSystem NotificationType = "system"
	// NotificationNewMessage is sent when user receives a new message
	NotificationNewMessage NotificationType = "new_message"
	// NotificationMessageRead is sent when a message is read (optional, for read receipts)
	NotificationMessageRead NotificationType = "message_read"

	// Phase-8 payment notification types. See migration 097 for paper trail.
	// NotificationTipReceived is sent to a channel owner when a tip invoice settles.
	NotificationTipReceived NotificationType = "tip_received"
	// NotificationPayoutPendingApproval is sent to admins when a creator requests a payout.
	NotificationPayoutPendingApproval NotificationType = "payout_pending_approval"
	// NotificationPayoutApproved is sent to the requester when an admin approves a payout.
	NotificationPayoutApproved NotificationType = "payout_approved"
	// NotificationPayoutCompleted is sent to the requester when admin marks a payout executed (records txid).
	NotificationPayoutCompleted NotificationType = "payout_completed"
	// NotificationPayoutRejected is sent to the requester when admin rejects a payout (with reason).
	NotificationPayoutRejected NotificationType = "payout_rejected"
	// NotificationPayoutReady is sent by the balance worker when a creator's balance first crosses MinPayoutSats.
	NotificationPayoutReady NotificationType = "payout_ready"
	// NotificationLowBalanceStuck is sent by the balance worker when a creator's balance > 0 < MinPayoutSats for >7d.
	NotificationLowBalanceStuck NotificationType = "low_balance_stuck"
)

// Notification represents a user notification
type Notification struct {
	ID        uuid.UUID              `json:"id" db:"id"`
	UserID    uuid.UUID              `json:"user_id" db:"user_id"`
	Type      NotificationType       `json:"type" db:"type"`
	Title     string                 `json:"title" db:"title"`
	Message   string                 `json:"message" db:"message"`
	Data      map[string]interface{} `json:"data" db:"data"`
	Read      bool                   `json:"read" db:"read"`
	CreatedAt time.Time              `json:"created_at" db:"created_at"`
	ReadAt    *time.Time             `json:"read_at,omitempty" db:"read_at"`
}

// NotificationData represents structured data for notifications
type NotificationData struct {
	VideoID        *uuid.UUID `json:"video_id,omitempty"`
	ChannelID      *uuid.UUID `json:"channel_id,omitempty"`
	ChannelName    string     `json:"channel_name,omitempty"`
	VideoTitle     string     `json:"video_title,omitempty"`
	ThumbnailCID   string     `json:"thumbnail_cid,omitempty"`
	SubscriberID   *uuid.UUID `json:"subscriber_id,omitempty"`
	CommentID      *uuid.UUID `json:"comment_id,omitempty"`
	CommentText    string     `json:"comment_text,omitempty"`
	MessageID      *uuid.UUID `json:"message_id,omitempty"`
	SenderID       *uuid.UUID `json:"sender_id,omitempty"`
	SenderName     string     `json:"sender_name,omitempty"`
	MessagePreview string     `json:"message_preview,omitempty"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
}

// NotificationFilter represents filter options for listing notifications
type NotificationFilter struct {
	UserID    uuid.UUID
	Unread    *bool
	Types     []NotificationType
	Limit     int
	Offset    int
	StartDate *time.Time
	EndDate   *time.Time
}

// NotificationPreferences stores per-user notification toggle settings.
type NotificationPreferences struct {
	UserID       string `json:"user_id" db:"user_id"`
	Comment      bool   `json:"comment" db:"comment_enabled"`
	Like         bool   `json:"like" db:"like_enabled"`
	Subscribe    bool   `json:"subscribe" db:"subscribe_enabled"`
	Mention      bool   `json:"mention" db:"mention_enabled"`
	Reply        bool   `json:"reply" db:"reply_enabled"`
	Upload       bool   `json:"upload" db:"upload_enabled"`
	System       bool   `json:"system" db:"system_enabled"`
	EmailEnabled bool   `json:"email_enabled" db:"email_enabled"`
}

// DefaultNotificationPreferences returns all-enabled preferences for a user.
func DefaultNotificationPreferences(userID string) *NotificationPreferences {
	return &NotificationPreferences{
		UserID:       userID,
		Comment:      true,
		Like:         true,
		Subscribe:    true,
		Mention:      true,
		Reply:        true,
		Upload:       true,
		System:       true,
		EmailEnabled: true,
	}
}

// NotificationStats represents notification statistics for a user
type NotificationStats struct {
	TotalCount  int                      `json:"total_count"`
	UnreadCount int                      `json:"unread_count"`
	ByType      map[NotificationType]int `json:"by_type"`
}

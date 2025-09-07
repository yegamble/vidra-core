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

// NotificationStats represents notification statistics for a user
type NotificationStats struct {
	TotalCount  int                      `json:"total_count"`
	UnreadCount int                      `json:"unread_count"`
	ByType      map[NotificationType]int `json:"by_type"`
}

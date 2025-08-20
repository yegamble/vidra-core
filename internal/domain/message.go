package domain

import "time"

type Message struct {
	ID                   string     `json:"id" db:"id"`
	SenderID             string     `json:"sender_id" db:"sender_id"`
	RecipientID          string     `json:"recipient_id" db:"recipient_id"`
	Content              string     `json:"content" db:"content"`
	MessageType          string     `json:"message_type" db:"message_type"`
	IsRead               bool       `json:"is_read" db:"is_read"`
	IsDeletedBySender    bool       `json:"is_deleted_by_sender" db:"is_deleted_by_sender"`
	IsDeletedByRecipient bool       `json:"is_deleted_by_recipient" db:"is_deleted_by_recipient"`
	ParentMessageID      *string    `json:"parent_message_id,omitempty" db:"parent_message_id"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
	ReadAt               *time.Time `json:"read_at,omitempty" db:"read_at"`

	// Populated fields for API responses
	Sender        *User    `json:"sender,omitempty"`
	Recipient     *User    `json:"recipient,omitempty"`
	ParentMessage *Message `json:"parent_message,omitempty"`
}

type Conversation struct {
	ID               string    `json:"id" db:"id"`
	ParticipantOneID string    `json:"participant_one_id" db:"participant_one_id"`
	ParticipantTwoID string    `json:"participant_two_id" db:"participant_two_id"`
	LastMessageID    *string   `json:"last_message_id,omitempty" db:"last_message_id"`
	LastMessageAt    time.Time `json:"last_message_at" db:"last_message_at"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`

	// Populated fields for API responses
	ParticipantOne *User    `json:"participant_one,omitempty"`
	ParticipantTwo *User    `json:"participant_two,omitempty"`
	LastMessage    *Message `json:"last_message,omitempty"`
	UnreadCount    int      `json:"unread_count,omitempty"`
}

// Message type constants
const (
	MessageTypeText   = "text"
	MessageTypeSystem = "system"
)

// Request/Response DTOs
type SendMessageRequest struct {
	RecipientID     string  `json:"recipient_id" validate:"required,uuid"`
	Content         string  `json:"content" validate:"required,max=2000"`
	ParentMessageID *string `json:"parent_message_id,omitempty" validate:"omitempty,uuid"`
}

type GetMessagesRequest struct {
	ConversationWith string `json:"conversation_with" validate:"required,uuid"`
	Limit            int    `json:"limit,omitempty" validate:"omitempty,min=1,max=100"`
	Offset           int    `json:"offset,omitempty" validate:"omitempty,min=0"`
	Before           string `json:"before,omitempty" validate:"omitempty,uuid"`
}

type MarkMessageReadRequest struct {
	MessageID string `json:"message_id" validate:"required,uuid"`
}

type DeleteMessageRequest struct {
	MessageID string `json:"message_id" validate:"required,uuid"`
}

type GetConversationsRequest struct {
	Limit  int `json:"limit,omitempty" validate:"omitempty,min=1,max=50"`
	Offset int `json:"offset,omitempty" validate:"omitempty,min=0"`
}

type MessageResponse struct {
	Message Message `json:"message"`
}

type MessagesResponse struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	HasMore  bool      `json:"has_more"`
}

type ConversationsResponse struct {
	Conversations []Conversation `json:"conversations"`
	Total         int            `json:"total"`
	HasMore       bool           `json:"has_more"`
}

package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Chat domain errors
var (
	ErrChatMessageEmpty       = errors.New("chat message cannot be empty")
	ErrChatMessageTooLong     = errors.New("chat message exceeds maximum length")
	ErrInvalidChatMessageType = errors.New("invalid chat message type")
	ErrInvalidStreamID        = errors.New("invalid stream ID")
	ErrInvalidUserID          = errors.New("invalid user ID")
	ErrUserBanned             = errors.New("user is banned from this chat")
	ErrNotModerator           = errors.New("user is not a moderator")
	ErrCannotBanModerator     = errors.New("cannot ban a moderator")
	ErrCannotBanStreamer      = errors.New("cannot ban the stream owner")
	ErrBanAlreadyExists       = errors.New("user is already banned")
	ErrModeratorAlreadyExists = errors.New("user is already a moderator")
)

// ChatMessageType represents the type of chat message
type ChatMessageType string

const (
	ChatMessageTypeRegular    ChatMessageType = "message"
	ChatMessageTypeSystem     ChatMessageType = "system"
	ChatMessageTypeModeration ChatMessageType = "moderation"
)

const (
	MaxChatMessageLength = 500
)

// ChatMessage represents a chat message in a live stream
type ChatMessage struct {
	ID        uuid.UUID              `json:"id"`
	StreamID  uuid.UUID              `json:"stream_id"`
	UserID    uuid.UUID              `json:"user_id"`
	Username  string                 `json:"username"`
	Message   string                 `json:"message"`
	Type      ChatMessageType        `json:"type"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Deleted   bool                   `json:"deleted"`
	CreatedAt time.Time              `json:"created_at"`
}

// Validate validates the chat message
func (m *ChatMessage) Validate() error {
	if m.Message == "" {
		return ErrChatMessageEmpty
	}
	if len(m.Message) > MaxChatMessageLength {
		return ErrChatMessageTooLong
	}
	if m.StreamID == uuid.Nil {
		return ErrInvalidStreamID
	}
	if m.UserID == uuid.Nil {
		return ErrInvalidUserID
	}
	if !m.Type.IsValid() {
		return ErrInvalidChatMessageType
	}
	return nil
}

// IsValid checks if the message type is valid
func (t ChatMessageType) IsValid() bool {
	switch t {
	case ChatMessageTypeRegular, ChatMessageTypeSystem, ChatMessageTypeModeration:
		return true
	default:
		return false
	}
}

// NewChatMessage creates a new chat message
func NewChatMessage(streamID, userID uuid.UUID, username, message string) *ChatMessage {
	return &ChatMessage{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		Username:  username,
		Message:   message,
		Type:      ChatMessageTypeRegular,
		Metadata:  make(map[string]interface{}),
		Deleted:   false,
		CreatedAt: time.Now(),
	}
}

// NewSystemMessage creates a system message
func NewSystemMessage(streamID uuid.UUID, message string) *ChatMessage {
	return &ChatMessage{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    uuid.Nil, // System messages have no user
		Username:  "System",
		Message:   message,
		Type:      ChatMessageTypeSystem,
		Metadata:  make(map[string]interface{}),
		Deleted:   false,
		CreatedAt: time.Now(),
	}
}

// NewModerationMessage creates a moderation message
func NewModerationMessage(streamID uuid.UUID, message string, metadata map[string]interface{}) *ChatMessage {
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	return &ChatMessage{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    uuid.Nil,
		Username:  "Moderator",
		Message:   message,
		Type:      ChatMessageTypeModeration,
		Metadata:  metadata,
		Deleted:   false,
		CreatedAt: time.Now(),
	}
}

// ChatModerator represents a moderator for a stream
type ChatModerator struct {
	ID        uuid.UUID `json:"id"`
	StreamID  uuid.UUID `json:"stream_id"`
	UserID    uuid.UUID `json:"user_id"`
	GrantedBy uuid.UUID `json:"granted_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate validates the chat moderator
func (m *ChatModerator) Validate() error {
	if m.StreamID == uuid.Nil {
		return ErrInvalidStreamID
	}
	if m.UserID == uuid.Nil {
		return ErrInvalidUserID
	}
	if m.GrantedBy == uuid.Nil {
		return ErrInvalidUserID
	}
	return nil
}

// NewChatModerator creates a new chat moderator
func NewChatModerator(streamID, userID, grantedBy uuid.UUID) *ChatModerator {
	return &ChatModerator{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		GrantedBy: grantedBy,
		CreatedAt: time.Now(),
	}
}

// ChatBan represents a chat ban (timeout or permanent)
type ChatBan struct {
	ID        uuid.UUID  `json:"id"`
	StreamID  uuid.UUID  `json:"stream_id"`
	UserID    uuid.UUID  `json:"user_id"`
	BannedBy  uuid.UUID  `json:"banned_by"`
	Reason    string     `json:"reason,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Validate validates the chat ban
func (b *ChatBan) Validate() error {
	if b.StreamID == uuid.Nil {
		return ErrInvalidStreamID
	}
	if b.UserID == uuid.Nil {
		return ErrInvalidUserID
	}
	if b.BannedBy == uuid.Nil {
		return ErrInvalidUserID
	}
	return nil
}

// IsExpired checks if the ban has expired
func (b *ChatBan) IsExpired() bool {
	if b.ExpiresAt == nil {
		return false // Permanent ban
	}
	return time.Now().After(*b.ExpiresAt)
}

// IsPermanent checks if the ban is permanent
func (b *ChatBan) IsPermanent() bool {
	return b.ExpiresAt == nil
}

// NewChatBan creates a new chat ban (timeout)
func NewChatBan(streamID, userID, bannedBy uuid.UUID, reason string, duration time.Duration) *ChatBan {
	var expiresAt *time.Time
	if duration > 0 {
		expiry := time.Now().Add(duration)
		expiresAt = &expiry
	}

	return &ChatBan{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		BannedBy:  bannedBy,
		Reason:    reason,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
}

// NewPermanentBan creates a permanent chat ban
func NewPermanentBan(streamID, userID, bannedBy uuid.UUID, reason string) *ChatBan {
	return &ChatBan{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		BannedBy:  bannedBy,
		Reason:    reason,
		ExpiresAt: nil, // nil = permanent
		CreatedAt: time.Now(),
	}
}

// ChatStreamStats represents aggregate statistics for a stream's chat
type ChatStreamStats struct {
	StreamID          uuid.UUID `json:"stream_id"`
	UniqueChatters    int       `json:"unique_chatters"`
	MessageCount      int       `json:"message_count"`
	ModerationActions int       `json:"moderation_actions"`
	LastMessageAt     time.Time `json:"last_message_at,omitempty"`
	ModeratorCount    int       `json:"moderator_count"`
	ActiveBanCount    int       `json:"active_ban_count"`
}

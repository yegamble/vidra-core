package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test ChatMessage creation and validation
func TestNewChatMessage(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	username := "testuser"
	message := "Hello, world!"

	msg := NewChatMessage(streamID, userID, username, message)

	assert.NotEqual(t, uuid.Nil, msg.ID)
	assert.Equal(t, streamID, msg.StreamID)
	assert.Equal(t, userID, msg.UserID)
	assert.Equal(t, username, msg.Username)
	assert.Equal(t, message, msg.Message)
	assert.Equal(t, ChatMessageTypeRegular, msg.Type)
	assert.NotNil(t, msg.Metadata)
	assert.False(t, msg.Deleted)
	assert.False(t, msg.CreatedAt.IsZero())
}

func TestNewSystemMessage(t *testing.T) {
	streamID := uuid.New()
	message := "Stream started"

	msg := NewSystemMessage(streamID, message)

	assert.NotEqual(t, uuid.Nil, msg.ID)
	assert.Equal(t, streamID, msg.StreamID)
	assert.Equal(t, uuid.Nil, msg.UserID)
	assert.Equal(t, "System", msg.Username)
	assert.Equal(t, message, msg.Message)
	assert.Equal(t, ChatMessageTypeSystem, msg.Type)
	assert.NotNil(t, msg.Metadata)
	assert.False(t, msg.Deleted)
	assert.False(t, msg.CreatedAt.IsZero())
}

func TestNewModerationMessage(t *testing.T) {
	streamID := uuid.New()
	message := "User banned"
	metadata := map[string]interface{}{
		"banned_user": "baduser",
		"reason":      "spam",
	}

	msg := NewModerationMessage(streamID, message, metadata)

	assert.NotEqual(t, uuid.Nil, msg.ID)
	assert.Equal(t, streamID, msg.StreamID)
	assert.Equal(t, uuid.Nil, msg.UserID)
	assert.Equal(t, "Moderator", msg.Username)
	assert.Equal(t, message, msg.Message)
	assert.Equal(t, ChatMessageTypeModeration, msg.Type)
	assert.Equal(t, metadata, msg.Metadata)
	assert.False(t, msg.Deleted)
	assert.False(t, msg.CreatedAt.IsZero())
}

func TestNewModerationMessage_NilMetadata(t *testing.T) {
	streamID := uuid.New()
	message := "User banned"

	msg := NewModerationMessage(streamID, message, nil)

	assert.NotNil(t, msg.Metadata)
	assert.Empty(t, msg.Metadata)
}

func TestChatMessage_Validate(t *testing.T) {
	tests := []struct {
		name    string
		msg     *ChatMessage
		wantErr error
	}{
		{
			name: "valid message",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.New(),
				UserID:   uuid.New(),
				Username: "user",
				Message:  "Hello",
				Type:     ChatMessageTypeRegular,
			},
			wantErr: nil,
		},
		{
			name: "empty message",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.New(),
				UserID:   uuid.New(),
				Username: "user",
				Message:  "",
				Type:     ChatMessageTypeRegular,
			},
			wantErr: ErrChatMessageEmpty,
		},
		{
			name: "message too long",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.New(),
				UserID:   uuid.New(),
				Username: "user",
				Message:  string(make([]byte, MaxChatMessageLength+1)),
				Type:     ChatMessageTypeRegular,
			},
			wantErr: ErrChatMessageTooLong,
		},
		{
			name: "nil stream ID",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.Nil,
				UserID:   uuid.New(),
				Username: "user",
				Message:  "Hello",
				Type:     ChatMessageTypeRegular,
			},
			wantErr: ErrInvalidStreamID,
		},
		{
			name: "nil user ID",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.New(),
				UserID:   uuid.Nil,
				Username: "user",
				Message:  "Hello",
				Type:     ChatMessageTypeRegular,
			},
			wantErr: ErrInvalidUserID,
		},
		{
			name: "invalid message type",
			msg: &ChatMessage{
				ID:       uuid.New(),
				StreamID: uuid.New(),
				UserID:   uuid.New(),
				Username: "user",
				Message:  "Hello",
				Type:     "invalid",
			},
			wantErr: ErrInvalidChatMessageType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestChatMessageType_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		msgType ChatMessageType
		want    bool
	}{
		{"regular", ChatMessageTypeRegular, true},
		{"system", ChatMessageTypeSystem, true},
		{"moderation", ChatMessageTypeModeration, true},
		{"invalid", "invalid", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.msgType.IsValid())
		})
	}
}

// Test ChatModerator creation and validation
func TestNewChatModerator(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	grantedBy := uuid.New()

	mod := NewChatModerator(streamID, userID, grantedBy)

	assert.NotEqual(t, uuid.Nil, mod.ID)
	assert.Equal(t, streamID, mod.StreamID)
	assert.Equal(t, userID, mod.UserID)
	assert.Equal(t, grantedBy, mod.GrantedBy)
	assert.False(t, mod.CreatedAt.IsZero())
}

func TestChatModerator_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mod     *ChatModerator
		wantErr error
	}{
		{
			name: "valid moderator",
			mod: &ChatModerator{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.New(),
				GrantedBy: uuid.New(),
				CreatedAt: time.Now(),
			},
			wantErr: nil,
		},
		{
			name: "nil stream ID",
			mod: &ChatModerator{
				ID:        uuid.New(),
				StreamID:  uuid.Nil,
				UserID:    uuid.New(),
				GrantedBy: uuid.New(),
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidStreamID,
		},
		{
			name: "nil user ID",
			mod: &ChatModerator{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.Nil,
				GrantedBy: uuid.New(),
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidUserID,
		},
		{
			name: "nil granted by",
			mod: &ChatModerator{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.New(),
				GrantedBy: uuid.Nil,
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidUserID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mod.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test ChatBan creation and validation
func TestNewChatBan(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	bannedBy := uuid.New()
	reason := "spam"
	duration := 10 * time.Minute

	ban := NewChatBan(streamID, userID, bannedBy, reason, duration)

	assert.NotEqual(t, uuid.Nil, ban.ID)
	assert.Equal(t, streamID, ban.StreamID)
	assert.Equal(t, userID, ban.UserID)
	assert.Equal(t, bannedBy, ban.BannedBy)
	assert.Equal(t, reason, ban.Reason)
	require.NotNil(t, ban.ExpiresAt)
	assert.True(t, ban.ExpiresAt.After(time.Now()))
	assert.False(t, ban.CreatedAt.IsZero())
}

func TestNewChatBan_ZeroDuration(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	bannedBy := uuid.New()
	reason := "spam"

	ban := NewChatBan(streamID, userID, bannedBy, reason, 0)

	assert.Nil(t, ban.ExpiresAt, "zero duration should result in permanent ban")
}

func TestNewPermanentBan(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	bannedBy := uuid.New()
	reason := "serious violation"

	ban := NewPermanentBan(streamID, userID, bannedBy, reason)

	assert.NotEqual(t, uuid.Nil, ban.ID)
	assert.Equal(t, streamID, ban.StreamID)
	assert.Equal(t, userID, ban.UserID)
	assert.Equal(t, bannedBy, ban.BannedBy)
	assert.Equal(t, reason, ban.Reason)
	assert.Nil(t, ban.ExpiresAt)
	assert.False(t, ban.CreatedAt.IsZero())
}

func TestChatBan_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ban     *ChatBan
		wantErr error
	}{
		{
			name: "valid ban",
			ban: &ChatBan{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.New(),
				BannedBy:  uuid.New(),
				Reason:    "spam",
				ExpiresAt: nil,
				CreatedAt: time.Now(),
			},
			wantErr: nil,
		},
		{
			name: "nil stream ID",
			ban: &ChatBan{
				ID:        uuid.New(),
				StreamID:  uuid.Nil,
				UserID:    uuid.New(),
				BannedBy:  uuid.New(),
				Reason:    "spam",
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidStreamID,
		},
		{
			name: "nil user ID",
			ban: &ChatBan{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.Nil,
				BannedBy:  uuid.New(),
				Reason:    "spam",
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidUserID,
		},
		{
			name: "nil banned by",
			ban: &ChatBan{
				ID:        uuid.New(),
				StreamID:  uuid.New(),
				UserID:    uuid.New(),
				BannedBy:  uuid.Nil,
				Reason:    "spam",
				CreatedAt: time.Now(),
			},
			wantErr: ErrInvalidUserID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ban.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestChatBan_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{
			name:      "permanent ban (nil expiry)",
			expiresAt: nil,
			want:      false,
		},
		{
			name: "expired ban",
			expiresAt: func() *time.Time {
				t := time.Now().Add(-1 * time.Hour)
				return &t
			}(),
			want: true,
		},
		{
			name: "not expired ban",
			expiresAt: func() *time.Time {
				t := time.Now().Add(1 * time.Hour)
				return &t
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ban := &ChatBan{
				ExpiresAt: tt.expiresAt,
			}
			assert.Equal(t, tt.want, ban.IsExpired())
		})
	}
}

func TestChatBan_IsPermanent(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{
			name:      "permanent ban",
			expiresAt: nil,
			want:      true,
		},
		{
			name: "temporary ban",
			expiresAt: func() *time.Time {
				t := time.Now().Add(1 * time.Hour)
				return &t
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ban := &ChatBan{
				ExpiresAt: tt.expiresAt,
			}
			assert.Equal(t, tt.want, ban.IsPermanent())
		})
	}
}

// Test edge cases
func TestChatMessage_MaxLength(t *testing.T) {
	msg := NewChatMessage(
		uuid.New(),
		uuid.New(),
		"user",
		string(make([]byte, MaxChatMessageLength)),
	)

	err := msg.Validate()
	assert.NoError(t, err, "message at max length should be valid")
}

func TestChatMessage_MaxLengthPlusOne(t *testing.T) {
	msg := NewChatMessage(
		uuid.New(),
		uuid.New(),
		"user",
		string(make([]byte, MaxChatMessageLength+1)),
	)

	err := msg.Validate()
	assert.ErrorIs(t, err, ErrChatMessageTooLong)
}

func TestNewChatBan_NegativeDuration(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	bannedBy := uuid.New()
	reason := "test"

	ban := NewChatBan(streamID, userID, bannedBy, reason, -1*time.Hour)

	// Negative duration should result in a permanent ban (nil expiry)
	assert.Nil(t, ban.ExpiresAt)
	assert.True(t, ban.IsPermanent())
}

func TestChatBan_ExpiryEdgeCase(t *testing.T) {
	// Test a ban that expires in the next millisecond
	expiresAt := time.Now().Add(1 * time.Millisecond)
	ban := &ChatBan{
		ExpiresAt: &expiresAt,
	}

	// Should not be expired yet
	assert.False(t, ban.IsExpired())

	// Wait for it to expire
	time.Sleep(2 * time.Millisecond)

	// Should be expired now
	assert.True(t, ban.IsExpired())
}

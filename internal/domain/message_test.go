package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMessageTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Text", MessageTypeText, "text"},
		{"System", MessageTypeSystem, "system"},
		{"KeyExchange", MessageTypeKeyExchange, "key_exchange"},
		{"Secure", MessageTypeSecure, "secure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestKeyExchangeTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Offer", KeyExchangeTypeOffer, "offer"},
		{"Accept", KeyExchangeTypeAccept, "accept"},
		{"Confirm", KeyExchangeTypeConfirm, "confirm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestCryptoAuditOperationConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"KeyGeneration", CryptoOpKeyGeneration, "key_generation"},
		{"KeyExchange", CryptoOpKeyExchange, "key_exchange"},
		{"Encryption", CryptoOpEncryption, "encryption"},
		{"Decryption", CryptoOpDecryption, "decryption"},
		{"KeyRotation", CryptoOpKeyRotation, "key_rotation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestConversationJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	lastMsgID := "msg-last-123"
	lastKeyRotation := now.Add(-24 * time.Hour)

	conv := Conversation{
		ID:                  "conv-456",
		ParticipantOneID:    "user-1",
		ParticipantTwoID:    "user-2",
		LastMessageID:       &lastMsgID,
		LastMessageAt:       now,
		CreatedAt:           now,
		UpdatedAt:           now,
		IsEncrypted:         true,
		KeyExchangeComplete: true,
		EncryptionVersion:   2,
		LastKeyRotation:     &lastKeyRotation,
		UnreadCount:         5,
	}

	data, err := json.Marshal(conv)
	assert.NoError(t, err)

	var decoded Conversation
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, conv.ID, decoded.ID)
	assert.Equal(t, conv.ParticipantOneID, decoded.ParticipantOneID)
	assert.Equal(t, conv.ParticipantTwoID, decoded.ParticipantTwoID)
	assert.NotNil(t, decoded.LastMessageID)
	assert.Equal(t, lastMsgID, *decoded.LastMessageID)
	assert.True(t, decoded.IsEncrypted)
	assert.True(t, decoded.KeyExchangeComplete)
	assert.Equal(t, 2, decoded.EncryptionVersion)
	assert.NotNil(t, decoded.LastKeyRotation)
	assert.Equal(t, 5, decoded.UnreadCount)
}

func TestConversationMinimalJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	conv := Conversation{
		ID:               "conv-789",
		ParticipantOneID: "user-a",
		ParticipantTwoID: "user-b",
		LastMessageAt:    now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	data, err := json.Marshal(conv)
	assert.NoError(t, err)

	var decoded Conversation
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Nil(t, decoded.LastMessageID)
	assert.False(t, decoded.IsEncrypted)
	assert.False(t, decoded.KeyExchangeComplete)
	assert.Equal(t, 0, decoded.EncryptionVersion)
	assert.Nil(t, decoded.LastKeyRotation)
}

func TestSendMessageRequestJSON(t *testing.T) {
	parentMsgID := "parent-msg-123"
	req := SendMessageRequest{
		RecipientID:     "recipient-uuid",
		Content:         "Hello, how are you?",
		ParentMessageID: &parentMsgID,
		IsEncrypted:     false,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded SendMessageRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.RecipientID, decoded.RecipientID)
	assert.Equal(t, req.Content, decoded.Content)
	assert.NotNil(t, decoded.ParentMessageID)
	assert.Equal(t, parentMsgID, *decoded.ParentMessageID)
	assert.False(t, decoded.IsEncrypted)
}

func TestSendMessageRequestWithoutParent(t *testing.T) {
	req := SendMessageRequest{
		RecipientID: "recipient-uuid",
		Content:     "A standalone message",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded SendMessageRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Nil(t, decoded.ParentMessageID)
}

func TestMessageJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	readAt := now.Add(5 * time.Minute)
	encryptedContent := "encrypted-base64-data"
	nonce := "nonce-value"
	signature := "pgp-sig-abc"

	msg := Message{
		ID:                   "msg-001",
		SenderID:             "user-1",
		RecipientID:          "user-2",
		Content:              "plaintext message",
		MessageType:          MessageTypeText,
		IsRead:               true,
		IsDeletedBySender:    false,
		IsDeletedByRecipient: false,
		CreatedAt:            now,
		UpdatedAt:            now,
		ReadAt:               &readAt,
		EncryptedContent:     &encryptedContent,
		ContentNonce:         &nonce,
		PGPSignature:         &signature,
		IsEncrypted:          true,
		EncryptionVersion:    1,
	}

	data, err := json.Marshal(msg)
	assert.NoError(t, err)

	var decoded Message
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, msg.SenderID, decoded.SenderID)
	assert.Equal(t, msg.RecipientID, decoded.RecipientID)
	assert.Equal(t, msg.Content, decoded.Content)
	assert.Equal(t, MessageTypeText, decoded.MessageType)
	assert.True(t, decoded.IsRead)
	assert.False(t, decoded.IsDeletedBySender)
	assert.False(t, decoded.IsDeletedByRecipient)
	assert.NotNil(t, decoded.ReadAt)
	assert.NotNil(t, decoded.EncryptedContent)
	assert.Equal(t, encryptedContent, *decoded.EncryptedContent)
	assert.NotNil(t, decoded.ContentNonce)
	assert.NotNil(t, decoded.PGPSignature)
	assert.True(t, decoded.IsEncrypted)
	assert.Equal(t, 1, decoded.EncryptionVersion)
}

func TestMessagesResponseJSON(t *testing.T) {
	resp := MessagesResponse{
		Messages: []Message{
			{ID: "msg-1", Content: "Hello"},
			{ID: "msg-2", Content: "World"},
		},
		Total:   10,
		HasMore: true,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded MessagesResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Len(t, decoded.Messages, 2)
	assert.Equal(t, 10, decoded.Total)
	assert.True(t, decoded.HasMore)
}

func TestConversationsResponseJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	resp := ConversationsResponse{
		Conversations: []Conversation{
			{ID: "conv-1", ParticipantOneID: "u1", ParticipantTwoID: "u2", LastMessageAt: now, CreatedAt: now, UpdatedAt: now},
		},
		Total:   1,
		HasMore: false,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded ConversationsResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Len(t, decoded.Conversations, 1)
	assert.Equal(t, 1, decoded.Total)
	assert.False(t, decoded.HasMore)
}

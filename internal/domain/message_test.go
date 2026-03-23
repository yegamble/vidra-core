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
	content := "plaintext message"
	encryptedContent := "encrypted-base64-data"
	nonce := "nonce-value"
	signature := "pgp-sig-abc"

	msg := Message{
		ID:                   "msg-001",
		SenderID:             "user-1",
		RecipientID:          "user-2",
		Content:              &content,
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
	hello := "Hello"
	world := "World"
	resp := MessagesResponse{
		Messages: []Message{
			{ID: "msg-1", Content: &hello},
			{ID: "msg-2", Content: &world},
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

func TestConversationHasEncryptionStatus(t *testing.T) {
	conv := Conversation{
		ID:               "conv-test",
		ParticipantOneID: "user-1",
		ParticipantTwoID: "user-2",
		EncryptionStatus: EncryptionStatusNone,
	}
	assert.Equal(t, "none", conv.EncryptionStatus)

	conv.EncryptionStatus = EncryptionStatusPending
	assert.Equal(t, "pending", conv.EncryptionStatus)

	conv.EncryptionStatus = EncryptionStatusActive
	assert.Equal(t, "active", conv.EncryptionStatus)
}

func TestEncryptionStatusConstants(t *testing.T) {
	assert.Equal(t, "none", EncryptionStatusNone)
	assert.Equal(t, "pending", EncryptionStatusPending)
	assert.Equal(t, "active", EncryptionStatusActive)
}

func TestRegisterIdentityKeyRequestJSON(t *testing.T) {
	req := RegisterIdentityKeyRequest{
		PublicIdentityKey: "base64-x25519-pubkey",
		PublicSigningKey:  "base64-ed25519-pubkey",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded RegisterIdentityKeyRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.PublicIdentityKey, decoded.PublicIdentityKey)
	assert.Equal(t, req.PublicSigningKey, decoded.PublicSigningKey)
}

func TestStoreEncryptedMessageRequestJSON(t *testing.T) {
	req := StoreEncryptedMessageRequest{
		RecipientID:      "recipient-uuid",
		EncryptedContent: "base64ciphertext",
		ContentNonce:     "base64nonce24bytes",
		Signature:        "base64ed25519sig",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded StoreEncryptedMessageRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.RecipientID, decoded.RecipientID)
	assert.Equal(t, req.EncryptedContent, decoded.EncryptedContent)
	assert.Equal(t, req.ContentNonce, decoded.ContentNonce)
	assert.Equal(t, req.Signature, decoded.Signature)
}

func TestPublicKeyBundleJSON(t *testing.T) {
	bundle := PublicKeyBundle{
		PublicIdentityKey: "x25519-pubkey",
		PublicSigningKey:  "ed25519-pubkey",
		KeyVersion:        1,
	}

	data, err := json.Marshal(bundle)
	assert.NoError(t, err)

	var decoded PublicKeyBundle
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, bundle.PublicIdentityKey, decoded.PublicIdentityKey)
	assert.Equal(t, bundle.PublicSigningKey, decoded.PublicSigningKey)
	assert.Equal(t, 1, decoded.KeyVersion)
}

func TestE2EEStatusResponseClientSideModel(t *testing.T) {
	resp := E2EEStatusResponse{
		HasIdentityKey: true,
		KeyVersion:     1,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded E2EEStatusResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.True(t, decoded.HasIdentityKey)
	assert.Equal(t, 1, decoded.KeyVersion)
}

func TestUserSigningKeyHasPublicIdentityKey(t *testing.T) {
	pubIdentKey := "x25519-pub-key-value"
	key := UserSigningKey{
		UserID:            "user-1",
		PublicKey:         "ed25519-signing-key",
		PublicIdentityKey: &pubIdentKey,
		KeyVersion:        1,
	}

	assert.NotNil(t, key.PublicIdentityKey)
	assert.Equal(t, pubIdentKey, *key.PublicIdentityKey)
}

func TestMessageContentNullable(t *testing.T) {
	encContent := "base64ciphertext"
	nonce := "base64nonce"
	msg := Message{
		ID:               "msg-001",
		SenderID:         "user-1",
		RecipientID:      "user-2",
		Content:          nil,
		MessageType:      MessageTypeSecure,
		IsEncrypted:      true,
		EncryptedContent: &encContent,
		ContentNonce:     &nonce,
	}

	assert.Nil(t, msg.Content)
	assert.NotNil(t, msg.EncryptedContent)

	plainContent := "hello world"
	plainMsg := Message{
		ID:          "msg-002",
		SenderID:    "user-1",
		RecipientID: "user-2",
		Content:     &plainContent,
		MessageType: MessageTypeText,
		IsEncrypted: false,
	}

	assert.NotNil(t, plainMsg.Content)
	assert.Equal(t, "hello world", *plainMsg.Content)
}

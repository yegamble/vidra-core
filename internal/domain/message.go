package domain

import "time"

type Message struct {
	ID                   string     `json:"id" db:"id"`
	SenderID             string     `json:"sender_id" db:"sender_id"`
	RecipientID          string     `json:"recipient_id" db:"recipient_id"`
	Content              *string    `json:"content,omitempty" db:"content"`
	MessageType          string     `json:"message_type" db:"message_type"`
	IsRead               bool       `json:"is_read" db:"is_read"`
	IsDeletedBySender    bool       `json:"is_deleted_by_sender" db:"is_deleted_by_sender"`
	IsDeletedByRecipient bool       `json:"is_deleted_by_recipient" db:"is_deleted_by_recipient"`
	ParentMessageID      *string    `json:"parent_message_id,omitempty" db:"parent_message_id"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
	ReadAt               *time.Time `json:"read_at,omitempty" db:"read_at"`

	EncryptedContent  *string `json:"encrypted_content,omitempty" db:"encrypted_content"`
	ContentNonce      *string `json:"content_nonce,omitempty" db:"content_nonce"`
	PGPSignature      *string `json:"pgp_signature,omitempty" db:"pgp_signature"`
	IsEncrypted       bool    `json:"is_encrypted" db:"is_encrypted"`
	EncryptionVersion int     `json:"encryption_version" db:"encryption_version"`

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

	EncryptionStatus string `json:"encryption_status" db:"encryption_status"`

	IsEncrypted         bool       `json:"is_encrypted" db:"is_encrypted"`
	KeyExchangeComplete bool       `json:"key_exchange_complete" db:"key_exchange_complete"`
	EncryptionVersion   int        `json:"encryption_version" db:"encryption_version"`
	LastKeyRotation     *time.Time `json:"last_key_rotation,omitempty" db:"last_key_rotation"`

	ParticipantOne *User    `json:"participant_one,omitempty"`
	ParticipantTwo *User    `json:"participant_two,omitempty"`
	LastMessage    *Message `json:"last_message,omitempty"`
	UnreadCount    int      `json:"unread_count,omitempty"`
}

const (
	MessageTypeText        = "text"
	MessageTypeSystem      = "system"
	MessageTypeKeyExchange = "key_exchange"
	MessageTypeSecure      = "secure"
)

const (
	EncryptionStatusNone    = "none"
	EncryptionStatusPending = "pending"
	EncryptionStatusActive  = "active"
)

type SendMessageRequest struct {
	RecipientID     string  `json:"recipient_id" validate:"required,uuid"`
	Content         string  `json:"content" validate:"required,max=2000"`
	ParentMessageID *string `json:"parent_message_id,omitempty" validate:"omitempty,uuid"`
	IsEncrypted     bool    `json:"is_encrypted,omitempty"`
	// ClientMessageID lets callers pass a client-generated UUID that the realtime hub echoes
	// back in `message_received.data.client_message_id`. Used by the frontend to match an
	// optimistic message to its server-assigned counterpart deterministically.
	ClientMessageID string `json:"client_message_id,omitempty" validate:"omitempty,uuid"`
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

type ConversationKey struct {
	ID                    string     `json:"id" db:"id"`
	ConversationID        string     `json:"conversation_id" db:"conversation_id"`
	UserID                string     `json:"user_id" db:"user_id"`
	EncryptedPrivateKey   string     `json:"encrypted_private_key" db:"encrypted_private_key"`
	PublicKey             string     `json:"public_key" db:"public_key"`
	EncryptedSharedSecret *string    `json:"encrypted_shared_secret,omitempty" db:"encrypted_shared_secret"`
	KeyVersion            int        `json:"key_version" db:"key_version"`
	IsActive              bool       `json:"is_active" db:"is_active"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty" db:"expires_at"`
}

type KeyExchangeMessage struct {
	ID             string    `json:"id" db:"id"`
	ConversationID string    `json:"conversation_id" db:"conversation_id"`
	SenderID       string    `json:"sender_id" db:"sender_id"`
	RecipientID    string    `json:"recipient_id" db:"recipient_id"`
	ExchangeType   string    `json:"exchange_type" db:"exchange_type"`
	PublicKey      string    `json:"public_key" db:"public_key"`
	Signature      string    `json:"signature" db:"signature"`
	Nonce          string    `json:"nonce" db:"nonce"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	ExpiresAt      time.Time `json:"expires_at" db:"expires_at"`
}

type UserSigningKey struct {
	UserID              string    `json:"user_id" db:"user_id"`
	EncryptedPrivateKey *string   `json:"encrypted_private_key,omitempty" db:"encrypted_private_key"`
	PublicKey           string    `json:"public_key" db:"public_key"`
	PublicIdentityKey   *string   `json:"public_identity_key,omitempty" db:"public_identity_key"`
	KeyVersion          int       `json:"key_version" db:"key_version"`
	CreatedAt           time.Time `json:"created_at" db:"created_at"`
}

type PublicKeyBundle struct {
	PublicIdentityKey string `json:"public_identity_key"`
	PublicSigningKey  string `json:"public_signing_key"`
	KeyVersion        int    `json:"key_version"`
}

type CryptoAuditLog struct {
	ID             string    `json:"id" db:"id"`
	UserID         string    `json:"user_id" db:"user_id"`
	ConversationID *string   `json:"conversation_id,omitempty" db:"conversation_id"`
	Operation      string    `json:"operation" db:"operation"`
	Success        bool      `json:"success" db:"success"`
	ErrorMessage   *string   `json:"error_message,omitempty" db:"error_message"`
	ClientIP       *string   `json:"client_ip,omitempty" db:"client_ip"`
	UserAgent      *string   `json:"user_agent,omitempty" db:"user_agent"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

const (
	KeyExchangeTypeOffer   = "offer"
	KeyExchangeTypeAccept  = "accept"
	KeyExchangeTypeConfirm = "confirm"
)

const (
	CryptoOpKeyGeneration = "key_generation"
	CryptoOpKeyExchange   = "key_exchange"
	CryptoOpEncryption    = "encryption"
	CryptoOpDecryption    = "decryption"
	CryptoOpKeyRotation   = "key_rotation"
	CryptoOpRegisterKey   = "register_identity_key"
	CryptoOpStoreMessage  = "store_encrypted_message"
)

type RegisterIdentityKeyRequest struct {
	PublicIdentityKey string `json:"public_identity_key" validate:"required,max=256"`
	PublicSigningKey  string `json:"public_signing_key" validate:"required,max=256"`
}

type InitiateKeyExchangeRequest struct {
	RecipientID     string `json:"recipient_id" validate:"required,uuid"`
	SenderPublicKey string `json:"sender_public_key" validate:"required,max=256"`
}

type AcceptKeyExchangeRequest struct {
	KeyExchangeID string `json:"key_exchange_id" validate:"required,uuid"`
	PublicKey     string `json:"public_key" validate:"required,max=256"`
}

type StoreEncryptedMessageRequest struct {
	RecipientID      string  `json:"recipient_id" validate:"required,uuid"`
	EncryptedContent string  `json:"encrypted_content" validate:"required,max=8192"`
	ContentNonce     string  `json:"content_nonce" validate:"required,len=32"`
	Signature        string  `json:"signature" validate:"required,max=128"`
	ParentMessageID  *string `json:"parent_message_id,omitempty" validate:"omitempty,uuid"`
}

type KeyExchangeResponse struct {
	KeyExchange KeyExchangeMessage `json:"key_exchange"`
	Status      string             `json:"status"`
}

type E2EEStatusResponse struct {
	HasIdentityKey  bool       `json:"has_identity_key"`
	KeyVersion      int        `json:"key_version"`
	LastKeyRotation *time.Time `json:"last_key_rotation,omitempty"`
}

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

	// E2EE fields
	EncryptedContent  *string `json:"encrypted_content,omitempty" db:"encrypted_content"`
	ContentNonce      *string `json:"content_nonce,omitempty" db:"content_nonce"`
	PGPSignature      *string `json:"pgp_signature,omitempty" db:"pgp_signature"`
	IsEncrypted       bool    `json:"is_encrypted" db:"is_encrypted"`
	EncryptionVersion int     `json:"encryption_version" db:"encryption_version"`

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

	// E2EE fields
	IsEncrypted         bool       `json:"is_encrypted" db:"is_encrypted"`
	KeyExchangeComplete bool       `json:"key_exchange_complete" db:"key_exchange_complete"`
	EncryptionVersion   int        `json:"encryption_version" db:"encryption_version"`
	LastKeyRotation     *time.Time `json:"last_key_rotation,omitempty" db:"last_key_rotation"`

	// Populated fields for API responses
	ParticipantOne *User    `json:"participant_one,omitempty"`
	ParticipantTwo *User    `json:"participant_two,omitempty"`
	LastMessage    *Message `json:"last_message,omitempty"`
	UnreadCount    int      `json:"unread_count,omitempty"`
}

// Message type constants
const (
	MessageTypeText        = "text"
	MessageTypeSystem      = "system"
	MessageTypeKeyExchange = "key_exchange"
	MessageTypeSecure      = "secure"
)

// Request/Response DTOs
type SendMessageRequest struct {
	RecipientID     string  `json:"recipient_id" validate:"required,uuid"`
	Content         string  `json:"content" validate:"required,max=2000"`
	ParentMessageID *string `json:"parent_message_id,omitempty" validate:"omitempty,uuid"`
	IsEncrypted     bool    `json:"is_encrypted,omitempty"`
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

// E2EE Domain Models

// UserMasterKey represents a user's master encryption key
type UserMasterKey struct {
	UserID             string    `json:"user_id" db:"user_id"`
	EncryptedMasterKey string    `json:"encrypted_master_key" db:"encrypted_master_key"`
	Argon2Salt         string    `json:"argon2_salt" db:"argon2_salt"`
	Argon2Memory       int       `json:"argon2_memory" db:"argon2_memory"`
	Argon2Time         int       `json:"argon2_time" db:"argon2_time"`
	Argon2Parallelism  int       `json:"argon2_parallelism" db:"argon2_parallelism"`
	KeyVersion         int       `json:"key_version" db:"key_version"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// ConversationKey represents encryption keys for a conversation
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

// KeyExchangeMessage represents key exchange handshake messages
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

// UserSigningKey represents Ed25519 signing keys for message authenticity
type UserSigningKey struct {
	UserID              string    `json:"user_id" db:"user_id"`
	EncryptedPrivateKey string    `json:"encrypted_private_key" db:"encrypted_private_key"`
	PublicKey           string    `json:"public_key" db:"public_key"`
	KeyVersion          int       `json:"key_version" db:"key_version"`
	CreatedAt           time.Time `json:"created_at" db:"created_at"`
}

// CryptoAuditLog represents cryptographic operations audit log
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

// Key exchange type constants
const (
	KeyExchangeTypeOffer   = "offer"
	KeyExchangeTypeAccept  = "accept"
	KeyExchangeTypeConfirm = "confirm"
)

// Crypto audit operation constants
const (
	CryptoOpKeyGeneration = "key_generation"
	CryptoOpKeyExchange   = "key_exchange"
	CryptoOpEncryption    = "encryption"
	CryptoOpDecryption    = "decryption"
	CryptoOpKeyRotation   = "key_rotation"
)

// E2EE Request/Response DTOs

// InitiateKeyExchangeRequest initiates E2EE for a conversation
type InitiateKeyExchangeRequest struct {
	RecipientID string `json:"recipient_id" validate:"required,uuid"`
	PublicKey   string `json:"public_key" validate:"required"`
	Signature   string `json:"signature" validate:"required"`
}

// AcceptKeyExchangeRequest accepts E2EE key exchange
type AcceptKeyExchangeRequest struct {
	KeyExchangeID string `json:"key_exchange_id" validate:"required,uuid"`
	PublicKey     string `json:"public_key" validate:"required"`
	Signature     string `json:"signature" validate:"required"`
}

// SendSecureMessageRequest sends encrypted message
type SendSecureMessageRequest struct {
	RecipientID      string  `json:"recipient_id" validate:"required,uuid"`
	EncryptedContent string  `json:"encrypted_content" validate:"required"`
	PGPSignature     string  `json:"pgp_signature" validate:"required"`
	ParentMessageID  *string `json:"parent_message_id,omitempty" validate:"omitempty,uuid"`
}

// SetupE2EERequest sets up user's master key for E2EE
type SetupE2EERequest struct {
	Password string `json:"password" validate:"required,min=8,max=128"`
}

// UnlockE2EERequest unlocks user's E2EE keys
type UnlockE2EERequest struct {
	Password string `json:"password" validate:"required"`
}

// KeyExchangeResponse returns key exchange status
type KeyExchangeResponse struct {
	KeyExchange KeyExchangeMessage `json:"key_exchange"`
	Status      string             `json:"status"`
}

// E2EEStatusResponse returns E2EE status for user
type E2EEStatusResponse struct {
	HasMasterKey    bool       `json:"has_master_key"`
	IsUnlocked      bool       `json:"is_unlocked"`
	KeyVersion      int        `json:"key_version"`
	LastKeyRotation *time.Time `json:"last_key_rotation,omitempty"`
}

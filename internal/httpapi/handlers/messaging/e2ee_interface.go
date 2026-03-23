package messaging

import (
	"context"

	"vidra-core/internal/domain"
)

type E2EEServiceInterface interface {
	RegisterIdentityKey(ctx context.Context, userID string, publicIdentityKey string, publicSigningKey string, clientIP string, userAgent string) error

	GetPublicKeys(ctx context.Context, userID string) (*domain.PublicKeyBundle, error)

	GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error)

	InitiateKeyExchange(ctx context.Context, senderID string, recipientID string, senderPublicKey string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error)

	AcceptKeyExchange(ctx context.Context, keyExchangeID string, userID string, recipientPublicKey string, clientIP string, userAgent string) error

	GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)

	StoreEncryptedMessage(ctx context.Context, senderID string, req *domain.StoreEncryptedMessageRequest, clientIP string, userAgent string) (*domain.Message, error)

	GetEncryptedMessages(ctx context.Context, conversationID string, userID string, limit int, offset int) ([]*domain.Message, error)
}

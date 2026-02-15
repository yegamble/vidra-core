package messaging

import (
	"context"

	"athena/internal/domain"
)

type E2EEServiceInterface interface {
	SetupE2EE(ctx context.Context, userID string, password string, clientIP string, userAgent string) error
	UnlockE2EE(ctx context.Context, userID string, password string, clientIP string, userAgent string) error
	LockE2EE(ctx context.Context, userID string)
	GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error)
	InitiateKeyExchange(ctx context.Context, senderID string, recipientID string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error)
	AcceptKeyExchange(ctx context.Context, keyExchangeID string, userID string, clientIP string, userAgent string) error
	GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)
	EncryptMessage(ctx context.Context, senderID string, recipientID string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error)
	SaveSecureMessage(ctx context.Context, message *domain.Message) error
	GetMessage(ctx context.Context, messageID string) (*domain.Message, error)
	DecryptMessage(ctx context.Context, message *domain.Message, userID string, clientIP string, userAgent string) (string, error)
}

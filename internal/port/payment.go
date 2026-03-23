package port

import (
	"context"

	"vidra-core/internal/domain"
)

// PaymentService defines the interface for payment operations.
type PaymentService interface {
	CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	GetWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	GetWalletBalance(ctx context.Context, userID string) (int64, error)
	CreatePaymentIntent(ctx context.Context, userID string, videoID *string, amount int64) (*domain.IOTAPaymentIntent, error)
	GetPaymentIntent(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error)
	GetTransactionHistory(ctx context.Context, userID string, limit, offset int) ([]*domain.IOTATransaction, error)
}

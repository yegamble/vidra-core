package payments

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type IOTARepository interface {
	CreateWallet(ctx context.Context, wallet *domain.IOTAWallet) error
	GetWalletByUserID(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	GetWalletByID(ctx context.Context, walletID string) (*domain.IOTAWallet, error)
	UpdateWalletBalance(ctx context.Context, walletID string, balance int64) error
	CreatePaymentIntent(ctx context.Context, intent *domain.IOTAPaymentIntent) error
	GetPaymentIntentByID(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error)
	UpdatePaymentIntentStatus(ctx context.Context, intentID string, status domain.PaymentIntentStatus, txID *string) error
	GetActivePaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error)
	GetExpiredPaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error)
	CreateTransaction(ctx context.Context, tx *domain.IOTATransaction) error
	GetTransactionByHash(ctx context.Context, txHash string) (*domain.IOTATransaction, error)
	UpdateTransactionStatus(ctx context.Context, txID string, status domain.TransactionStatus, confirmations int) error
	GetTransactionsByWalletID(ctx context.Context, walletID string, limit, offset int) ([]*domain.IOTATransaction, error)
}

type IOTAClient interface {
	GenerateKeypair() (privateKey []byte, publicKey []byte, err error)
	DeriveAddress(publicKey []byte) (string, error)
	ValidateAddress(address string) bool
	GetBalance(ctx context.Context, address string) (int64, error)
	GetNodeStatus(ctx context.Context) error
	QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]domain.ReceivedTransaction, error)
}

type PaymentService struct {
	repo          IOTARepository
	client        IOTAClient
	encryptionKey []byte
}

func NewPaymentService(repo IOTARepository, client IOTAClient, encryptionKey []byte) *PaymentService {
	return &PaymentService{
		repo:          repo,
		client:        client,
		encryptionKey: encryptionKey,
	}
}

func (s *PaymentService) CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	existing, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil && err != domain.ErrWalletNotFound {
		return nil, fmt.Errorf("failed to check existing wallet: %w", err)
	}
	if existing != nil {
		return nil, domain.ErrWalletAlreadyExists
	}

	privateKey, publicKey, err := s.client.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	address, err := s.client.DeriveAddress(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive address: %w", err)
	}

	encryptedKey, nonce, err := s.EncryptPrivateKey(hex.EncodeToString(privateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt private key: %w", err)
	}

	wallet := &domain.IOTAWallet{
		ID:                  uuid.New().String(),
		UserID:              userID,
		EncryptedPrivateKey: encryptedKey,
		PrivateKeyNonce:     nonce,
		PublicKey:           hex.EncodeToString(publicKey),
		Address:             address,
		BalanceIOTA:         0,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	if err := s.repo.CreateWallet(ctx, wallet); err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	return wallet, nil
}

func (s *PaymentService) GetWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	return s.repo.GetWalletByUserID(ctx, userID)
}

func (s *PaymentService) GetWalletBalance(ctx context.Context, userID string) (int64, error) {
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}

	balance, err := s.client.GetBalance(ctx, wallet.Address)
	if err != nil {
		return 0, fmt.Errorf("failed to get balance from network: %w", err)
	}

	if balance != wallet.BalanceIOTA {
		if err := s.repo.UpdateWalletBalance(ctx, wallet.ID, balance); err != nil {
			return balance, nil
		}
	}

	return balance, nil
}

func (s *PaymentService) CreatePaymentIntent(ctx context.Context, userID string, videoID *string, amount int64) (*domain.IOTAPaymentIntent, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	paymentAddress := wallet.Address

	intent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     amount,
		PaymentAddress: paymentAddress,
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if videoID != nil {
		intent.VideoID = sql.NullString{String: *videoID, Valid: true}
	}

	if err := s.repo.CreatePaymentIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("failed to create payment intent: %w", err)
	}

	return intent, nil
}

func (s *PaymentService) GetPaymentIntent(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error) {
	return s.repo.GetPaymentIntentByID(ctx, intentID)
}

func (s *PaymentService) GetTransactionHistory(ctx context.Context, userID string, limit, offset int) ([]*domain.IOTATransaction, error) {
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	transactions, err := s.repo.GetTransactionsByWalletID(ctx, wallet.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	if transactions == nil {
		return []*domain.IOTATransaction{}, nil
	}

	return transactions, nil
}

func (s *PaymentService) EncryptPrivateKey(seed string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}

	encrypted := gcm.Seal(nil, nonce, []byte(seed), nil)
	return encrypted, nonce, nil
}

func (s *PaymentService) DecryptPrivateKey(encryptedSeed, nonce []byte) (string, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plaintext, err := gcm.Open(nil, nonce, encryptedSeed, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func (s *PaymentService) DetectPayment(ctx context.Context, intentID string) error {
	intent, err := s.repo.GetPaymentIntentByID(ctx, intentID)
	if err != nil {
		return err
	}

	if intent.Status == domain.PaymentIntentStatusPaid {
		return domain.ErrPaymentAlreadyPaid
	}

	if time.Now().After(intent.ExpiresAt) {
		return domain.ErrPaymentIntentExpired
	}

	txs, err := s.client.QueryTransactionBlocks(ctx, intent.PaymentAddress, 50)
	if err != nil {
		return fmt.Errorf("failed to query transaction blocks: %w", err)
	}

	// Filter transactions by timestamp: only count those after intent creation (with 5s clock-skew buffer).
	thresholdMs := intent.CreatedAt.UnixMilli() - 5000
	var totalAmount int64
	var txDigest string
	for _, tx := range txs {
		if tx.TimestampMs < thresholdMs {
			continue
		}
		if txDigest == "" {
			txDigest = tx.Digest
		}
		totalAmount += tx.AmountIOTA
	}

	if totalAmount >= intent.AmountIOTA {
		wallet, walletErr := s.repo.GetWalletByUserID(ctx, intent.UserID)
		if walletErr != nil {
			slog.Info(fmt.Sprintf("WARNING: failed to find wallet for payment intent %s: %v", intent.ID, walletErr))
		}

		iotaTx := &domain.IOTATransaction{
			ID:                uuid.New().String(),
			TransactionDigest: txDigest,
			AmountIOTA:        intent.AmountIOTA,
			TxType:            domain.TransactionTypePayment,
			Status:            domain.TransactionStatusConfirmed,
			Confirmations:     10,
			ToAddress:         sql.NullString{String: intent.PaymentAddress, Valid: true},
			CreatedAt:         time.Now(),
		}

		if wallet != nil {
			iotaTx.WalletID = sql.NullString{String: wallet.ID, Valid: true}
		}

		if err := s.repo.CreateTransaction(ctx, iotaTx); err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}

		if err := s.repo.UpdatePaymentIntentStatus(ctx, intent.ID, domain.PaymentIntentStatusPaid, &txDigest); err != nil {
			return fmt.Errorf("failed to update payment intent: %w", err)
		}
	}

	return nil
}

func (s *PaymentService) ExpirePaymentIntents(ctx context.Context) error {
	expiredIntents, err := s.repo.GetExpiredPaymentIntents(ctx)
	if err != nil {
		return fmt.Errorf("failed to get expired intents: %w", err)
	}

	for _, intent := range expiredIntents {
		if err := s.repo.UpdatePaymentIntentStatus(ctx, intent.ID, domain.PaymentIntentStatusExpired, nil); err != nil {
			return fmt.Errorf("failed to expire intent %s: %w", intent.ID, err)
		}
	}

	return nil
}

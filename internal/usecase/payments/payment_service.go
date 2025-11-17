package payments

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
)

// IOTARepository defines the interface for IOTA data persistence
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

// IOTAClient defines the interface for IOTA network operations
type IOTAClient interface {
	GenerateSeed() (string, error)
	DeriveAddress(seed string, index uint32) (string, error)
	ValidateAddress(address string) bool
	GetBalance(ctx context.Context, address string) (int64, error)
}

// PaymentService handles payment-related business logic
type PaymentService struct {
	repo          IOTARepository
	client        IOTAClient
	encryptionKey []byte
}

// NewPaymentService creates a new payment service
func NewPaymentService(repo IOTARepository, client IOTAClient, encryptionKey []byte) *PaymentService {
	return &PaymentService{
		repo:          repo,
		client:        client,
		encryptionKey: encryptionKey,
	}
}

// CreateWallet creates a new IOTA wallet for a user
func (s *PaymentService) CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	// Check if wallet already exists
	existing, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil && err != domain.ErrWalletNotFound {
		return nil, fmt.Errorf("failed to check existing wallet: %w", err)
	}
	if existing != nil {
		return nil, domain.ErrWalletAlreadyExists
	}

	// Generate new seed
	seed, err := s.client.GenerateSeed()
	if err != nil {
		return nil, fmt.Errorf("failed to generate seed: %w", err)
	}

	// Derive address from seed
	address, err := s.client.DeriveAddress(seed, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to derive address: %w", err)
	}

	// Encrypt seed
	encryptedSeed, nonce, err := s.EncryptSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt seed: %w", err)
	}

	// Create wallet
	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: encryptedSeed,
		SeedNonce:     nonce,
		Address:       address,
		BalanceIOTA:   0,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.repo.CreateWallet(ctx, wallet); err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	return wallet, nil
}

// GetWallet retrieves a wallet by user ID
func (s *PaymentService) GetWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	return s.repo.GetWalletByUserID(ctx, userID)
}

// GetWalletBalance retrieves the current balance for a user's wallet
func (s *PaymentService) GetWalletBalance(ctx context.Context, userID string) (int64, error) {
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}

	// Query the IOTA network for the latest balance
	balance, err := s.client.GetBalance(ctx, wallet.Address)
	if err != nil {
		return 0, fmt.Errorf("failed to get balance from network: %w", err)
	}

	// Update stored balance if it changed
	if balance != wallet.BalanceIOTA {
		if err := s.repo.UpdateWalletBalance(ctx, wallet.ID, balance); err != nil {
			// Log error but return the balance anyway
			return balance, nil
		}
	}

	return balance, nil
}

// CreatePaymentIntent creates a new payment intent
func (s *PaymentService) CreatePaymentIntent(ctx context.Context, userID string, videoID *string, amount int64) (*domain.IOTAPaymentIntent, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	// Verify user has a wallet
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Generate a unique payment address (for this simplified version, use the wallet address)
	paymentAddress := wallet.Address

	// Create payment intent
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

// GetPaymentIntent retrieves a payment intent by ID
func (s *PaymentService) GetPaymentIntent(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error) {
	return s.repo.GetPaymentIntentByID(ctx, intentID)
}

// GetTransactionHistory retrieves transaction history for a user
func (s *PaymentService) GetTransactionHistory(ctx context.Context, userID string, limit, offset int) ([]*domain.IOTATransaction, error) {
	// Get user's wallet
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Get transactions for the wallet
	return s.repo.GetTransactionsByWalletID(ctx, wallet.ID, limit, offset)
}

// EncryptSeed encrypts the IOTA seed using AES-GCM
func (s *PaymentService) EncryptSeed(seed string) ([]byte, []byte, error) {
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

// DecryptSeed decrypts an encrypted IOTA seed
func (s *PaymentService) DecryptSeed(encryptedSeed, nonce []byte) (string, error) {
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

// Helper function to repeat a string n times
func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

// DetectPayment checks if payment has been received for a payment intent
func (s *PaymentService) DetectPayment(ctx context.Context, intentID string) error {
	// Get the payment intent
	intent, err := s.repo.GetPaymentIntentByID(ctx, intentID)
	if err != nil {
		return err
	}

	// Check if already paid
	if intent.Status == domain.PaymentIntentStatusPaid {
		return domain.ErrPaymentAlreadyPaid
	}

	// Check if expired
	if time.Now().After(intent.ExpiresAt) {
		return domain.ErrPaymentIntentExpired
	}

	// Check the balance of the payment address
	balance, err := s.client.GetBalance(ctx, intent.PaymentAddress)
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	// If balance is greater than or equal to the expected amount, mark as paid
	if balance >= intent.AmountIOTA {
		// Get user's wallet to associate the transaction
		wallet, _ := s.repo.GetWalletByUserID(ctx, intent.UserID)

		// Create a transaction record
		txHash := fmt.Sprintf("iota-payment-%s-%d", intent.ID, time.Now().Unix())
		tx := &domain.IOTATransaction{
			ID:              uuid.New().String(),
			TransactionHash: txHash,
			AmountIOTA:      intent.AmountIOTA,
			TxType:          domain.TransactionTypePayment,
			Status:          domain.TransactionStatusConfirmed,
			Confirmations:   10,
			ToAddress:       sql.NullString{String: intent.PaymentAddress, Valid: true},
			CreatedAt:       time.Now(),
		}

		if wallet != nil {
			tx.WalletID = sql.NullString{String: wallet.ID, Valid: true}
		}

		// Create transaction
		if err := s.repo.CreateTransaction(ctx, tx); err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}

		// Update payment intent status
		if err := s.repo.UpdatePaymentIntentStatus(ctx, intent.ID, domain.PaymentIntentStatusPaid, &txHash); err != nil {
			return fmt.Errorf("failed to update payment intent: %w", err)
		}
	}

	return nil
}

// ExpirePaymentIntents marks expired payment intents as expired
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

package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
	"vidra-core/internal/domain"
)

// IOTARepository defines the interface for IOTA data operations
type IOTARepository interface {
	GetActivePaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error)
	GetExpiredPaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error)
	UpdatePaymentIntentStatus(ctx context.Context, intentID string, status domain.PaymentIntentStatus, txID *string) error
	GetWalletByID(ctx context.Context, walletID string) (*domain.IOTAWallet, error)
	GetWalletByUserID(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	CreateTransaction(ctx context.Context, tx *domain.IOTATransaction) error
	GetTransactionByHash(ctx context.Context, txHash string) (*domain.IOTATransaction, error)
	UpdateTransactionStatus(ctx context.Context, txID string, status domain.TransactionStatus, confirmations int) error
	UpdateWalletBalance(ctx context.Context, walletID string, balance int64) error
}

// IOTAClient defines the interface for IOTA network operations
type IOTAClient interface {
	GetBalance(ctx context.Context, address string) (int64, error)
}

// IOTAPaymentWorker monitors and processes IOTA payments
type IOTAPaymentWorker struct {
	repo   IOTARepository
	client IOTAClient
	ticker *time.Ticker
	done   chan bool
}

// NewIOTAPaymentWorker creates a new IOTA payment worker
func NewIOTAPaymentWorker(repo IOTARepository, client IOTAClient) *IOTAPaymentWorker {
	return &IOTAPaymentWorker{
		repo:   repo,
		client: client,
		done:   make(chan bool),
	}
}

// Start begins the worker's monitoring loop
func (w *IOTAPaymentWorker) Start(ctx context.Context, interval time.Duration) {
	w.ticker = time.NewTicker(interval)
	log.Printf("IOTA payment worker started with interval %v", interval)

	go func() {
		for {
			select {
			case <-w.ticker.C:
				if err := w.processPayments(ctx); err != nil {
					log.Printf("Error processing payments: %v", err)
				}
			case <-w.done:
				log.Println("IOTA payment worker stopped")
				return
			case <-ctx.Done():
				log.Println("IOTA payment worker context cancelled")
				return
			}
		}
	}()
}

// Stop stops the worker
func (w *IOTAPaymentWorker) Stop() {
	if w.ticker != nil {
		w.ticker.Stop()
	}
	close(w.done)
}

// processPayments checks for new payments and updates payment intents
func (w *IOTAPaymentWorker) processPayments(ctx context.Context) error {
	// Get active payment intents
	intents, err := w.repo.GetActivePaymentIntents(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active payment intents: %w", err)
	}

	for _, intent := range intents {
		if err := w.checkPaymentIntent(ctx, intent); err != nil {
			log.Printf("Error checking payment intent %s: %v", intent.ID, err)
			// Continue with other intents even if one fails
			continue
		}
	}

	// Expire old payment intents
	if err := w.expireOldIntents(ctx); err != nil {
		log.Printf("Error expiring old intents: %v", err)
	}

	return nil
}

// checkPaymentIntent checks if a payment has been received for an intent
func (w *IOTAPaymentWorker) checkPaymentIntent(ctx context.Context, intent *domain.IOTAPaymentIntent) error {
	// Check the balance of the payment address
	balance, err := w.client.GetBalance(ctx, intent.PaymentAddress)
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	// If balance is greater than or equal to the expected amount, mark as paid
	if balance >= intent.AmountIOTA {
		// Create a transaction record
		txHash := fmt.Sprintf("iota-payment-%s", intent.ID)
		tx := &domain.IOTATransaction{
			ID:                fmt.Sprintf("tx-%s", intent.ID),
			TransactionDigest: txHash,
			AmountIOTA:        intent.AmountIOTA,
			TxType:            domain.TransactionTypePayment,
			Status:            domain.TransactionStatusConfirmed,
			Confirmations:     10,
			ToAddress:         sql.NullString{String: intent.PaymentAddress, Valid: true},
			CreatedAt:         time.Now(),
		}

		if intent.UserID != "" {
			// Get user's wallet
			wallet, err := w.repo.GetWalletByUserID(ctx, intent.UserID)
			if err == nil {
				tx.WalletID = sql.NullString{String: wallet.ID, Valid: true}
			}
		}

		// Create transaction
		if err := w.repo.CreateTransaction(ctx, tx); err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}

		// Update payment intent status
		if err := w.repo.UpdatePaymentIntentStatus(ctx, intent.ID, domain.PaymentIntentStatusPaid, &txHash); err != nil {
			return fmt.Errorf("failed to update payment intent: %w", err)
		}

		log.Printf("Payment intent %s marked as paid", intent.ID)
	}

	return nil
}

// expireOldIntents marks expired payment intents as expired
func (w *IOTAPaymentWorker) expireOldIntents(ctx context.Context) error {
	expiredIntents, err := w.repo.GetExpiredPaymentIntents(ctx)
	if err != nil {
		return fmt.Errorf("failed to get expired intents: %w", err)
	}

	for _, intent := range expiredIntents {
		if err := w.repo.UpdatePaymentIntentStatus(ctx, intent.ID, domain.PaymentIntentStatusExpired, nil); err != nil {
			log.Printf("Failed to expire intent %s: %v", intent.ID, err)
			continue
		}
		log.Printf("Payment intent %s expired", intent.ID)
	}

	return nil
}

// MonitorTransactions monitors pending transactions for confirmations
func (w *IOTAPaymentWorker) MonitorTransactions(ctx context.Context) error {
	// This would monitor pending transactions and update their confirmation status
	// Stub implementation for now
	return nil
}

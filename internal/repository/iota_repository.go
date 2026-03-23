package repository

import (
	"context"
	"database/sql"

	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

type IOTARepository struct {
	db *sqlx.DB
}

func NewIOTARepository(db *sqlx.DB) *IOTARepository {
	return &IOTARepository{
		db: db,
	}
}

func (r *IOTARepository) CreateWallet(ctx context.Context, wallet *domain.IOTAWallet) error {
	query := `
		INSERT INTO iota_wallets (id, user_id, encrypted_private_key, private_key_nonce, public_key, address, balance_iota, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query,
		wallet.ID, wallet.UserID, wallet.EncryptedPrivateKey, wallet.PrivateKeyNonce,
		wallet.PublicKey, wallet.Address, wallet.BalanceIOTA,
	).Scan(&wallet.CreatedAt, &wallet.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrWalletAlreadyExists
		}
		if isForeignKeyViolation(err) {
			return domain.ErrUserNotFound
		}
		return err
	}
	return nil
}

func (r *IOTARepository) GetWalletByUserID(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	var wallet domain.IOTAWallet
	query := `SELECT * FROM iota_wallets WHERE user_id = $1`
	err := r.db.GetContext(ctx, &wallet, query, userID)
	if err == sql.ErrNoRows {
		return nil, domain.ErrWalletNotFound
	}
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *IOTARepository) GetWalletByID(ctx context.Context, walletID string) (*domain.IOTAWallet, error) {
	var wallet domain.IOTAWallet
	query := `SELECT * FROM iota_wallets WHERE id = $1`
	err := r.db.GetContext(ctx, &wallet, query, walletID)
	if err == sql.ErrNoRows {
		return nil, domain.ErrWalletNotFound
	}
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *IOTARepository) UpdateWalletBalance(ctx context.Context, walletID string, balance int64) error {
	query := `UPDATE iota_wallets SET balance_iota = $1, updated_at = NOW() WHERE id = $2`
	result, err := r.db.ExecContext(ctx, query, balance, walletID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrWalletNotFound
	}
	return nil
}

func (r *IOTARepository) CreatePaymentIntent(ctx context.Context, intent *domain.IOTAPaymentIntent) error {
	var metadata any
	if len(intent.Metadata) > 0 {
		metadata = string(intent.Metadata)
	}

	query := `
		INSERT INTO iota_payment_intents
		(id, user_id, video_id, amount_iota, payment_address, status, expires_at, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NOW(), NOW())
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query,
		intent.ID, intent.UserID, intent.VideoID, intent.AmountIOTA,
		intent.PaymentAddress, intent.Status, intent.ExpiresAt, metadata,
	).Scan(&intent.CreatedAt, &intent.UpdatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return domain.ErrVideoNotFound
		}
		return err
	}
	return nil
}

func (r *IOTARepository) GetPaymentIntentByID(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error) {
	var intent domain.IOTAPaymentIntent
	query := `SELECT * FROM iota_payment_intents WHERE id = $1`
	err := r.db.GetContext(ctx, &intent, query, intentID)
	if err == sql.ErrNoRows {
		return nil, domain.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &intent, nil
}

func (r *IOTARepository) UpdatePaymentIntentStatus(ctx context.Context, intentID string, status domain.PaymentIntentStatus, txID *string) error {
	query := `
		UPDATE iota_payment_intents
		SET status = $1::varchar,
		    transaction_id = $2,
		    paid_at = CASE WHEN $1::varchar = 'paid' THEN NOW() ELSE paid_at END,
		    updated_at = NOW()
		WHERE id = $3
	`
	result, err := r.db.ExecContext(ctx, query, status, txID, intentID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrPaymentIntentNotFound
	}
	return nil
}

func (r *IOTARepository) GetActivePaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	var intents []*domain.IOTAPaymentIntent
	query := `
		SELECT * FROM iota_payment_intents
		WHERE status = 'pending' AND expires_at > NOW()
		ORDER BY created_at DESC
	`
	err := r.db.SelectContext(ctx, &intents, query)
	if err != nil {
		return nil, err
	}
	return intents, nil
}

func (r *IOTARepository) GetExpiredPaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	var intents []*domain.IOTAPaymentIntent
	query := `
		SELECT * FROM iota_payment_intents
		WHERE status = 'pending' AND expires_at <= NOW()
		ORDER BY expires_at ASC
	`
	err := r.db.SelectContext(ctx, &intents, query)
	if err != nil {
		return nil, err
	}
	return intents, nil
}

func (r *IOTARepository) CreateTransaction(ctx context.Context, tx *domain.IOTATransaction) error {
	var metadata any
	if len(tx.Metadata) > 0 {
		metadata = string(tx.Metadata)
	}

	query := `
		INSERT INTO iota_transactions
		(id, wallet_id, transaction_digest, amount_iota, tx_type, status, confirmations, gas_budget, gas_used, from_address, to_address, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, NOW())
		RETURNING created_at
	`
	return r.db.QueryRowContext(ctx, query,
		tx.ID, tx.WalletID, tx.TransactionDigest, tx.AmountIOTA, tx.TxType,
		tx.Status, tx.Confirmations, tx.GasBudget, tx.GasUsed, tx.FromAddress, tx.ToAddress, metadata,
	).Scan(&tx.CreatedAt)
}

func (r *IOTARepository) GetTransactionByHash(ctx context.Context, txHash string) (*domain.IOTATransaction, error) {
	var tx domain.IOTATransaction
	query := `SELECT * FROM iota_transactions WHERE transaction_digest = $1`
	err := r.db.GetContext(ctx, &tx, query, txHash)
	if err == sql.ErrNoRows {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *IOTARepository) UpdateTransactionStatus(ctx context.Context, txID string, status domain.TransactionStatus, confirmations int) error {
	query := `
		UPDATE iota_transactions
		SET status = $1::varchar,
		    confirmations = $2,
		    confirmed_at = CASE WHEN $1::varchar = 'confirmed' THEN NOW() ELSE confirmed_at END
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, status, confirmations, txID)
	return err
}

func (r *IOTARepository) GetTransactionsByWalletID(ctx context.Context, walletID string, limit, offset int) ([]*domain.IOTATransaction, error) {
	var transactions []*domain.IOTATransaction
	query := `
		SELECT * FROM iota_transactions
		WHERE wallet_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	err := r.db.SelectContext(ctx, &transactions, query, walletID, limit, offset)
	if err != nil {
		return nil, err
	}
	return transactions, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && (err.Error() == "pq: duplicate key value violates unique constraint" ||
		containsString(err.Error(), "unique constraint"))
}

func isForeignKeyViolation(err error) bool {
	return err != nil && containsString(err.Error(), "foreign key")
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

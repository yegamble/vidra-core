package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIOTAMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newIOTARepo(t *testing.T) (*IOTARepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupIOTAMockDB(t)
	repo := NewIOTARepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestIOTARepository_Unit_CreateWallet(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()
		wallet := &domain.IOTAWallet{
			ID:                  uuid.New().String(),
			UserID:              uuid.New().String(),
			EncryptedPrivateKey: []byte("encrypted-seed"),
			PrivateKeyNonce:     []byte("nonce"),
			PublicKey:           "public-key",
			Address:             "iota1address",
			BalanceIOTA:         0,
		}

		rows := sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_wallets (id, user_id, encrypted_private_key, private_key_nonce, public_key, address, balance_iota, created_at, updated_at)`)).
			WithArgs(wallet.ID, wallet.UserID, wallet.EncryptedPrivateKey, wallet.PrivateKeyNonce, wallet.PublicKey, wallet.Address, wallet.BalanceIOTA).
			WillReturnRows(rows)

		err := repo.CreateWallet(ctx, wallet)
		require.NoError(t, err)
		assert.Equal(t, now, wallet.CreatedAt)
		assert.Equal(t, now, wallet.UpdatedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		wallet := &domain.IOTAWallet{
			ID:                  uuid.New().String(),
			UserID:              uuid.New().String(),
			EncryptedPrivateKey: []byte("seed"),
			PrivateKeyNonce:     []byte("nonce"),
			Address:             "address",
			BalanceIOTA:         0,
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_wallets`)).
			WillReturnError(sql.ErrConnDone)

		err := repo.CreateWallet(ctx, wallet)
		require.Error(t, err)
	})
}

func TestIOTARepository_Unit_GetWalletByUserID(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		userID := uuid.New().String()
		walletID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "encrypted_private_key", "private_key_nonce", "public_key", "address", "balance_iota", "created_at", "updated_at",
		}).AddRow(walletID, userID, []byte("seed"), []byte("nonce"), "public-key", "address", int64(100), now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_wallets WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(rows)

		wallet, err := repo.GetWalletByUserID(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, wallet)
		assert.Equal(t, walletID, wallet.ID)
		assert.Equal(t, userID, wallet.UserID)
		assert.Equal(t, int64(100), wallet.BalanceIOTA)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_wallets`)).
			WithArgs(userID).
			WillReturnError(sql.ErrNoRows)

		wallet, err := repo.GetWalletByUserID(ctx, userID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
		assert.Nil(t, wallet)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT *`)).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		wallet, err := repo.GetWalletByUserID(ctx, userID)
		require.Error(t, err)
		assert.Nil(t, wallet)
	})
}

func TestIOTARepository_Unit_GetWalletByID(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()
		userID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "encrypted_private_key", "private_key_nonce", "public_key", "address", "balance_iota", "created_at", "updated_at",
		}).AddRow(walletID, userID, []byte("seed"), []byte("nonce"), "public-key", "address", int64(200), now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_wallets WHERE id = $1`)).
			WithArgs(walletID).
			WillReturnRows(rows)

		wallet, err := repo.GetWalletByID(ctx, walletID)
		require.NoError(t, err)
		require.NotNil(t, wallet)
		assert.Equal(t, walletID, wallet.ID)
		assert.Equal(t, int64(200), wallet.BalanceIOTA)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_wallets`)).
			WithArgs(walletID).
			WillReturnError(sql.ErrNoRows)

		wallet, err := repo.GetWalletByID(ctx, walletID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
		assert.Nil(t, wallet)
	})
}

func TestIOTARepository_Unit_UpdateWalletBalance(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()
		newBalance := int64(500)

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_wallets SET balance_iota = $1, updated_at = NOW() WHERE id = $2`)).
			WithArgs(newBalance, walletID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateWalletBalance(ctx, walletID, newBalance)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found (0 rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_wallets`)).
			WithArgs(int64(100), walletID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateWalletBalance(ctx, walletID, 100)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_wallets`)).
			WithArgs(int64(100), walletID).
			WillReturnError(sql.ErrConnDone)

		err := repo.UpdateWalletBalance(ctx, walletID, 100)
		require.Error(t, err)
	})
}

func TestIOTARepository_Unit_CreatePaymentIntent(t *testing.T) {
	ctx := context.Background()

	t.Run("success with metadata", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()
		intent := &domain.IOTAPaymentIntent{
			ID:             uuid.New().String(),
			UserID:         uuid.New().String(),
			VideoID:        sql.NullString{String: uuid.New().String(), Valid: true},
			AmountIOTA:     100,
			PaymentAddress: "payment-address",
			Status:         domain.PaymentIntentStatusPending,
			ExpiresAt:      now.Add(1 * time.Hour),
			Metadata:       []byte(`{"key":"value"}`),
		}

		rows := sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_payment_intents`)).
			WithArgs(
				intent.ID, intent.UserID, intent.VideoID, intent.AmountIOTA,
				intent.PaymentAddress, intent.Status, intent.ExpiresAt, string(intent.Metadata),
			).
			WillReturnRows(rows)

		err := repo.CreatePaymentIntent(ctx, intent)
		require.NoError(t, err)
		assert.Equal(t, now, intent.CreatedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success without metadata", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()
		intent := &domain.IOTAPaymentIntent{
			ID:             uuid.New().String(),
			UserID:         uuid.New().String(),
			VideoID:        sql.NullString{},
			AmountIOTA:     50,
			PaymentAddress: "address",
			Status:         domain.PaymentIntentStatusPending,
			ExpiresAt:      now.Add(30 * time.Minute),
			Metadata:       []byte{},
		}

		rows := sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_payment_intents`)).
			WithArgs(
				intent.ID, intent.UserID, intent.VideoID, intent.AmountIOTA,
				intent.PaymentAddress, intent.Status, intent.ExpiresAt, nil,
			).
			WillReturnRows(rows)

		err := repo.CreatePaymentIntent(ctx, intent)
		require.NoError(t, err)
	})
}

func TestIOTARepository_Unit_GetPaymentIntentByID(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		intentID := uuid.New().String()
		userID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "video_id", "amount_iota", "payment_address",
			"status", "expires_at", "paid_at", "transaction_id", "metadata", "created_at", "updated_at",
		}).AddRow(
			intentID, userID, sql.NullString{}, int64(100), "address",
			domain.PaymentIntentStatusPending, now.Add(1*time.Hour), sql.NullTime{}, sql.NullString{}, []byte{}, now, now,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_payment_intents WHERE id = $1`)).
			WithArgs(intentID).
			WillReturnRows(rows)

		intent, err := repo.GetPaymentIntentByID(ctx, intentID)
		require.NoError(t, err)
		require.NotNil(t, intent)
		assert.Equal(t, intentID, intent.ID)
		assert.Equal(t, domain.PaymentIntentStatusPending, intent.Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		intentID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_payment_intents`)).
			WithArgs(intentID).
			WillReturnError(sql.ErrNoRows)

		intent, err := repo.GetPaymentIntentByID(ctx, intentID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrPaymentIntentNotFound)
		assert.Nil(t, intent)
	})
}

func TestIOTARepository_Unit_UpdatePaymentIntentStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success with transaction ID", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		intentID := uuid.New().String()
		txID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_payment_intents SET status = $1::varchar, transaction_id = $2`)).
			WithArgs(domain.PaymentIntentStatusPaid, &txID, intentID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdatePaymentIntentStatus(ctx, intentID, domain.PaymentIntentStatusPaid, &txID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success without transaction ID", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		intentID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_payment_intents`)).
			WithArgs(domain.PaymentIntentStatusExpired, (*string)(nil), intentID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdatePaymentIntentStatus(ctx, intentID, domain.PaymentIntentStatusExpired, nil)
		require.NoError(t, err)
	})

	t.Run("not found (0 rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		intentID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_payment_intents`)).
			WithArgs(domain.PaymentIntentStatusPaid, (*string)(nil), intentID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdatePaymentIntentStatus(ctx, intentID, domain.PaymentIntentStatusPaid, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrPaymentIntentNotFound)
	})
}

func TestIOTARepository_Unit_GetActivePaymentIntents(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "video_id", "amount_iota", "payment_address",
			"status", "expires_at", "paid_at", "transaction_id", "metadata", "created_at", "updated_at",
		}).
			AddRow(uuid.New().String(), uuid.New().String(), sql.NullString{}, int64(100), "addr1", domain.PaymentIntentStatusPending, now.Add(1*time.Hour), sql.NullTime{}, sql.NullString{}, []byte{}, now, now).
			AddRow(uuid.New().String(), uuid.New().String(), sql.NullString{}, int64(200), "addr2", domain.PaymentIntentStatusPending, now.Add(2*time.Hour), sql.NullTime{}, sql.NullString{}, []byte{}, now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_payment_intents WHERE status = 'pending' AND expires_at > NOW()`)).
			WillReturnRows(rows)

		intents, err := repo.GetActivePaymentIntents(ctx)
		require.NoError(t, err)
		require.Len(t, intents, 2)
		assert.Equal(t, domain.PaymentIntentStatusPending, intents[0].Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty results", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"id"})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_payment_intents`)).
			WillReturnRows(rows)

		intents, err := repo.GetActivePaymentIntents(ctx)
		require.NoError(t, err)
		assert.Empty(t, intents)
	})
}

func TestIOTARepository_Unit_GetExpiredPaymentIntents(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "video_id", "amount_iota", "payment_address",
			"status", "expires_at", "paid_at", "transaction_id", "metadata", "created_at", "updated_at",
		}).AddRow(uuid.New().String(), uuid.New().String(), sql.NullString{}, int64(50), "addr", domain.PaymentIntentStatusPending, now.Add(-1*time.Hour), sql.NullTime{}, sql.NullString{}, []byte{}, now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_payment_intents WHERE status = 'pending' AND expires_at <= NOW()`)).
			WillReturnRows(rows)

		intents, err := repo.GetExpiredPaymentIntents(ctx)
		require.NoError(t, err)
		require.Len(t, intents, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestIOTARepository_Unit_CreateTransaction(t *testing.T) {
	ctx := context.Background()

	t.Run("success with metadata", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()
		tx := &domain.IOTATransaction{
			ID:                uuid.New().String(),
			WalletID:          sql.NullString{String: uuid.New().String(), Valid: true},
			TransactionDigest: "hash123",
			AmountIOTA:        100,
			TxType:            "deposit",
			Status:            "pending",
			Confirmations:     0,
			FromAddress:       sql.NullString{String: "from-addr", Valid: true},
			ToAddress:         sql.NullString{String: "to-addr", Valid: true},
			Metadata:          []byte(`{"note":"test"}`),
		}

		rows := sqlmock.NewRows([]string{"created_at"}).AddRow(now)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_transactions`)).
			WithArgs(
				tx.ID, tx.WalletID, tx.TransactionDigest, tx.AmountIOTA, tx.TxType,
				tx.Status, tx.Confirmations, tx.GasBudget, tx.GasUsed, tx.FromAddress, tx.ToAddress, string(tx.Metadata),
			).
			WillReturnRows(rows)

		err := repo.CreateTransaction(ctx, tx)
		require.NoError(t, err)
		assert.Equal(t, now, tx.CreatedAt)
	})

	t.Run("success without metadata", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		now := time.Now()
		tx := &domain.IOTATransaction{
			ID:                uuid.New().String(),
			WalletID:          sql.NullString{},
			TransactionDigest: "hash",
			AmountIOTA:        50,
			TxType:            "withdrawal",
			Status:            "pending",
			Confirmations:     0,
			Metadata:          []byte{},
		}

		rows := sqlmock.NewRows([]string{"created_at"}).AddRow(now)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO iota_transactions`)).
			WithArgs(
				tx.ID, tx.WalletID, tx.TransactionDigest, tx.AmountIOTA, tx.TxType,
				tx.Status, tx.Confirmations, tx.GasBudget, tx.GasUsed, tx.FromAddress, tx.ToAddress, nil,
			).
			WillReturnRows(rows)

		err := repo.CreateTransaction(ctx, tx)
		require.NoError(t, err)
	})
}

func TestIOTARepository_Unit_GetTransactionByHash(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		txHash := "transaction-hash"
		txID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "wallet_id", "transaction_digest", "amount_iota", "tx_type", "status",
			"confirmations", "from_address", "to_address", "metadata", "confirmed_at", "created_at",
		}).AddRow(
			txID, sql.NullString{}, txHash, int64(100), "deposit", "confirmed",
			10, sql.NullString{}, sql.NullString{}, []byte{}, sql.NullTime{}, now,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_transactions WHERE transaction_digest = $1`)).
			WithArgs(txHash).
			WillReturnRows(rows)

		tx, err := repo.GetTransactionByHash(ctx, txHash)
		require.NoError(t, err)
		require.NotNil(t, tx)
		assert.Equal(t, txHash, tx.TransactionDigest)
		assert.Equal(t, int64(100), tx.AmountIOTA)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		txHash := "nonexistent"

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_transactions`)).
			WithArgs(txHash).
			WillReturnError(sql.ErrNoRows)

		tx, err := repo.GetTransactionByHash(ctx, txHash)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrTransactionNotFound)
		assert.Nil(t, tx)
	})
}

func TestIOTARepository_Unit_UpdateTransactionStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		txID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_transactions SET status = $1::varchar, confirmations = $2`)).
			WithArgs("confirmed", 10, txID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateTransactionStatus(ctx, txID, "confirmed", 10)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		txID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE iota_transactions`)).
			WithArgs("pending", 0, txID).
			WillReturnError(sql.ErrConnDone)

		err := repo.UpdateTransactionStatus(ctx, txID, "pending", 0)
		require.Error(t, err)
	})
}

func TestIOTARepository_Unit_GetTransactionsByWalletID(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "wallet_id", "transaction_digest", "amount_iota", "tx_type", "status",
			"confirmations", "from_address", "to_address", "metadata", "confirmed_at", "created_at",
		}).
			AddRow(uuid.New().String(), sql.NullString{String: walletID, Valid: true}, "hash1", int64(100), "deposit", "confirmed", 10, sql.NullString{}, sql.NullString{}, []byte{}, sql.NullTime{}, now).
			AddRow(uuid.New().String(), sql.NullString{String: walletID, Valid: true}, "hash2", int64(50), "withdrawal", "pending", 0, sql.NullString{}, sql.NullString{}, []byte{}, sql.NullTime{}, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_transactions WHERE wallet_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`)).
			WithArgs(walletID, 10, 0).
			WillReturnRows(rows)

		txs, err := repo.GetTransactionsByWalletID(ctx, walletID, 10, 0)
		require.NoError(t, err)
		require.Len(t, txs, 2)
		assert.Equal(t, "hash1", txs[0].TransactionDigest)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty results", func(t *testing.T) {
		repo, mock, cleanup := newIOTARepo(t)
		defer cleanup()

		walletID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"id"})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM iota_transactions`)).
			WithArgs(walletID, 10, 0).
			WillReturnRows(rows)

		txs, err := repo.GetTransactionsByWalletID(ctx, walletID, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, txs)
	})
}

func TestHelperFunctions(t *testing.T) {
	t.Run("isUniqueViolation", func(t *testing.T) {
		assert.False(t, isUniqueViolation(nil))
	})

	t.Run("isForeignKeyViolation", func(t *testing.T) {
		assert.False(t, isForeignKeyViolation(nil))
	})

	t.Run("containsString", func(t *testing.T) {
		assert.True(t, containsString("hello world", "world"))
		assert.False(t, containsString("hello", "goodbye"))
		assert.True(t, containsString("error: foreign key", "foreign key"))
	})

	t.Run("indexOf", func(t *testing.T) {
		assert.Equal(t, 6, indexOf("hello world", "world"))
		assert.Equal(t, -1, indexOf("hello", "goodbye"))
		assert.Equal(t, 0, indexOf("start here", "start"))
	})
}

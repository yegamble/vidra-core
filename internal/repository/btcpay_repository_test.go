package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBTCPayTestDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestBTCPayRepository_CreateInvoice(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	invoice := &domain.BTCPayInvoice{
		ID:              "uuid-1",
		BTCPayInvoiceID: "btcpay-inv-1",
		UserID:          "user-1",
		AmountSats:      100000,
		Currency:        "BTC",
		Status:          domain.InvoiceStatusNew,
		CheckoutLink:    "https://btcpay.example.com/i/btcpay-inv-1",
		ExpiresAt:       time.Now().Add(15 * time.Minute),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	mock.ExpectExec("INSERT INTO btcpay_invoices").
		WithArgs(
			invoice.ID, invoice.BTCPayInvoiceID, invoice.UserID, invoice.AmountSats,
			invoice.Currency, invoice.Status, invoice.CheckoutLink, invoice.BitcoinAddress,
			invoice.Metadata, invoice.ExpiresAt, invoice.CreatedAt, invoice.UpdatedAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateInvoice(context.Background(), invoice)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBTCPayRepository_GetInvoiceByID(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "btcpay_invoice_id", "user_id", "amount_sats", "currency", "status", "btcpay_checkout_link", "bitcoin_address", "metadata", "expires_at", "settled_at", "created_at", "updated_at"}).
		AddRow("uuid-1", "btcpay-inv-1", "user-1", int64(100000), "BTC", "New", "https://btcpay.example.com/i/1", sql.NullString{}, nil, now, sql.NullTime{}, now, now)

	mock.ExpectQuery("SELECT \\* FROM btcpay_invoices WHERE id = \\$1").
		WithArgs("uuid-1").
		WillReturnRows(rows)

	invoice, err := repo.GetInvoiceByID(context.Background(), "uuid-1")
	require.NoError(t, err)
	assert.Equal(t, "btcpay-inv-1", invoice.BTCPayInvoiceID)
	assert.Equal(t, int64(100000), invoice.AmountSats)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBTCPayRepository_GetInvoiceByID_NotFound(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	mock.ExpectQuery("SELECT \\* FROM btcpay_invoices WHERE id = \\$1").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetInvoiceByID(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, domain.ErrInvoiceNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBTCPayRepository_GetInvoicesByUser(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "btcpay_invoice_id", "user_id", "amount_sats", "currency", "status", "btcpay_checkout_link", "bitcoin_address", "metadata", "expires_at", "settled_at", "created_at", "updated_at"}).
		AddRow("uuid-1", "inv-1", "user-1", int64(100000), "BTC", "New", "", sql.NullString{}, nil, now, sql.NullTime{}, now, now).
		AddRow("uuid-2", "inv-2", "user-1", int64(200000), "BTC", "Settled", "", sql.NullString{}, nil, now, sql.NullTime{Valid: true, Time: now}, now, now)

	mock.ExpectQuery("SELECT \\* FROM btcpay_invoices WHERE user_id = \\$1").
		WithArgs("user-1", 20, 0).
		WillReturnRows(rows)

	invoices, err := repo.GetInvoicesByUser(context.Background(), "user-1", 20, 0)
	require.NoError(t, err)
	assert.Len(t, invoices, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBTCPayRepository_UpdateInvoiceStatus(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	mock.ExpectExec("UPDATE btcpay_invoices SET status = \\$1").
		WithArgs(domain.InvoiceStatusSettled, "btcpay-inv-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateInvoiceStatus(context.Background(), "btcpay-inv-1", domain.InvoiceStatusSettled)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBTCPayRepository_UpdateInvoiceStatus_NotFound(t *testing.T) {
	db, mock := newBTCPayTestDB(t)
	defer db.Close()
	repo := NewBTCPayRepository(db)

	mock.ExpectExec("UPDATE btcpay_invoices SET status = \\$1").
		WithArgs(domain.InvoiceStatusSettled, "nonexistent").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UpdateInvoiceStatus(context.Background(), "nonexistent", domain.InvoiceStatusSettled)
	assert.ErrorIs(t, err, domain.ErrInvoiceNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

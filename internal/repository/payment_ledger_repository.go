package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

// PaymentLedgerRepository persists and queries rows in the payment_ledger table.
// See migration 094 for the schema and invariants.
type PaymentLedgerRepository struct {
	db *sqlx.DB
}

// NewPaymentLedgerRepository creates a repository backed by the given sqlx DB.
func NewPaymentLedgerRepository(db *sqlx.DB) *PaymentLedgerRepository {
	return &PaymentLedgerRepository{db: db}
}

// Record inserts a ledger entry. Uses the UNIQUE idempotency_key index to make
// repeated calls (webhook retries, admin double-click) a no-op. If the key
// already exists, returns domain.ErrLedgerDuplicate.
func (r *PaymentLedgerRepository) Record(ctx context.Context, entry *domain.PaymentLedgerEntry) error {
	if !entry.EntryType.IsValid() {
		return domain.ErrLedgerEntryInvalid
	}
	if entry.IdempotencyKey == "" {
		return fmt.Errorf("payment_ledger: idempotency_key is required")
	}
	if entry.Currency == "" {
		entry.Currency = "BTC"
	}
	query := `
		INSERT INTO payment_ledger (
			user_id, counterparty_user_id, channel_id, entry_type, amount_sats,
			currency, invoice_id, payout_id, metadata, idempotency_key, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11, NOW()))
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, created_at
	`
	var id string
	var createdAt = entry.CreatedAt
	// JSONB column rejects an empty []byte — pass nil when metadata is unset.
	var metaArg interface{}
	if len(entry.Metadata) > 0 {
		metaArg = entry.Metadata
	}
	row := r.db.QueryRowxContext(ctx, query,
		entry.UserID,
		entry.CounterpartyUserID,
		entry.ChannelID,
		entry.EntryType,
		entry.AmountSats,
		entry.Currency,
		entry.InvoiceID,
		entry.PayoutID,
		metaArg,
		entry.IdempotencyKey,
		nullTimeOrNil(entry.CreatedAt),
	)
	if err := row.Scan(&id, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// ON CONFLICT DO NOTHING + RETURNING produces no rows on duplicate —
			// treat as idempotent success by reporting ErrLedgerDuplicate to the
			// caller, who can choose to ignore (webhook) or surface (payout service).
			return domain.ErrLedgerDuplicate
		}
		return fmt.Errorf("ledger insert: %w", err)
	}
	entry.ID = id
	entry.CreatedAt = createdAt
	return nil
}

// GetAvailableBalance returns SUM(amount_sats) for a user in a transaction.
// The caller is responsible for opening the transaction at the right isolation
// level (SERIALIZABLE for payout reservations).
func (r *PaymentLedgerRepository) GetAvailableBalance(ctx context.Context, tx sqlx.ExtContext, userID string) (int64, error) {
	if tx == nil {
		tx = r.db
	}
	var balance sql.NullInt64
	row := tx.QueryRowxContext(ctx,
		"SELECT COALESCE(SUM(amount_sats), 0) FROM payment_ledger WHERE user_id = $1", userID)
	if err := row.Scan(&balance); err != nil {
		return 0, fmt.Errorf("ledger balance: %w", err)
	}
	return balance.Int64, nil
}

// GetPendingPayoutSats returns ABS(SUM(negative amounts)) for payout_requested
// entries whose payout row is still pending or approved — the informational
// "pending payout" figure surfaced by /wallet/balance. Not subtracted from the
// available balance (it's already folded in via the payout_requested entry).
func (r *PaymentLedgerRepository) GetPendingPayoutSats(ctx context.Context, userID string) (int64, error) {
	const query = `
		SELECT COALESCE(SUM(-pl.amount_sats), 0)
		FROM payment_ledger pl
		JOIN btcpay_payouts p ON p.id = pl.payout_id
		WHERE pl.user_id = $1
		  AND pl.entry_type = 'payout_requested'
		  AND p.status IN ('pending', 'approved')
	`
	var v sql.NullInt64
	row := r.db.QueryRowxContext(ctx, query, userID)
	if err := row.Scan(&v); err != nil {
		return 0, fmt.Errorf("pending payout: %w", err)
	}
	return v.Int64, nil
}

// ListEntries returns hydrated ledger rows for the given filter.
// Counterparty + channel names are LEFT-JOINed to avoid N+1.
func (r *PaymentLedgerRepository) ListEntries(ctx context.Context, f domain.LedgerListFilter) ([]*domain.HydratedLedgerEntry, int, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	where := []string{"pl.user_id = $1"}
	args := []interface{}{f.UserID}
	i := 2

	switch f.Direction {
	case domain.LedgerDirectionSent:
		where = append(where, "pl.amount_sats < 0")
	case domain.LedgerDirectionReceived:
		where = append(where, "pl.amount_sats > 0")
	}

	if f.EntryType != "" && f.EntryType.IsValid() {
		where = append(where, fmt.Sprintf("pl.entry_type = $%d", i))
		args = append(args, f.EntryType)
		i++
	}
	if f.StartDate != nil {
		where = append(where, fmt.Sprintf("pl.created_at >= $%d", i))
		args = append(args, *f.StartDate)
		i++
	}
	if f.EndDate != nil {
		where = append(where, fmt.Sprintf("pl.created_at <= $%d", i))
		args = append(args, *f.EndDate)
		i++
	}

	whereSQL := strings.Join(where, " AND ")

	// Count
	var total int
	if err := r.db.GetContext(ctx, &total,
		"SELECT COUNT(*) FROM payment_ledger pl WHERE "+whereSQL, args...); err != nil {
		return nil, 0, fmt.Errorf("ledger count: %w", err)
	}

	// Page
	query := fmt.Sprintf(`
		SELECT
			pl.id, pl.user_id, pl.counterparty_user_id, pl.channel_id, pl.entry_type,
			pl.amount_sats, pl.currency, pl.invoice_id, pl.payout_id, pl.metadata,
			pl.idempotency_key, pl.created_at,
			COALESCE(cu.username, '') AS counterparty_name,
			COALESCE(ch.display_name, '') AS channel_name
		FROM payment_ledger pl
		LEFT JOIN users cu ON cu.id = pl.counterparty_user_id
		LEFT JOIN channels ch ON ch.id = pl.channel_id
		WHERE %s
		ORDER BY pl.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereSQL, i, i+1)
	args = append(args, f.Limit, f.Offset)

	var rows []*domain.HydratedLedgerEntry
	if err := r.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("ledger list: %w", err)
	}
	return rows, total, nil
}

func nullTimeOrNil(t interface{}) interface{} {
	// created_at is TIMESTAMPTZ NOT NULL DEFAULT NOW() — if caller passed a zero
	// time, let the DB default fire by returning nil.
	switch v := t.(type) {
	case interface{ IsZero() bool }:
		if v.IsZero() {
			return nil
		}
		return t
	default:
		return t
	}
}

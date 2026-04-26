package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// UserLowBalanceStateRepository fronts the user_low_balance_state table
// (migration 099). Tracks when each user's balance first entered the
// (0, MIN_PAYOUT_SATS) window — used by the balance worker to emit
// low_balance_stuck only after the balance has been continuously low for
// 7+ days.
type UserLowBalanceStateRepository struct {
	db *sqlx.DB
}

// NewUserLowBalanceStateRepository constructs the repo.
func NewUserLowBalanceStateRepository(db *sqlx.DB) *UserLowBalanceStateRepository {
	return &UserLowBalanceStateRepository{db: db}
}

// LowBalanceState mirrors a user_low_balance_state row.
type LowBalanceState struct {
	UserID          string    `db:"user_id"`
	Since           time.Time `db:"since"`
	LastBalanceSats int64     `db:"last_balance_sats"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// MarkLow records that a user's balance is in the low-balance window.
// Inserts on first entry (preserving Since); updates last_balance_sats on
// subsequent ticks within the window without resetting Since.
func (r *UserLowBalanceStateRepository) MarkLow(
	ctx context.Context,
	userID string,
	balanceSats int64,
) error {
	if userID == "" {
		return errors.New("userID required")
	}
	const q = `
		INSERT INTO user_low_balance_state (user_id, since, last_balance_sats, updated_at)
		VALUES ($1, NOW(), $2, NOW())
		ON CONFLICT (user_id) DO UPDATE
			SET last_balance_sats = EXCLUDED.last_balance_sats,
			    updated_at = NOW();
	`
	if _, err := r.db.ExecContext(ctx, q, userID, balanceSats); err != nil {
		return fmt.Errorf("marking low balance: %w", err)
	}
	return nil
}

// ClearLow deletes a user's low-balance state — called when the balance
// crosses out of the (0, MIN_PAYOUT_SATS) window (either to zero or
// above MIN). Idempotent (DELETE of a non-existent row is fine).
func (r *UserLowBalanceStateRepository) ClearLow(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("userID required")
	}
	const q = `DELETE FROM user_low_balance_state WHERE user_id = $1`
	if _, err := r.db.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("clearing low balance state: %w", err)
	}
	return nil
}

// Get returns the current low-balance state for a user, or nil if none.
func (r *UserLowBalanceStateRepository) Get(ctx context.Context, userID string) (*LowBalanceState, error) {
	if userID == "" {
		return nil, errors.New("userID required")
	}
	var s LowBalanceState
	const q = `SELECT user_id, since, last_balance_sats, updated_at FROM user_low_balance_state WHERE user_id = $1`
	if err := r.db.GetContext(ctx, &s, q, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting low balance state: %w", err)
	}
	return &s, nil
}

// ListStuckSince returns all users whose low-balance window started before
// `cutoff` — i.e., users whose balance has been continuously low for at
// least the threshold duration the worker enforces (default 7 days).
func (r *UserLowBalanceStateRepository) ListStuckSince(ctx context.Context, cutoff time.Time) ([]LowBalanceState, error) {
	rows, err := r.db.QueryxContext(ctx, `
		SELECT user_id, since, last_balance_sats, updated_at
		FROM user_low_balance_state
		WHERE since < $1
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("listing stuck users: %w", err)
	}
	defer rows.Close()
	var out []LowBalanceState
	for rows.Next() {
		var s LowBalanceState
		if err := rows.StructScan(&s); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

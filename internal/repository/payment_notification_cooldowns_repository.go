package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// PaymentNotificationCooldownsRepository fronts the
// payment_notification_cooldowns table (migration 098). Records the
// last emission timestamp per (user_id, notification_type) and exposes
// `TryEmit` for the balance worker — atomic-once-per-24h semantics.
type PaymentNotificationCooldownsRepository struct {
	db *sqlx.DB
}

// NewPaymentNotificationCooldownsRepository constructs the repo.
func NewPaymentNotificationCooldownsRepository(db *sqlx.DB) *PaymentNotificationCooldownsRepository {
	return &PaymentNotificationCooldownsRepository{db: db}
}

// TryEmit attempts to record an emission for (userID, notificationType).
// Returns true when this caller is the first to emit (or when the previous
// emission was older than the cooldown window). Returns false when the
// caller should suppress emission to avoid duplicates.
//
// The atomicity is enforced by Postgres via the WHERE clause on the
// ON CONFLICT DO UPDATE — the row only updates when the new emission is
// outside the cooldown window relative to the existing one. We detect
// "actually wrote" by capturing the row's xmax: 0 means a fresh INSERT;
// any non-zero indicates an UPDATE happened (i.e. cooldown elapsed).
func (r *PaymentNotificationCooldownsRepository) TryEmit(
	ctx context.Context,
	userID, notificationType string,
	cooldownHours int,
) (bool, error) {
	if userID == "" || notificationType == "" {
		return false, errors.New("userID and notificationType required")
	}
	if cooldownHours <= 0 {
		cooldownHours = 24
	}
	const q = `
		INSERT INTO payment_notification_cooldowns (user_id, notification_type, emitted_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, notification_type) DO UPDATE
			SET emitted_at = EXCLUDED.emitted_at
			WHERE payment_notification_cooldowns.emitted_at < NOW() - ($3::int * INTERVAL '1 hour')
		RETURNING (xmax = 0) AS inserted, emitted_at;
	`
	var row struct {
		Inserted   bool         `db:"inserted"`
		EmittedAt  sql.NullTime `db:"emitted_at"`
	}
	if err := r.db.QueryRowxContext(ctx, q, userID, notificationType, cooldownHours).StructScan(&row); err != nil {
		// No rows returned = ON CONFLICT WHERE clause prevented update
		// (still inside cooldown). That is the suppress-emission signal.
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("recording notification cooldown: %w", err)
	}
	return row.Inserted, nil
}

// LastEmittedAt returns the most recent emission timestamp for a
// (user, type) pair, or sql.NullTime{Valid: false} if none.
func (r *PaymentNotificationCooldownsRepository) LastEmittedAt(
	ctx context.Context,
	userID, notificationType string,
) (sql.NullTime, error) {
	var t sql.NullTime
	const q = `SELECT emitted_at FROM payment_notification_cooldowns WHERE user_id = $1 AND notification_type = $2`
	err := r.db.QueryRowContext(ctx, q, userID, notificationType).Scan(&t)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sql.NullTime{}, fmt.Errorf("fetching cooldown row: %w", err)
	}
	return t, nil
}

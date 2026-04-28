package inner_circle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrMembershipNotFound indicates no membership row matched the lookup.
var ErrMembershipNotFound = errors.New("inner_circle: membership not found")

// MembershipRepository persists and queries inner_circle_memberships.
type MembershipRepository struct {
	db *sqlx.DB
}

// NewMembershipRepository wraps the given DB.
func NewMembershipRepository(db *sqlx.DB) *MembershipRepository {
	return &MembershipRepository{db: db}
}

// GetActiveTier returns the tier_id of the caller's active membership for the
// given channel, or empty string if none. Used by the streaming middleware and
// by the post-list tier gate.
func (r *MembershipRepository) GetActiveTier(ctx context.Context, userID, channelID uuid.UUID) (string, error) {
	const query = `
		SELECT tier_id FROM inner_circle_memberships
		WHERE user_id = $1 AND channel_id = $2
		  AND status = 'active' AND expires_at > NOW()
		ORDER BY CASE tier_id
			WHEN 'elite' THEN 3
			WHEN 'vip' THEN 2
			WHEN 'supporter' THEN 1
			ELSE 0
		END DESC
		LIMIT 1
	`
	var tier string
	if err := r.db.QueryRowxContext(ctx, query, userID, channelID).Scan(&tier); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("inner_circle: get active tier: %w", err)
	}
	return tier, nil
}

// ListMine returns all of the caller's memberships across all channels. When
// includePending is false, pending rows are filtered out.
func (r *MembershipRepository) ListMine(ctx context.Context, userID uuid.UUID, includePending bool) ([]domain.InnerCircleMembership, error) {
	query := `
		SELECT id, user_id, channel_id, tier_id, status, started_at, expires_at,
		       polar_subscription_id, btcpay_invoice_id, created_at, updated_at
		FROM inner_circle_memberships
		WHERE user_id = $1
		  AND status IN ('active'`
	if includePending {
		query += `,'pending'`
	}
	query += `)
		ORDER BY created_at DESC`

	rows, err := r.db.QueryxContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("inner_circle: list mine: %w", err)
	}
	defer rows.Close()

	out := make([]domain.InnerCircleMembership, 0, 4)
	for rows.Next() {
		var m domain.InnerCircleMembership
		if err := rows.StructScan(&m); err != nil {
			return nil, fmt.Errorf("inner_circle: scan membership: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListByChannel returns paginated active members for a channel. Used by the
// creator dashboard.
func (r *MembershipRepository) ListByChannel(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]domain.InnerCircleMembership, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	const query = `
		SELECT id, user_id, channel_id, tier_id, status, started_at, expires_at,
		       polar_subscription_id, btcpay_invoice_id, created_at, updated_at
		FROM inner_circle_memberships
		WHERE channel_id = $1 AND status = 'active' AND expires_at > NOW()
		ORDER BY started_at DESC NULLS LAST, created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryxContext(ctx, query, channelID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("inner_circle: list by channel: %w", err)
	}
	defer rows.Close()

	out := make([]domain.InnerCircleMembership, 0, limit)
	for rows.Next() {
		var m domain.InnerCircleMembership
		if err := rows.StructScan(&m); err != nil {
			return nil, fmt.Errorf("inner_circle: scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreatePending writes a pending membership row. Returns the new ID. Idempotent
// on (user_id, channel_id) when an active or pending row already exists for
// that pair (returns the existing row's ID; status untouched).
func (r *MembershipRepository) CreatePending(ctx context.Context, userID, channelID uuid.UUID, tierID string, polarSubscriptionID *string, btcpayInvoiceID *uuid.UUID, expiresAt time.Time) (uuid.UUID, error) {
	const query = `
		INSERT INTO inner_circle_memberships
			(user_id, channel_id, tier_id, status, expires_at, polar_subscription_id, btcpay_invoice_id)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6)
		ON CONFLICT (user_id, channel_id) WHERE status IN ('active','pending')
		DO UPDATE SET updated_at = NOW()
		RETURNING id
	`
	var id uuid.UUID
	if err := r.db.QueryRowxContext(ctx, query, userID, channelID, tierID, expiresAt, polarSubscriptionID, btcpayInvoiceID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("inner_circle: create pending: %w", err)
	}
	return id, nil
}

// UpsertActiveByPolar UPSERTs a Polar-driven membership keyed on
// polar_subscription_id. Used by the Polar webhook handler so that
// subscription.created and subscription.updated events for the same
// subscription don't create duplicate rows.
func (r *MembershipRepository) UpsertActiveByPolar(ctx context.Context, userID, channelID uuid.UUID, tierID, polarSubscriptionID string, expiresAt time.Time) (uuid.UUID, error) {
	const query = `
		INSERT INTO inner_circle_memberships
			(user_id, channel_id, tier_id, status, started_at, expires_at, polar_subscription_id)
		VALUES ($1, $2, $3, 'active', NOW(), $4, $5)
		ON CONFLICT (polar_subscription_id) WHERE polar_subscription_id IS NOT NULL
		DO UPDATE SET
			status     = 'active',
			tier_id    = EXCLUDED.tier_id,
			expires_at = EXCLUDED.expires_at,
			started_at = COALESCE(inner_circle_memberships.started_at, NOW()),
			updated_at = NOW()
		RETURNING id
	`
	var id uuid.UUID
	if err := r.db.QueryRowxContext(ctx, query, userID, channelID, tierID, expiresAt, polarSubscriptionID).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("inner_circle: upsert polar membership: %w", err)
	}
	return id, nil
}

// UpsertActiveByBTCPay creates or extends a BTCPay-rail membership. expires_at
// stacks on renewal (max of current and now, plus 30 days). Idempotent on
// btcpay_invoice_id via the optimistic existence check + UPDATE path.
func (r *MembershipRepository) UpsertActiveByBTCPay(ctx context.Context, userID, channelID uuid.UUID, tierID string, btcpayInvoiceID uuid.UUID) (uuid.UUID, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inner_circle: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const selectExisting = `
		SELECT id, expires_at FROM inner_circle_memberships
		WHERE user_id = $1 AND channel_id = $2 AND status IN ('active','pending')
		FOR UPDATE
	`
	var existingID uuid.UUID
	var existingExpires time.Time
	row := tx.QueryRowxContext(ctx, selectExisting, userID, channelID)
	scanErr := row.Scan(&existingID, &existingExpires)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("inner_circle: lookup existing: %w", scanErr)
	}

	now := time.Now().UTC()
	base := now
	if scanErr == nil && existingExpires.After(now) {
		base = existingExpires
	}
	newExpires := base.Add(30 * 24 * time.Hour)

	if errors.Is(scanErr, sql.ErrNoRows) {
		const insert = `
			INSERT INTO inner_circle_memberships
				(user_id, channel_id, tier_id, status, started_at, expires_at, btcpay_invoice_id)
			VALUES ($1, $2, $3, 'active', NOW(), $4, $5)
			RETURNING id
		`
		var id uuid.UUID
		if err := tx.QueryRowxContext(ctx, insert, userID, channelID, tierID, newExpires, btcpayInvoiceID).Scan(&id); err != nil {
			return uuid.Nil, fmt.Errorf("inner_circle: insert btcpay membership: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return uuid.Nil, err
		}
		return id, nil
	}

	const update = `
		UPDATE inner_circle_memberships
		SET status            = 'active',
		    tier_id           = $2,
		    expires_at        = $3,
		    started_at        = COALESCE(started_at, NOW()),
		    btcpay_invoice_id = $4,
		    updated_at        = NOW()
		WHERE id = $1
	`
	if _, err := tx.ExecContext(ctx, update, existingID, tierID, newExpires, btcpayInvoiceID); err != nil {
		return uuid.Nil, fmt.Errorf("inner_circle: update btcpay membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return existingID, nil
}

// Cancel marks a membership as cancelled. Caller-side ownership check happens
// in the service layer.
func (r *MembershipRepository) Cancel(ctx context.Context, id uuid.UUID, callerUserID uuid.UUID) error {
	const query = `
		UPDATE inner_circle_memberships
		SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND status IN ('active','pending')
	`
	res, err := r.db.ExecContext(ctx, query, id, callerUserID)
	if err != nil {
		return fmt.Errorf("inner_circle: cancel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMembershipNotFound
	}
	return nil
}

// SetPolarStatus is called by the Polar webhook receiver on
// subscription.canceled events. Status flips to cancelled but the row keeps
// the polar_subscription_id for audit.
func (r *MembershipRepository) SetPolarStatus(ctx context.Context, polarSubscriptionID, status string) error {
	const query = `
		UPDATE inner_circle_memberships
		SET status = $2, updated_at = NOW()
		WHERE polar_subscription_id = $1
	`
	res, err := r.db.ExecContext(ctx, query, polarSubscriptionID, status)
	if err != nil {
		return fmt.Errorf("inner_circle: set polar status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMembershipNotFound
	}
	return nil
}

// ExpireDue marks active memberships whose expires_at is in the past as
// expired, and pending memberships older than the given window as expired.
// Returns the count expired in each bucket.
func (r *MembershipRepository) ExpireDue(ctx context.Context, pendingTimeout time.Duration) (activeExpired, pendingExpired int, err error) {
	if pendingTimeout <= 0 {
		pendingTimeout = time.Hour
	}
	tx, txErr := r.db.BeginTxx(ctx, nil)
	if txErr != nil {
		return 0, 0, fmt.Errorf("inner_circle: begin: %w", txErr)
	}
	defer func() { _ = tx.Rollback() }()

	res1, err := tx.ExecContext(ctx, `
		UPDATE inner_circle_memberships
		SET status = 'expired', updated_at = NOW()
		WHERE status = 'active' AND expires_at < NOW()
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("inner_circle: expire active: %w", err)
	}
	n1, _ := res1.RowsAffected()

	res2, err := tx.ExecContext(ctx, `
		UPDATE inner_circle_memberships
		SET status = 'expired', updated_at = NOW()
		WHERE status = 'pending' AND created_at < NOW() - $1::interval
	`, fmt.Sprintf("%d seconds", int(pendingTimeout.Seconds())))
	if err != nil {
		return 0, 0, fmt.Errorf("inner_circle: expire pending: %w", err)
	}
	n2, _ := res2.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return int(n1), int(n2), nil
}

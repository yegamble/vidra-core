package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

// PayoutRepository persists rows in btcpay_payouts. Per F05, every state
// transition is a CONDITIONAL UPDATE — second concurrent attempt returns 0
// rows, which the service surfaces as 409 Conflict.
type PayoutRepository struct {
	db *sqlx.DB
}

func NewPayoutRepository(db *sqlx.DB) *PayoutRepository {
	return &PayoutRepository{db: db}
}

// Create inserts a new payout row in 'pending'. Returns the populated payout (with id).
func (r *PayoutRepository) Create(ctx context.Context, tx sqlx.ExtContext, p *domain.Payout) error {
	if tx == nil {
		tx = r.db
	}
	const q = `
		INSERT INTO btcpay_payouts (
			requester_user_id, amount_sats, destination, destination_type, status, auto_trigger
		) VALUES ($1, $2, $3, $4, 'pending', $5)
		RETURNING id, requested_at, created_at, updated_at
	`
	row := tx.QueryRowxContext(ctx, q,
		p.RequesterUserID, p.AmountSats, p.Destination, p.DestinationType, p.AutoTrigger)
	if err := row.Scan(&p.ID, &p.RequestedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("create payout: %w", err)
	}
	p.Status = domain.PayoutStatusPending
	return nil
}

// GetByID fetches a payout by its UUID.
func (r *PayoutRepository) GetByID(ctx context.Context, id string) (*domain.Payout, error) {
	var p domain.Payout
	err := r.db.GetContext(ctx, &p, "SELECT * FROM btcpay_payouts WHERE id = $1", id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPayoutNotFound
		}
		return nil, fmt.Errorf("get payout: %w", err)
	}
	return &p, nil
}

// ListMine returns paginated payouts for a single user, newest first.
func (r *PayoutRepository) ListMine(ctx context.Context, userID string, limit, offset int) ([]*domain.Payout, int, error) {
	if limit <= 0 {
		limit = 20
	}
	var total int
	if err := r.db.GetContext(ctx, &total,
		"SELECT COUNT(*) FROM btcpay_payouts WHERE requester_user_id = $1", userID); err != nil {
		return nil, 0, fmt.Errorf("count payouts: %w", err)
	}
	var rows []*domain.Payout
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM btcpay_payouts WHERE requester_user_id = $1 ORDER BY requested_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list payouts: %w", err)
	}
	return rows, total, nil
}

// ListPending returns all pending payouts (admin queue), newest-first by request time.
func (r *PayoutRepository) ListPending(ctx context.Context, limit, offset int) ([]*domain.Payout, int, error) {
	if limit <= 0 {
		limit = 50
	}
	var total int
	if err := r.db.GetContext(ctx, &total,
		"SELECT COUNT(*) FROM btcpay_payouts WHERE status = 'pending'"); err != nil {
		return nil, 0, fmt.Errorf("count pending payouts: %w", err)
	}
	var rows []*domain.Payout
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM btcpay_payouts WHERE status = 'pending' ORDER BY requested_at ASC LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list pending: %w", err)
	}
	return rows, total, nil
}

// TransitionPending performs a conditional UPDATE: status='pending' → newStatus.
// Returns ErrPayoutInvalidStatus if 0 rows affected (concurrent transition).
func (r *PayoutRepository) TransitionPending(ctx context.Context, id string, newStatus domain.PayoutStatus, adminID string, reason string) error {
	if !validPendingTransition(newStatus) {
		return domain.ErrPayoutInvalidStatus
	}
	var q string
	var args []interface{}
	switch newStatus {
	case domain.PayoutStatusApproved:
		q = `UPDATE btcpay_payouts
		     SET status = 'approved', approved_at = NOW(), approved_by_admin_id = $2, updated_at = NOW()
		     WHERE id = $1 AND status = 'pending'`
		args = []interface{}{id, adminID}
	case domain.PayoutStatusRejected:
		q = `UPDATE btcpay_payouts
		     SET status = 'rejected', rejection_reason = $2, updated_at = NOW()
		     WHERE id = $1 AND status = 'pending'`
		args = []interface{}{id, reason}
	case domain.PayoutStatusCancelled:
		q = `UPDATE btcpay_payouts
		     SET status = 'cancelled', updated_at = NOW()
		     WHERE id = $1 AND status = 'pending'`
		args = []interface{}{id}
	}
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("transition payout: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return domain.ErrPayoutInvalidStatus
	}
	return nil
}

// TransitionApproved: approved -> completed (with txid) OR approved -> rejected.
func (r *PayoutRepository) TransitionApproved(ctx context.Context, id string, newStatus domain.PayoutStatus, txid, reason string) error {
	if newStatus != domain.PayoutStatusCompleted && newStatus != domain.PayoutStatusRejected {
		return domain.ErrPayoutInvalidStatus
	}
	var q string
	var args []interface{}
	if newStatus == domain.PayoutStatusCompleted {
		q = `UPDATE btcpay_payouts
		     SET status = 'completed', executed_at = NOW(), txid = $2, updated_at = NOW()
		     WHERE id = $1 AND status = 'approved'`
		args = []interface{}{id, txid}
	} else {
		q = `UPDATE btcpay_payouts
		     SET status = 'rejected', rejection_reason = $2, updated_at = NOW()
		     WHERE id = $1 AND status = 'approved'`
		args = []interface{}{id, reason}
	}
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("transition approved: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return domain.ErrPayoutInvalidStatus
	}
	return nil
}

func validPendingTransition(s domain.PayoutStatus) bool {
	switch s {
	case domain.PayoutStatusApproved, domain.PayoutStatusRejected, domain.PayoutStatusCancelled:
		return true
	}
	return false
}

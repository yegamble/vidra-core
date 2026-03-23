package repository

import (
	"context"
	"database/sql"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// AbuseMessageRepository handles DB operations for abuse report discussion threads.
type AbuseMessageRepository struct {
	db *sqlx.DB
}

// NewAbuseMessageRepository creates a new AbuseMessageRepository.
func NewAbuseMessageRepository(db *sqlx.DB) *AbuseMessageRepository {
	return &AbuseMessageRepository{db: db}
}

// GetAbuseReportOwner returns the reporter_id for the given abuse report.
func (r *AbuseMessageRepository) GetAbuseReportOwner(ctx context.Context, reportID uuid.UUID) (string, error) {
	var ownerID string
	err := r.db.GetContext(ctx, &ownerID, `SELECT reporter_id FROM abuse_reports WHERE id = $1`, reportID)
	if err == sql.ErrNoRows {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("getting abuse report owner: %w", err)
	}
	return ownerID, nil
}

// ListAbuseMessages returns all messages for a given abuse report.
func (r *AbuseMessageRepository) ListAbuseMessages(ctx context.Context, reportID uuid.UUID) ([]*domain.AbuseMessage, error) {
	var msgs []*domain.AbuseMessage
	err := r.db.SelectContext(ctx, &msgs,
		`SELECT id, abuse_report_id, sender_id, message, created_at
		 FROM abuse_report_messages
		 WHERE abuse_report_id = $1
		 ORDER BY created_at ASC`, reportID)
	if err != nil {
		return nil, fmt.Errorf("listing abuse messages: %w", err)
	}
	return msgs, nil
}

// CreateAbuseMessage inserts a new message and populates generated fields.
func (r *AbuseMessageRepository) CreateAbuseMessage(ctx context.Context, msg *domain.AbuseMessage) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO abuse_report_messages (abuse_report_id, sender_id, message)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at`,
		msg.AbuseReportID, msg.SenderID, msg.Message,
	).Scan(&msg.ID, &msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("creating abuse message: %w", err)
	}
	return nil
}

// DeleteAbuseMessage removes a message by ID (scoped to the report for safety).
func (r *AbuseMessageRepository) DeleteAbuseMessage(ctx context.Context, reportID, msgID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM abuse_report_messages WHERE id = $1 AND abuse_report_id = $2`,
		msgID, reportID)
	if err != nil {
		return fmt.Errorf("deleting abuse message: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

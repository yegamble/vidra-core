package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
)

// TwoFABackupCodeRepository implements usecase.TwoFABackupCodeRepository
type TwoFABackupCodeRepository struct {
	db *sqlx.DB
}

// NewTwoFABackupCodeRepository creates a new backup code repository
func NewTwoFABackupCodeRepository(db *sqlx.DB) *TwoFABackupCodeRepository {
	return &TwoFABackupCodeRepository{db: db}
}

// Create creates a new backup code
func (r *TwoFABackupCodeRepository) Create(ctx context.Context, code *domain.TwoFABackupCode) error {
	// Generate ID if not provided
	if code.ID == "" {
		code.ID = uuid.NewString()
	}

	// Set created_at if not provided
	if code.CreatedAt.IsZero() {
		code.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO twofa_backup_codes (id, user_id, code_hash, used_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	var usedAt interface{}
	if code.UsedAt.Valid {
		usedAt = code.UsedAt.Time
	}

	_, err := r.db.ExecContext(ctx, query,
		code.ID,
		code.UserID,
		code.CodeHash,
		usedAt,
		code.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create backup code: %w", err)
	}

	return nil
}

// GetUnusedForUser retrieves all unused backup codes for a user
func (r *TwoFABackupCodeRepository) GetUnusedForUser(ctx context.Context, userID string) ([]*domain.TwoFABackupCode, error) {
	query := `
		SELECT id, user_id, code_hash, used_at, created_at
		FROM twofa_backup_codes
		WHERE user_id = $1 AND used_at IS NULL
		ORDER BY created_at ASC
	`

	var codes []*domain.TwoFABackupCode
	err := r.db.SelectContext(ctx, &codes, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return []*domain.TwoFABackupCode{}, nil
		}
		return nil, fmt.Errorf("failed to get unused backup codes: %w", err)
	}

	return codes, nil
}

// MarkAsUsed marks a backup code as used
func (r *TwoFABackupCodeRepository) MarkAsUsed(ctx context.Context, codeID string) error {
	query := `
		UPDATE twofa_backup_codes
		SET used_at = $1
		WHERE id = $2 AND used_at IS NULL
	`

	result, err := r.db.ExecContext(ctx, query, time.Now(), codeID)
	if err != nil {
		return fmt.Errorf("failed to mark backup code as used: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrTwoFABackupCodeUsed
	}

	return nil
}

// DeleteAllForUser deletes all backup codes for a user
func (r *TwoFABackupCodeRepository) DeleteAllForUser(ctx context.Context, userID string) error {
	query := `DELETE FROM twofa_backup_codes WHERE user_id = $1`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete backup codes: %w", err)
	}

	return nil
}

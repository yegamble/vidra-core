package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ChannelCollaboratorRepository struct {
	db *sqlx.DB
}

func NewChannelCollaboratorRepository(db *sqlx.DB) *ChannelCollaboratorRepository {
	return &ChannelCollaboratorRepository{db: db}
}

func (r *ChannelCollaboratorRepository) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]*domain.ChannelCollaborator, error) {
	rows, err := r.db.QueryxContext(ctx, `
		SELECT
			c.id, c.channel_id, c.user_id, c.invited_by, c.role, c.status,
			c.responded_at, c.created_at, c.updated_at,
			u.id AS account_id, u.username, u.email, u.display_name, u.bio,
			u.bitcoin_wallet, u.role AS account_role, u.is_active,
			u.email_verified, u.email_verified_at, u.twofa_enabled, u.twofa_secret,
			u.twofa_confirmed_at, u.created_at AS account_created_at, u.updated_at AS account_updated_at
		FROM channel_collaborators c
		JOIN users u ON u.id = c.user_id
		WHERE c.channel_id = $1
		ORDER BY c.created_at ASC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list collaborators: %w", err)
	}
	defer func() { _ = rows.Close() }()

	collaborators := []*domain.ChannelCollaborator{}
	for rows.Next() {
		collaborator, err := scanChannelCollaborator(rows)
		if err != nil {
			return nil, err
		}
		collaborators = append(collaborators, collaborator)
	}

	return collaborators, rows.Err()
}

func (r *ChannelCollaboratorRepository) GetByChannelAndID(ctx context.Context, channelID, collaboratorID uuid.UUID) (*domain.ChannelCollaborator, error) {
	rows, err := r.db.QueryxContext(ctx, `
		SELECT
			c.id, c.channel_id, c.user_id, c.invited_by, c.role, c.status,
			c.responded_at, c.created_at, c.updated_at,
			u.id AS account_id, u.username, u.email, u.display_name, u.bio,
			u.bitcoin_wallet, u.role AS account_role, u.is_active,
			u.email_verified, u.email_verified_at, u.twofa_enabled, u.twofa_secret,
			u.twofa_confirmed_at, u.created_at AS account_created_at, u.updated_at AS account_updated_at
		FROM channel_collaborators c
		JOIN users u ON u.id = c.user_id
		WHERE c.channel_id = $1 AND c.id = $2`, channelID, collaboratorID)
	if err != nil {
		return nil, fmt.Errorf("get collaborator: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrNotFound
	}

	return scanChannelCollaborator(rows)
}

func (r *ChannelCollaboratorRepository) GetByChannelAndUser(ctx context.Context, channelID, userID uuid.UUID) (*domain.ChannelCollaborator, error) {
	rows, err := r.db.QueryxContext(ctx, `
		SELECT
			c.id, c.channel_id, c.user_id, c.invited_by, c.role, c.status,
			c.responded_at, c.created_at, c.updated_at,
			u.id AS account_id, u.username, u.email, u.display_name, u.bio,
			u.bitcoin_wallet, u.role AS account_role, u.is_active,
			u.email_verified, u.email_verified_at, u.twofa_enabled, u.twofa_secret,
			u.twofa_confirmed_at, u.created_at AS account_created_at, u.updated_at AS account_updated_at
		FROM channel_collaborators c
		JOIN users u ON u.id = c.user_id
		WHERE c.channel_id = $1 AND c.user_id = $2`, channelID, userID)
	if err != nil {
		return nil, fmt.Errorf("get collaborator by user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrNotFound
	}

	return scanChannelCollaborator(rows)
}

func (r *ChannelCollaboratorRepository) UpsertInvite(ctx context.Context, collaborator *domain.ChannelCollaborator) error {
	if collaborator.ID == uuid.Nil {
		collaborator.ID = uuid.New()
	}

	now := time.Now().UTC()
	query := `
		INSERT INTO channel_collaborators (
			id, channel_id, user_id, invited_by, role, status, responded_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULL, $7, $7
		)
		ON CONFLICT (channel_id, user_id)
		DO UPDATE SET
			invited_by = EXCLUDED.invited_by,
			role = EXCLUDED.role,
			status = EXCLUDED.status,
			responded_at = NULL,
			updated_at = EXCLUDED.updated_at
		RETURNING id, created_at, updated_at, responded_at`

	return r.db.QueryRowContext(
		ctx,
		query,
		collaborator.ID,
		collaborator.ChannelID,
		collaborator.UserID,
		collaborator.InvitedBy,
		collaborator.Role,
		collaborator.Status,
		now,
	).Scan(&collaborator.ID, &collaborator.CreatedAt, &collaborator.UpdatedAt, &collaborator.RespondedAt)
}

func (r *ChannelCollaboratorRepository) UpdateStatus(ctx context.Context, collaboratorID uuid.UUID, status domain.ChannelCollaboratorStatus) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE channel_collaborators
		SET status = $2, responded_at = NOW(), updated_at = NOW()
		WHERE id = $1`, collaboratorID, status)
	if err != nil {
		return fmt.Errorf("update collaborator status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update collaborator status rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func (r *ChannelCollaboratorRepository) Delete(ctx context.Context, collaboratorID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM channel_collaborators WHERE id = $1`, collaboratorID)
	if err != nil {
		return fmt.Errorf("delete collaborator: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete collaborator rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func scanChannelCollaborator(rows *sqlx.Rows) (*domain.ChannelCollaborator, error) {
	var collaborator domain.ChannelCollaborator
	var accountID string
	var user domain.User
	var twoFASecret sql.NullString

	err := rows.Scan(
		&collaborator.ID,
		&collaborator.ChannelID,
		&collaborator.UserID,
		&collaborator.InvitedBy,
		&collaborator.Role,
		&collaborator.Status,
		&collaborator.RespondedAt,
		&collaborator.CreatedAt,
		&collaborator.UpdatedAt,
		&accountID,
		&user.Username,
		&user.Email,
		&user.DisplayName,
		&user.Bio,
		&user.BitcoinWallet,
		&user.Role,
		&user.IsActive,
		&user.EmailVerified,
		&user.EmailVerifiedAt,
		&user.TwoFAEnabled,
		&twoFASecret,
		&user.TwoFAConfirmedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan collaborator: %w", err)
	}

	user.ID = accountID
	if twoFASecret.Valid {
		user.TwoFASecret = twoFASecret.String
	}
	collaborator.User = &user
	return &collaborator, nil
}

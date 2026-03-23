package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
)

// RegistrationRepository provides data access for user registration requests.
type RegistrationRepository struct {
	db *sqlx.DB
}

// NewRegistrationRepository creates a new RegistrationRepository.
func NewRegistrationRepository(db *sqlx.DB) *RegistrationRepository {
	return &RegistrationRepository{db: db}
}

// ListPending returns all pending registration requests.
func (r *RegistrationRepository) ListPending(ctx context.Context) ([]*domain.UserRegistration, error) {
	var rows []*domain.UserRegistration
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, username, email, COALESCE(channel_name,'') AS channel_name,
		        COALESCE(reason,'') AS reason, status,
		        COALESCE(moderator_response,'') AS moderator_response, created_at
		   FROM user_registrations
		  WHERE status = 'pending'
		  ORDER BY created_at ASC`)
	return rows, err
}

// GetByID returns a registration by its UUID.
func (r *RegistrationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.UserRegistration, error) {
	var row domain.UserRegistration
	err := r.db.GetContext(ctx, &row,
		`SELECT id, username, email, COALESCE(channel_name,'') AS channel_name,
		        COALESCE(reason,'') AS reason, status,
		        COALESCE(moderator_response,'') AS moderator_response, created_at
		   FROM user_registrations WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &row, err
}

// UpdateStatus sets the status and optional moderator response for a registration.
func (r *RegistrationRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status, response string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE user_registrations
		    SET status = $1, moderator_response = $2, updated_at = NOW()
		  WHERE id = $3`, status, response, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete permanently removes a registration by its UUID.
func (r *RegistrationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM user_registrations WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

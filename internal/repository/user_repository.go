package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"gotube/internal/model"
)

// UserRepository defines the interface for user persistence
type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	GetByEmail(ctx context.Context, email string) (*model.User, error)
	GetByID(ctx context.Context, id int64) (*model.User, error)
	SetVerified(ctx context.Context, id int64, verified bool) error
	UpdateWallet(ctx context.Context, id int64, wallet string) error
}

// MySQLUserRepository is a MySQL implementation of UserRepository
type MySQLUserRepository struct {
	db *sqlx.DB
}

// NewMySQLUserRepository creates a new instance
func NewMySQLUserRepository(db *sqlx.DB) *MySQLUserRepository {
	return &MySQLUserRepository{db: db}
}

// Create inserts a new user into the database
func (r *MySQLUserRepository) Create(ctx context.Context, user *model.User) error {
	query := `INSERT INTO users (email, password_hash, verified, iota_wallet, created_at, updated_at)
              VALUES (:email, :password_hash, :verified, :iota_wallet, :created_at, :updated_at)`
	result, err := r.db.NamedExecContext(ctx, query, user)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("fetching user id: %w", err)
	}
	user.ID = id
	return nil
}

// GetByEmail retrieves a user by their email
func (r *MySQLUserRepository) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := r.db.GetContext(ctx, &u, `SELECT * FROM users WHERE email = ?`, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}

// GetByID retrieves a user by their ID
func (r *MySQLUserRepository) GetByID(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	err := r.db.GetContext(ctx, &u, `SELECT * FROM users WHERE id = ?`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

// SetVerified updates a user's verified flag
func (r *MySQLUserRepository) SetVerified(ctx context.Context, id int64, verified bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE users SET verified = ?, updated_at = NOW() WHERE id = ?`, verified, id)
	if err != nil {
		return fmt.Errorf("set verified: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateWallet sets or updates a user's IOTA wallet address
func (r *MySQLUserRepository) UpdateWallet(ctx context.Context, id int64, wallet string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE users SET iota_wallet = ?, updated_at = NOW() WHERE id = ?`, wallet, id)
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

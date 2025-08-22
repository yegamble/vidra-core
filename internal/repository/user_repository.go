package repository

import (
	"context"
	"database/sql"
	"fmt"

	"athena/internal/domain"
	"athena/internal/usecase"
	"github.com/jmoiron/sqlx"
)

type userRepository struct {
	db *sqlx.DB
}

func NewUserRepository(db *sqlx.DB) usecase.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	// Insert user record (avatar stored in user_avatars separately)
	query := `
        INSERT INTO users (id, username, email, display_name, bio, bitcoin_wallet, role, password_hash, is_active, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.DisplayName, user.Bio, user.BitcoinWallet,
		user.Role, passwordHash, user.IsActive, user.CreatedAt, user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

func (r *userRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	query := `
        SELECT u.id, u.username, u.email, u.display_name,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.created_at, u.updated_at
        FROM users u
        LEFT JOIN user_avatars a ON a.user_id = u.id
        WHERE u.id = $1`

	var user domain.User
	err := r.db.GetContext(ctx, &user, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by ID: %w", err)
	}

	return &user, nil
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	query := `
        SELECT u.id, u.username, u.email, u.display_name,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.created_at, u.updated_at
        FROM users u
        LEFT JOIN user_avatars a ON a.user_id = u.id
        WHERE u.email = $1`

	var user domain.User
	err := r.db.GetContext(ctx, &user, query, email)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}

	return &user, nil
}

func (r *userRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `
        SELECT u.id, u.username, u.email, u.display_name,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.created_at, u.updated_at
        FROM users u
        LEFT JOIN user_avatars a ON a.user_id = u.id
        WHERE u.username = $1`

	var user domain.User
	err := r.db.GetContext(ctx, &user, query, username)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by username: %w", err)
	}

	return &user, nil
}

func (r *userRepository) Update(ctx context.Context, user *domain.User) error {
	// Update base user fields (avatar handled in user_avatars separately)
	query := `
        UPDATE users 
        SET username = $2, email = $3, display_name = $4, bio = $5, 
            bitcoin_wallet = $6, role = $7, is_active = $8, updated_at = $9
        WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.DisplayName, user.Bio, user.BitcoinWallet,
		user.Role, user.IsActive, user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrUserNotFound
	}

	return nil
}

func (r *userRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrUserNotFound
	}

	return nil
}

func (r *userRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	query := `SELECT password_hash FROM users WHERE id = $1`

	var passwordHash string
	err := r.db.GetContext(ctx, &passwordHash, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", domain.ErrUserNotFound
		}
		return "", fmt.Errorf("failed to get password hash: %w", err)
	}

	return passwordHash, nil
}

func (r *userRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	query := `UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, userID, passwordHash)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrUserNotFound
	}

	return nil
}

func (r *userRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	query := `
        SELECT u.id, u.username, u.email, u.display_name,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.created_at, u.updated_at
        FROM users u
        LEFT JOIN user_avatars a ON a.user_id = u.id
        ORDER BY u.created_at DESC
        LIMIT $1 OFFSET $2`

	var users []*domain.User
	err := r.db.SelectContext(ctx, &users, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	return users, nil
}

func (r *userRepository) Count(ctx context.Context) (int64, error) {
	query := `SELECT COUNT(*) FROM users`

	var count int64
	err := r.db.GetContext(ctx, &count, query)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}

	return count, nil
}

// SetAvatarFields upserts the user's avatar file_id and ipfs_cid in user_avatars
func (r *userRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	// Ensure user exists to return a meaningful error
	var exists bool
	if err := r.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID); err != nil {
		return fmt.Errorf("failed to check user existence: %w", err)
	}
	if !exists {
		return domain.ErrUserNotFound
	}

	// Upsert avatar fields
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO user_avatars (user_id, ipfs_cid, webp_ipfs_cid, created_at, updated_at)
        VALUES ($1, $2, $3, NOW(), NOW())
        ON CONFLICT (user_id) DO UPDATE SET
            ipfs_cid = EXCLUDED.ipfs_cid,
            webp_ipfs_cid = EXCLUDED.webp_ipfs_cid,
            updated_at = NOW()
    `, userID, ipfsCID, webpCID)
	if err != nil {
		return fmt.Errorf("failed to upsert user avatar fields: %w", err)
	}
	return nil
}

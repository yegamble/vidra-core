package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

	// If channels table exists in this schema, ensure a default channel for the user.
	// This guards tests that use a legacy schema without channels.
	var channelsExist bool
	const q = `SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'channels'
    )`
	if err := r.db.QueryRowContext(ctx, q).Scan(&channelsExist); err == nil && channelsExist {
		_, _ = r.db.ExecContext(ctx, `
            INSERT INTO channels (account_id, handle, display_name, description)
            SELECT $1::uuid, $2, COALESCE(NULLIF($3, ''), $2), $4
            WHERE NOT EXISTS (SELECT 1 FROM channels WHERE account_id = $1::uuid)
        `, user.ID, user.Username, user.DisplayName, user.Bio)
	}

	return nil
}

const selectUserWithAvatar = `
        SELECT u.id, u.username, u.email, u.display_name,
               a.id            AS avatar_id,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.email_verified, u.email_verified_at, u.subscriber_count,
               u.twofa_enabled, u.twofa_secret, u.twofa_confirmed_at,
               u.created_at, u.updated_at
        FROM users u
        LEFT JOIN user_avatars a ON a.user_id = u.id`

type userRow struct {
	ID                string          `db:"id"`
	Username          string          `db:"username"`
	Email             string          `db:"email"`
	DisplayName       string          `db:"display_name"`
	AvatarID          sql.NullString  `db:"avatar_id"`
	AvatarIPFSCID     sql.NullString  `db:"avatar_ipfs_cid"`
	AvatarWebPIPFSCID sql.NullString  `db:"avatar_webp_ipfs_cid"`
	Bio               string          `db:"bio"`
	BitcoinWallet     string          `db:"bitcoin_wallet"`
	Role              domain.UserRole `db:"role"`
	IsActive          bool            `db:"is_active"`
	EmailVerified     bool            `db:"email_verified"`
	EmailVerifiedAt   sql.NullTime    `db:"email_verified_at"`
	SubscriberCount   int64           `db:"subscriber_count"`
	TwoFAEnabled      bool            `db:"twofa_enabled"`
	TwoFASecret       string          `db:"twofa_secret"`
	TwoFAConfirmedAt  sql.NullTime    `db:"twofa_confirmed_at"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

func mapUserRow(rrow userRow) *domain.User {
	u := &domain.User{
		ID:               rrow.ID,
		Username:         rrow.Username,
		Email:            rrow.Email,
		DisplayName:      rrow.DisplayName,
		Bio:              rrow.Bio,
		BitcoinWallet:    rrow.BitcoinWallet,
		Role:             rrow.Role,
		IsActive:         rrow.IsActive,
		EmailVerified:    rrow.EmailVerified,
		EmailVerifiedAt:  rrow.EmailVerifiedAt,
		SubscriberCount:  rrow.SubscriberCount,
		TwoFAEnabled:     rrow.TwoFAEnabled,
		TwoFASecret:      rrow.TwoFASecret,
		TwoFAConfirmedAt: rrow.TwoFAConfirmedAt,
		CreatedAt:        rrow.CreatedAt,
		UpdatedAt:        rrow.UpdatedAt,
	}
	if rrow.AvatarID.Valid || rrow.AvatarIPFSCID.Valid || rrow.AvatarWebPIPFSCID.Valid {
		u.Avatar = &domain.Avatar{
			ID:          rrow.AvatarID.String,
			IPFSCID:     rrow.AvatarIPFSCID,
			WebPIPFSCID: rrow.AvatarWebPIPFSCID,
		}
	}
	return u
}

func (r *userRepository) getUserWhere(ctx context.Context, where string, arg any) (*domain.User, error) {
	var rrow userRow
	query := selectUserWithAvatar + " WHERE " + where
	if err := r.db.GetContext(ctx, &rrow, query, arg); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return mapUserRow(rrow), nil
}

func (r *userRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return r.getUserWhere(ctx, "u.id = $1", id)
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.getUserWhere(ctx, "u.email = $1", email)
}

func (r *userRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return r.getUserWhere(ctx, "u.username = $1", username)
}

func (r *userRepository) Update(ctx context.Context, user *domain.User) error {
	// Update base user fields (avatar handled in user_avatars separately)
	query := `
        UPDATE users
        SET username = $2, email = $3, display_name = $4, bio = $5,
            bitcoin_wallet = $6, role = $7, is_active = $8,
            twofa_enabled = $9, twofa_secret = $10, twofa_confirmed_at = $11,
            updated_at = $12
        WHERE id = $1`

	var twoFAConfirmedAt interface{}
	if user.TwoFAConfirmedAt.Valid {
		twoFAConfirmedAt = user.TwoFAConfirmedAt.Time
	}

	result, err := r.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.DisplayName, user.Bio, user.BitcoinWallet,
		user.Role, user.IsActive,
		user.TwoFAEnabled, user.TwoFASecret, twoFAConfirmedAt,
		user.UpdatedAt)

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
	query := selectUserWithAvatar + `
        ORDER BY u.created_at DESC
        LIMIT $1 OFFSET $2`

	var rows []userRow
	if err := r.db.SelectContext(ctx, &rows, query, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	users := make([]*domain.User, 0, len(rows))
	for _, rrow := range rows {
		users = append(users, mapUserRow(rrow))
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

// MarkEmailAsVerified marks a user's email as verified
func (r *userRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	query := `
		UPDATE users 
		SET email_verified = true, email_verified_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to mark email as verified: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return domain.ErrUserNotFound
	}

	return nil
}

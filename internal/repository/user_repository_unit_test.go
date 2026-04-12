package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUserMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newUserRepo(t *testing.T) (*userRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupUserMockDB(t)
	repo := NewUserRepository(db).(*userRepository)
	cleanup := func() { _ = db.Close() }

	return repo, mock, cleanup
}

func makeUserSelectRows(now time.Time, withAvatar bool, withTwoFA bool) *sqlmock.Rows {
	avatarID := interface{}(nil)
	avatarCID := interface{}(nil)
	avatarWebpCID := interface{}(nil)
	if withAvatar {
		avatarID = "avatar-1"
		avatarCID = "bafy-avatar"
		avatarWebpCID = "bafy-avatar-webp"
	}

	twoFASecret := interface{}(nil)
	twoFAConfirmedAt := interface{}(nil)
	if withTwoFA {
		twoFASecret = "twofa-secret"
		twoFAConfirmedAt = now
	}

	return sqlmock.NewRows([]string{
		"id", "username", "email", "display_name",
		"avatar_id", "avatar_ipfs_cid", "avatar_webp_ipfs_cid",
		"bio", "bitcoin_wallet", "role", "is_active", "email_verified", "email_verified_at",
		"twofa_enabled", "twofa_secret", "twofa_confirmed_at",
		"created_at", "updated_at",
	}).AddRow(
		"user-1", "unit-user", "unit@example.com", "Unit User",
		avatarID, avatarCID, avatarWebpCID,
		"bio", "btc-wallet", string(domain.RoleAdmin), true, true, now,
		withTwoFA, twoFASecret, twoFAConfirmedAt,
		now, now,
	)
}

func TestUserRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	baseUser := &domain.User{
		ID:            "user-1",
		Username:      "unit-user",
		Email:         "unit@example.com",
		DisplayName:   "Unit User",
		Bio:           "bio",
		BitcoinWallet: "wallet",
		Role:          domain.RoleUser,
		IsActive:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	t.Run("success without channels table", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO users`).
			WithArgs(
				baseUser.ID, baseUser.Username, baseUser.Email, baseUser.DisplayName, baseUser.Bio, baseUser.BitcoinWallet,
				baseUser.Role, "hash", baseUser.IsActive, baseUser.CreatedAt, baseUser.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = current_schema()
              AND table_name = 'channels'
        )`)).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectCommit()

		err := repo.Create(ctx, baseUser, "hash")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with default channel creation", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO users`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = current_schema()
              AND table_name = 'channels'
        )`)).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(`(?s)INSERT INTO channels`).
			WithArgs(sqlmock.AnyArg(), baseUser.ID, baseUser.Username, baseUser.DisplayName, baseUser.Bio).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		err := repo.Create(ctx, baseUser, "hash")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("channels existence check error still commits", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO users`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = current_schema()
              AND table_name = 'channels'
        )`)).
			WillReturnError(errors.New("metadata unavailable"))
		mock.ExpectCommit()

		err := repo.Create(ctx, baseUser, "hash")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin transaction failure", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err := repo.Create(ctx, baseUser, "hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user insert failure rolls back", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO users`).WillReturnError(errors.New("duplicate key"))
		mock.ExpectRollback()

		err := repo.Create(ctx, baseUser, "hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create user")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("default channel insert failure rolls back", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO users`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = current_schema()
              AND table_name = 'channels'
        )`)).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(`(?s)INSERT INTO channels`).WillReturnError(errors.New("channel insert failed"))
		mock.ExpectRollback()

		err := repo.Create(ctx, baseUser, "hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create default channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_Getters(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("get by id success maps avatar and twofa fields", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT u\.id, u\.username, u\.email, u\.display_name.*WHERE u\.id = \$1`).
			WithArgs("user-1").
			WillReturnRows(makeUserSelectRows(now, true, true))

		user, err := repo.GetByID(ctx, "user-1")
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "user-1", user.ID)
		assert.Equal(t, "unit-user", user.Username)
		require.NotNil(t, user.Avatar)
		assert.Equal(t, "avatar-1", user.Avatar.ID)
		assert.Equal(t, "twofa-secret", user.TwoFASecret)
		assert.True(t, user.TwoFAConfirmedAt.Valid)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by email not found", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT u\.id, u\.username, u\.email, u\.display_name.*WHERE LOWER\(u\.email\) = LOWER\(\$1\)`).
			WithArgs("missing@example.com").
			WillReturnError(sql.ErrNoRows)

		user, err := repo.GetByEmail(ctx, "missing@example.com")
		require.Nil(t, user)
		require.ErrorIs(t, err, domain.ErrUserNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by username db error", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT u\.id, u\.username, u\.email, u\.display_name.*WHERE u\.username = \$1`).
			WithArgs("broken").
			WillReturnError(errors.New("db read failed"))

		user, err := repo.GetByUsername(ctx, "broken")
		require.Nil(t, user)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_Update(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		user := &domain.User{
			ID:               "user-1",
			Username:         "unit-user",
			Email:            "unit@example.com",
			DisplayName:      "Unit User",
			Bio:              "bio",
			BitcoinWallet:    "wallet",
			Role:             domain.RoleAdmin,
			IsActive:         true,
			TwoFAEnabled:     true,
			TwoFASecret:      "secret",
			TwoFAConfirmedAt: sql.NullTime{Time: now, Valid: true},
			UpdatedAt:        now,
		}

		mock.ExpectExec(`(?s)UPDATE users`).
			WithArgs(
				user.ID, user.Username, user.Email, user.DisplayName, user.Bio, user.BitcoinWallet,
				user.Role, user.IsActive, user.TwoFAEnabled, user.TwoFASecret, sqlmock.AnyArg(), user.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, user)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		user := &domain.User{ID: "user-1"}
		mock.ExpectExec(`(?s)UPDATE users`).WillReturnError(errors.New("update failed"))

		err := repo.Update(ctx, user)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update user")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		user := &domain.User{ID: "user-1"}
		mock.ExpectExec(`(?s)UPDATE users`).WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Update(ctx, user)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		user := &domain.User{ID: "missing"}
		mock.ExpectExec(`(?s)UPDATE users`).WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, user)
		require.ErrorIs(t, err, domain.ErrUserNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM users WHERE id = $1`)).
			WithArgs("user-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, "user-1")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM users WHERE id = $1`)).
			WithArgs("user-1").
			WillReturnError(errors.New("delete failed"))

		err := repo.Delete(ctx, "user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete user")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM users WHERE id = $1`)).
			WithArgs("user-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Delete(ctx, "user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM users WHERE id = $1`)).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, "missing")
		require.ErrorIs(t, err, domain.ErrUserNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_PasswordMethods(t *testing.T) {
	ctx := context.Background()

	t.Run("get password hash success", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT password_hash FROM users WHERE id = $1`)).
			WithArgs("user-1").
			WillReturnRows(sqlmock.NewRows([]string{"password_hash"}).AddRow("hash"))

		hash, err := repo.GetPasswordHash(ctx, "user-1")
		require.NoError(t, err)
		assert.Equal(t, "hash", hash)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get password hash not found", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT password_hash FROM users WHERE id = $1`)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		hash, err := repo.GetPasswordHash(ctx, "missing")
		require.Empty(t, hash)
		require.ErrorIs(t, err, domain.ErrUserNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update password branches", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`)).
			WithArgs("user-1", "new-hash").
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UpdatePassword(ctx, "user-1", "new-hash"))

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`)).
			WithArgs("user-1", "new-hash").
			WillReturnError(errors.New("update failed"))
		err := repo.UpdatePassword(ctx, "user-1", "new-hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update password")

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`)).
			WithArgs("user-1", "new-hash").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))
		err = repo.UpdatePassword(ctx, "user-1", "new-hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`)).
			WithArgs("missing", "new-hash").
			WillReturnResult(sqlmock.NewResult(0, 0))
		err = repo.UpdatePassword(ctx, "missing", "new-hash")
		require.ErrorIs(t, err, domain.ErrUserNotFound)

		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_ListAndCount(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("list success maps rows", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "username", "email", "display_name",
			"avatar_id", "avatar_ipfs_cid", "avatar_webp_ipfs_cid",
			"bio", "bitcoin_wallet", "role", "is_active", "email_verified", "email_verified_at",
			"twofa_enabled", "twofa_secret", "twofa_confirmed_at",
			"created_at", "updated_at",
		}).
			AddRow("user-1", "u1", "u1@example.com", "User One", "avatar-1", "cid-1", "webp-1", "bio1", "wallet1", string(domain.RoleUser), true, false, nil, false, nil, nil, now, now).
			AddRow("user-2", "u2", "u2@example.com", "User Two", nil, nil, nil, "bio2", "wallet2", string(domain.RoleAdmin), true, true, now, true, "secret", now, now, now)

		mock.ExpectQuery(`(?s)SELECT u\.id, u\.username, u\.email, u\.display_name.*ORDER BY u\.created_at DESC.*LIMIT \$1 OFFSET \$2`).
			WithArgs(2, 0).
			WillReturnRows(rows)

		users, err := repo.List(ctx, 2, 0)
		require.NoError(t, err)
		require.Len(t, users, 2)
		assert.NotNil(t, users[0].Avatar)
		assert.Nil(t, users[1].Avatar)
		assert.Equal(t, "secret", users[1].TwoFASecret)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list query error", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT u\.id, u\.username, u\.email, u\.display_name.*ORDER BY u\.created_at DESC.*LIMIT \$1 OFFSET \$2`).
			WithArgs(10, 0).
			WillReturnError(errors.New("list failed"))

		users, err := repo.List(ctx, 10, 0)
		require.Nil(t, users)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list users")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count success and error", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM users`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(9)))
		count, err := repo.Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(9), count)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM users`)).
			WillReturnError(errors.New("count failed"))
		count, err = repo.Count(ctx)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "failed to count users")

		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserRepository_Unit_AvatarAndEmailVerification(t *testing.T) {
	ctx := context.Background()
	ipfsCID := sql.NullString{String: "cid-1", Valid: true}
	webpCID := sql.NullString{String: "cid-webp-1", Valid: true}

	t.Run("set avatar fields branches", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`)).
			WithArgs("user-1").
			WillReturnError(errors.New("exists check failed"))
		err := repo.SetAvatarFields(ctx, "user-1", ipfsCID, webpCID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check user existence")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`)).
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		err = repo.SetAvatarFields(ctx, "missing", ipfsCID, webpCID)
		require.ErrorIs(t, err, domain.ErrUserNotFound)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`)).
			WithArgs("user-1").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(`(?s)INSERT INTO user_avatars`).
			WithArgs("user-1", ipfsCID, webpCID).
			WillReturnError(errors.New("upsert failed"))
		err = repo.SetAvatarFields(ctx, "user-1", ipfsCID, webpCID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to upsert user avatar fields")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`)).
			WithArgs("user-1").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(`(?s)INSERT INTO user_avatars`).
			WithArgs("user-1", ipfsCID, webpCID).
			WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, repo.SetAvatarFields(ctx, "user-1", ipfsCID, webpCID))

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("mark email verified branches", func(t *testing.T) {
		repo, mock, cleanup := newUserRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE users.*SET email_verified = true, email_verified_at = NOW\(\), updated_at = NOW\(\).*WHERE id = \$1`).
			WithArgs("user-1").
			WillReturnError(errors.New("update failed"))
		err := repo.MarkEmailAsVerified(ctx, "user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark email as verified")

		mock.ExpectExec(`(?s)UPDATE users.*SET email_verified = true, email_verified_at = NOW\(\), updated_at = NOW\(\).*WHERE id = \$1`).
			WithArgs("user-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))
		err = repo.MarkEmailAsVerified(ctx, "user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows failed")

		mock.ExpectExec(`(?s)UPDATE users.*SET email_verified = true, email_verified_at = NOW\(\), updated_at = NOW\(\).*WHERE id = \$1`).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))
		err = repo.MarkEmailAsVerified(ctx, "missing")
		require.ErrorIs(t, err, domain.ErrUserNotFound)

		mock.ExpectExec(`(?s)UPDATE users.*SET email_verified = true, email_verified_at = NOW\(\), updated_at = NOW\(\).*WHERE id = \$1`).
			WithArgs("user-1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.MarkEmailAsVerified(ctx, "user-1"))

		require.NoError(t, mock.ExpectationsWereMet())
	})
}

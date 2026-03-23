package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newChannelRepo(t *testing.T) (*ChannelRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupChannelMockDB(t)
	repo := NewChannelRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func makeChannelScanRows(channel domain.Channel) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "account_id", "handle", "display_name", "description", "support",
		"is_local", "atproto_did", "atproto_pds_url",
		"avatar_filename", "avatar_ipfs_cid", "banner_filename", "banner_ipfs_cid",
		"followers_count", "following_count", "videos_count",
		"created_at", "updated_at",
	}).AddRow(
		channel.ID.String(), channel.AccountID.String(), channel.Handle, channel.DisplayName, channel.Description, channel.Support,
		channel.IsLocal, channel.AtprotoDID, channel.AtprotoPDSURL,
		channel.AvatarFilename, channel.AvatarIPFSCID, channel.BannerFilename, channel.BannerIPFSCID,
		channel.FollowersCount, channel.FollowingCount, channel.VideosCount,
		channel.CreatedAt, channel.UpdatedAt,
	)
}

func makeUserScanRows(userID uuid.UUID, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "username", "email", "display_name", "bio", "created_at", "updated_at",
	}).AddRow(
		userID.String(), "owner", "owner@example.com", "Owner", "owner bio", now, now,
	)
}

func sampleChannel() domain.Channel {
	now := time.Now()
	accountID := uuid.New()
	desc := "channel description"
	support := "support text"
	return domain.Channel{
		ID:             uuid.New(),
		AccountID:      accountID,
		Handle:         "unit-channel",
		DisplayName:    "Unit Channel",
		Description:    &desc,
		Support:        &support,
		IsLocal:        true,
		FollowersCount: 1,
		FollowingCount: 2,
		VideosCount:    3,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestChannelRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		accountID := uuid.New()
		desc := "desc"
		support := "support"
		channel := &domain.Channel{
			AccountID:   accountID,
			Handle:      "unit-channel",
			DisplayName: "Unit Channel",
			Description: &desc,
			Support:     &support,
		}

		now := time.Now()
		mock.ExpectQuery(`(?s)INSERT INTO channels`).
			WithArgs(
				sqlmock.AnyArg(),
				accountID,
				channel.Handle,
				channel.DisplayName,
				channel.Description,
				channel.Support,
				true,
			).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))

		err := repo.Create(ctx, channel)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, channel.ID)
		assert.True(t, channel.IsLocal)
		assert.False(t, channel.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate handle maps to domain error", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		channel := &domain.Channel{AccountID: uuid.New(), Handle: "duplicate", DisplayName: "Dup"}
		mock.ExpectQuery(`(?s)INSERT INTO channels`).
			WillReturnError(errors.New("violates unique constraint on handle"))

		err := repo.Create(ctx, channel)
		require.ErrorIs(t, err, domain.ErrDuplicateEntry)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("generic create failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		channel := &domain.Channel{AccountID: uuid.New(), Handle: "x", DisplayName: "X"}
		mock.ExpectQuery(`(?s)INSERT INTO channels`).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, channel)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_GetByID(t *testing.T) {
	ctx := context.Background()
	ch := sampleChannel()

	t.Run("success with account loaded", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.id = \$1`).
			WithArgs(ch.ID).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeUserScanRows(ch.AccountID, ch.CreatedAt))

		got, err := repo.GetByID(ctx, ch.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ch.ID, got.ID)
		require.NotNil(t, got.Account)
		assert.Equal(t, "owner", got.Account.Username)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.id = \$1`).
			WithArgs(ch.ID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByID(ctx, ch.ID)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("load account failure bubbles up", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.id = \$1`).
			WithArgs(ch.ID).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnError(errors.New("user lookup failed"))

		got, err := repo.GetByID(ctx, ch.ID)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load channel account")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_GetByHandle(t *testing.T) {
	ctx := context.Background()
	ch := sampleChannel()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.handle = \$1`).
			WithArgs(ch.Handle).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeUserScanRows(ch.AccountID, ch.CreatedAt))

		got, err := repo.GetByHandle(ctx, ch.Handle)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ch.Handle, got.Handle)
		require.NotNil(t, got.Account)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.handle = \$1`).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByHandle(ctx, "missing")
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.handle = \$1`).
			WithArgs("broken").
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetByHandle(ctx, "broken")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get channel by handle")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_List(t *testing.T) {
	ctx := context.Background()
	ch1 := sampleChannel()
	ch2 := sampleChannel()
	ch2.ID = uuid.New()
	ch2.AccountID = uuid.New()
	ch2.Handle = "unit-channel-2"

	t.Run("success with defaults and bulk account fetch", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		params := domain.ChannelListParams{Page: 0, PageSize: 0} // triggers defaults

		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM channels c WHERE`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		rows := makeChannelScanRows(ch1)
		rows.AddRow(
			ch2.ID.String(), ch2.AccountID.String(), ch2.Handle, ch2.DisplayName, ch2.Description, ch2.Support,
			ch2.IsLocal, ch2.AtprotoDID, ch2.AtprotoPDSURL,
			ch2.AvatarFilename, ch2.AvatarIPFSCID, ch2.BannerFilename, ch2.BannerIPFSCID,
			ch2.FollowersCount, ch2.FollowingCount, ch2.VideosCount,
			ch2.CreatedAt, ch2.UpdatedAt,
		)
		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*ORDER BY c\.created_at DESC`).
			WillReturnRows(rows)

		// Single bulk query for all accounts (NOT N+1)
		accountRows := makeUserScanRows(ch1.AccountID, ch1.CreatedAt)
		// ch2's account is not returned (simulates deleted user) -> ch2.Account stays nil
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = ANY`).
			WillReturnRows(accountRows)

		resp, err := repo.List(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 2, resp.Total)
		assert.Equal(t, 1, resp.Page)
		assert.Equal(t, 20, resp.PageSize)
		require.Len(t, resp.Data, 2)
		assert.NotNil(t, resp.Data[0].Account)
		assert.Nil(t, resp.Data[1].Account)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		params := domain.ChannelListParams{
			Search: "abc",
			Sort:   "-videosCount",
		}

		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM channels c WHERE`).
			WillReturnError(errors.New("count failed"))

		resp, err := repo.List(ctx, params)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to count channels")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		accountID := uuid.New()
		isLocal := true
		params := domain.ChannelListParams{
			AccountID: &accountID,
			IsLocal:   &isLocal,
			Search:    "search",
			Sort:      "name",
			Page:      1,
			PageSize:  10,
		}

		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM channels c WHERE`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*ORDER BY c\.display_name ASC`).
			WillReturnError(errors.New("select failed"))

		resp, err := repo.List(ctx, params)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list channels")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_Update(t *testing.T) {
	ctx := context.Background()
	ch := sampleChannel()

	t.Run("invalid input when no mutable fields", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		got, err := repo.Update(ctx, ch.ID, domain.ChannelUpdateRequest{})
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		newName := "Renamed Channel"
		newDesc := "updated description"
		updates := domain.ChannelUpdateRequest{
			DisplayName: &newName,
			Description: &newDesc,
		}

		ch.DisplayName = newName
		ch.Description = &newDesc

		mock.ExpectQuery(`(?s)UPDATE channels.*RETURNING id, account_id, handle, display_name`).
			WithArgs(newName, &newDesc, ch.ID).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeUserScanRows(ch.AccountID, ch.CreatedAt))

		got, err := repo.Update(ctx, ch.ID, updates)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, newName, got.DisplayName)
		require.NotNil(t, got.Account)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		newName := "Name"
		updates := domain.ChannelUpdateRequest{DisplayName: &newName}
		mock.ExpectQuery(`(?s)UPDATE channels.*RETURNING id, account_id, handle, display_name`).
			WithArgs(newName, ch.ID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.Update(ctx, ch.ID, updates)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		newName := "Name"
		updates := domain.ChannelUpdateRequest{DisplayName: &newName}
		mock.ExpectQuery(`(?s)UPDATE channels.*RETURNING id, account_id, handle, display_name`).
			WithArgs(newName, ch.ID).
			WillReturnError(errors.New("update failed"))

		got, err := repo.Update(ctx, ch.ID, updates)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_Delete(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Delete(ctx, channelID))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnError(errors.New("delete failed"))

		err := repo.Delete(ctx, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Delete(ctx, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, channelID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_GetChannelsByAccountID(t *testing.T) {
	ctx := context.Background()
	accountID := uuid.New()
	ch := sampleChannel()
	ch.AccountID = accountID

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.account_id = \$1`).
			WithArgs(accountID).
			WillReturnRows(makeChannelScanRows(ch))

		got, err := repo.GetChannelsByAccountID(ctx, accountID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, ch.ID, got[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.account_id = \$1`).
			WithArgs(accountID).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetChannelsByAccountID(ctx, accountID)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get channels by account")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_GetDefaultChannelForAccount(t *testing.T) {
	ctx := context.Background()
	ch := sampleChannel()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.account_id = \$1.*LIMIT 1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeUserScanRows(ch.AccountID, ch.CreatedAt))

		got, err := repo.GetDefaultChannelForAccount(ctx, ch.AccountID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ch.ID, got.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.account_id = \$1.*LIMIT 1`).
			WithArgs(ch.AccountID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetDefaultChannelForAccount(ctx, ch.AccountID)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("load account error", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT\s+c\.id.*FROM channels c.*WHERE c\.account_id = \$1.*LIMIT 1`).
			WithArgs(ch.AccountID).
			WillReturnRows(makeChannelScanRows(ch))
		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnError(errors.New("user query failed"))

		got, err := repo.GetDefaultChannelForAccount(ctx, ch.AccountID)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load channel account")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_LoadChannelAccount(t *testing.T) {
	ctx := context.Background()
	ch := sampleChannel()

	t.Run("sql no rows is ignored", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnError(sql.ErrNoRows)

		err := repo.loadChannelAccount(ctx, &ch)
		require.NoError(t, err)
		assert.Nil(t, ch.Account)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("other errors are returned", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, username, email, display_name, bio, created_at, updated_at.*FROM users.*WHERE id = \$1`).
			WithArgs(ch.AccountID).
			WillReturnError(errors.New("db failed"))

		err := repo.loadChannelAccount(ctx, &ch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load channel account")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelRepository_Unit_CheckOwnership(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	userID := uuid.New()

	t.Run("success true", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM channels WHERE id = $1 AND account_id = $2)`)).
			WithArgs(channelID, userID).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.CheckOwnership(ctx, channelID, userID)
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newChannelRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM channels WHERE id = $1 AND account_id = $2)`)).
			WithArgs(channelID, userID).
			WillReturnError(errors.New("query failed"))

		ok, err := repo.CheckOwnership(ctx, channelID, userID)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "failed to check channel ownership")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

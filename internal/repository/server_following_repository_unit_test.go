package repository

import (
	"context"
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

func setupServerFollowingMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newServerFollowingRepo(t *testing.T) (port_ServerFollowingRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupServerFollowingMockDB(t)
	repo := NewServerFollowingRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

// port_ServerFollowingRepository is an alias to avoid importing port package inline
type port_ServerFollowingRepository interface {
	ListFollowers(ctx context.Context) ([]*domain.ServerFollowing, error)
	ListFollowing(ctx context.Context) ([]*domain.ServerFollowing, error)
	Follow(ctx context.Context, host string) error
	Unfollow(ctx context.Context, host string) error
	SetFollowerState(ctx context.Context, host string, state domain.ServerFollowingState) error
	DeleteFollower(ctx context.Context, host string) error
}

func serverFollowingColumns() []string {
	return []string{"id", "host", "state", "follower", "created_at"}
}

func TestServerFollowingRepository_ListFollowers(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = true ORDER BY created_at DESC`)).
			WillReturnRows(sqlmock.NewRows(serverFollowingColumns()).
				AddRow("id-1", "peer1.example.com", domain.ServerFollowingStateAccepted, true, now).
				AddRow("id-2", "peer2.example.com", domain.ServerFollowingStatePending, true, now))

		followers, err := repo.ListFollowers(ctx)
		require.NoError(t, err)
		assert.Len(t, followers, 2)
		assert.Equal(t, "peer1.example.com", followers[0].Host)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = true ORDER BY created_at DESC`)).
			WillReturnRows(sqlmock.NewRows(serverFollowingColumns()))

		followers, err := repo.ListFollowers(ctx)
		require.NoError(t, err)
		assert.Empty(t, followers)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = true ORDER BY created_at DESC`)).
			WillReturnError(errors.New("db error"))

		followers, err := repo.ListFollowers(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list followers")
		assert.Nil(t, followers)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerFollowingRepository_ListFollowing(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = false ORDER BY created_at DESC`)).
			WillReturnRows(sqlmock.NewRows(serverFollowingColumns()).
				AddRow("id-3", "remote.example.com", domain.ServerFollowingStatePending, false, now))

		following, err := repo.ListFollowing(ctx)
		require.NoError(t, err)
		assert.Len(t, following, 1)
		assert.Equal(t, "remote.example.com", following[0].Host)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = false ORDER BY created_at DESC`)).
			WillReturnError(errors.New("db error"))

		following, err := repo.ListFollowing(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list following")
		assert.Nil(t, following)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerFollowingRepository_Follow(t *testing.T) {
	ctx := context.Background()
	host := "new.example.com"

	t.Run("success (insert)", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO server_following`)).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Follow(ctx, host)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success (upsert conflict)", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO server_following`)).
			WillReturnResult(sqlmock.NewResult(1, 0)) // conflict, 0 rows changed

		err := repo.Follow(ctx, host)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO server_following`)).
			WillReturnError(errors.New("db error"))

		err := repo.Follow(ctx, host)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "follow instance")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerFollowingRepository_Unfollow(t *testing.T) {
	ctx := context.Background()
	host := "remove.example.com"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM server_following WHERE host = $1 AND follower = false`)).
			WithArgs(host).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Unfollow(ctx, host)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM server_following WHERE host = $1 AND follower = false`)).
			WithArgs(host).
			WillReturnError(errors.New("db error"))

		err := repo.Unfollow(ctx, host)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unfollow instance")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerFollowingRepository_SetFollowerState(t *testing.T) {
	ctx := context.Background()
	host := "follower.example.com"

	t.Run("success accepted", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE server_following SET state = $1 WHERE host = $2 AND follower = true`)).
			WithArgs(domain.ServerFollowingStateAccepted, host).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetFollowerState(ctx, host, domain.ServerFollowingStateAccepted)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success rejected", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE server_following SET state = $1 WHERE host = $2 AND follower = true`)).
			WithArgs(domain.ServerFollowingStateRejected, host).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetFollowerState(ctx, host, domain.ServerFollowingStateRejected)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE server_following SET state = $1 WHERE host = $2 AND follower = true`)).
			WillReturnError(errors.New("db error"))

		err := repo.SetFollowerState(ctx, host, domain.ServerFollowingStateAccepted)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "set follower state")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerFollowingRepository_DeleteFollower(t *testing.T) {
	ctx := context.Background()
	host := "gone.example.com"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM server_following WHERE host = $1 AND follower = true`)).
			WithArgs(host).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteFollower(ctx, host)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newServerFollowingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM server_following WHERE host = $1 AND follower = true`)).
			WithArgs(host).
			WillReturnError(errors.New("db error"))

		err := repo.DeleteFollower(ctx, host)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete follower")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

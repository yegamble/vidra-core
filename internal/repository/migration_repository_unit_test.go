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

func setupMigrationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestMigrationRepository_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO migration_jobs`)).
			WithArgs("admin-1", "source.host", "pending", false, "dbhost", 5432, "dbname", "dbuser", "dbpass", "/media", sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("job-1", now, now))

		dbHost, dbPort, dbName, dbUser, dbPass, mediaPath := "dbhost", 5432, "dbname", "dbuser", "dbpass", "/media"
		job := &domain.MigrationJob{
			AdminUserID:      "admin-1",
			SourceHost:       "source.host",
			Status:           "pending",
			DryRun:           false,
			SourceDBHost:     &dbHost,
			SourceDBPort:     &dbPort,
			SourceDBName:     &dbName,
			SourceDBUser:     &dbUser,
			SourceDBPassword: &dbPass,
			SourceMediaPath:  &mediaPath,
		}
		err := repo.Create(ctx, job)
		require.NoError(t, err)
		assert.Equal(t, "job-1", job.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO migration_jobs`)).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, &domain.MigrationJob{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create migration job")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMigrationRepository_GetByID(t *testing.T) {
	ctx := context.Background()
	columns := []string{
		"id", "admin_user_id", "source_host", "status", "dry_run", "error_message",
		"stats_json", "source_db_host", "source_db_port", "source_db_name",
		"source_db_user", "source_db_password", "source_media_path",
		"created_at", "started_at", "completed_at", "updated_at",
	}

	t.Run("success", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, admin_user_id`)).
			WithArgs("job-1").
			WillReturnRows(sqlmock.NewRows(columns).AddRow(
				"job-1", "admin-1", "host", "completed", false, nil,
				[]byte(`{}`), "dbh", 5432, "dbn", "dbu", "dbp", "/m",
				now, nil, nil, now,
			))

		job, err := repo.GetByID(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, "job-1", job.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, admin_user_id`)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		job, err := repo.GetByID(ctx, "missing")
		require.Error(t, err)
		assert.Nil(t, job)
		assert.ErrorIs(t, err, domain.ErrMigrationNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMigrationRepository_List(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM migration_jobs`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, admin_user_id`)).
			WithArgs(10, 0).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "admin_user_id", "source_host", "status", "dry_run", "error_message",
				"stats_json", "source_db_host", "source_db_port", "source_db_name",
				"source_db_user", "source_db_password", "source_media_path",
				"created_at", "started_at", "completed_at", "updated_at",
			}).AddRow(
				"job-1", "admin-1", "host", "pending", false, nil,
				[]byte(`{}`), "dbh", 5432, "dbn", "dbu", "dbp", "/m",
				time.Now(), nil, nil, time.Now(),
			))

		jobs, total, err := repo.List(ctx, 10, 0)
		require.NoError(t, err)
		assert.Len(t, jobs, 1)
		assert.Equal(t, int64(1), total)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count error", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM migration_jobs`)).
			WillReturnError(errors.New("count error"))

		_, _, err := repo.List(ctx, 10, 0)
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMigrationRepository_Update(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE migration_jobs`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, &domain.MigrationJob{ID: "job-1", Status: "completed"})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE migration_jobs`)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, &domain.MigrationJob{ID: "missing"})
		assert.ErrorIs(t, err, domain.ErrMigrationNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMigrationRepository_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM migration_jobs WHERE id`)).
			WithArgs("job-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, "job-1")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM migration_jobs WHERE id`)).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, "missing")
		assert.ErrorIs(t, err, domain.ErrMigrationNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMigrationRepository_GetRunning(t *testing.T) {
	ctx := context.Background()

	t.Run("found running job", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, admin_user_id`)).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "admin_user_id", "source_host", "status", "dry_run", "error_message",
				"stats_json", "source_db_host", "source_db_port", "source_db_name",
				"source_db_user", "source_db_password", "source_media_path",
				"created_at", "started_at", "completed_at", "updated_at",
			}).AddRow(
				"job-1", "admin-1", "host", "running", false, nil,
				[]byte(`{}`), "dbh", 5432, "dbn", "dbu", "dbp", "/m",
				now, nil, nil, now,
			))

		job, err := repo.GetRunning(ctx)
		require.NoError(t, err)
		require.NotNil(t, job)
		assert.Equal(t, domain.MigrationStatus("running"), job.Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no running job", func(t *testing.T) {
		db, mock := setupMigrationMockDB(t)
		defer db.Close()
		repo := NewMigrationRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, admin_user_id`)).
			WillReturnError(sql.ErrNoRows)

		job, err := repo.GetRunning(ctx)
		require.NoError(t, err)
		assert.Nil(t, job)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

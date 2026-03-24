package database

import (
	"context"
	"math"
	"os"
	"testing"

	migrationfs "vidra-core"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const defaultMigrationTestDBURL = "postgres://vidra:password@localhost:5432/vidra_test?sslmode=disable"

func openMigrationTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping database tests in short mode")
	}

	dbURL := os.Getenv("VIDRA_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = defaultMigrationTestDBURL
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Skipf("Skipping database migration integration test: %v", err)
	}

	return db
}

func TestEmbeddedMigrationsAvailable(t *testing.T) {
	goose.SetBaseFS(migrationfs.FS)
	t.Cleanup(func() {
		goose.SetBaseFS(nil)
	})

	migrations, err := goose.CollectMigrations(embeddedMigrationsDir, 0, math.MaxInt64)
	require.NoError(t, err)
	require.NotEmpty(t, migrations, "embedded migration directory should contain SQL migrations")
}

func TestRunMigrations(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "fresh database - all migrations applied",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMigrationTestDB(t)
			defer db.Close()

			_, err := db.Exec(`
				DROP SCHEMA public CASCADE;
				CREATE SCHEMA public;
				GRANT ALL ON SCHEMA public TO vidra;
				GRANT ALL ON SCHEMA public TO public;
			`)
			require.NoError(t, err)

			err = RunMigrations(context.Background(), db)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			var count int
			err = db.Get(&count, "SELECT COUNT(*) FROM goose_db_version")
			require.NoError(t, err)
			assert.Greater(t, count, 0, "should have migration records")

			err = db.Get(&count, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'")
			require.NoError(t, err)
			assert.Equal(t, 1, count, "users table should exist")
		})
	}
}

func TestCurrentVersion(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	err := RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version, err := CurrentVersion(db)
	require.NoError(t, err)
	assert.Greater(t, version, int64(0), "should have a migration version")
}

func TestRunMigrations_AlreadyMigrated(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	err := RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version1, err := CurrentVersion(db)
	require.NoError(t, err)

	err = RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version2, err := CurrentVersion(db)
	require.NoError(t, err)

	assert.Equal(t, version1, version2, "version should not change on re-run")
}

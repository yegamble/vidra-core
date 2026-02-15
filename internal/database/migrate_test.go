package database

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping database tests in short mode")
	}

	dbURL := "postgres://athena:password@localhost:5432/athena_test?sslmode=disable"

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
			db, err := sqlx.Connect("postgres", dbURL)
			require.NoError(t, err)
			defer db.Close()

			_, err = db.Exec(`
				DROP SCHEMA public CASCADE;
				CREATE SCHEMA public;
				GRANT ALL ON SCHEMA public TO athena;
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
	if testing.Short() {
		t.Skip("Skipping database tests in short mode")
	}

	dbURL := "postgres://athena:password@localhost:5432/athena_test?sslmode=disable"

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version, err := CurrentVersion(db)
	require.NoError(t, err)
	assert.Greater(t, version, int64(0), "should have a migration version")
}

func TestRunMigrations_AlreadyMigrated(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping database tests in short mode")
	}

	dbURL := "postgres://athena:password@localhost:5432/athena_test?sslmode=disable"

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version1, err := CurrentVersion(db)
	require.NoError(t, err)

	err = RunMigrations(context.Background(), db)
	require.NoError(t, err)

	version2, err := CurrentVersion(db)
	require.NoError(t, err)

	assert.Equal(t, version1, version2, "version should not change on re-run")
}

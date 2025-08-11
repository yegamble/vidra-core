package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

const (
	defaultPostgresURL = "postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable"
	defaultRedisURL    = "redis://localhost:6380/0"
)

type TestDB struct {
	DB    *sqlx.DB
	Redis *redis.Client
}

func SetupTestDB(t *testing.T) *TestDB {
	t.Helper()

	db, err := setupPostgres()
	if err != nil {
		t.Fatalf("Failed to setup test database: %v", err)
	}

	redisClient, err := setupRedis()
	if err != nil {
		t.Fatalf("Failed to setup test redis: %v", err)
	}

	testDB := &TestDB{
		DB:    db,
		Redis: redisClient,
	}

	t.Cleanup(func() {
		cleanupTestDB(t, testDB)
	})

	return testDB
}

func setupPostgres() (*sqlx.DB, error) {
	db, err := sqlx.Connect("postgres", defaultPostgresURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to test database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	// Migrations are now handled by Docker init script
	// No need to run them manually

	return db, nil
}

func setupRedis() (*redis.Client, error) {
	opt, err := redis.ParseURL(defaultRedisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return client, nil
}


func cleanupTestDB(t *testing.T, testDB *TestDB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Clean Redis
	if err := testDB.Redis.FlushDB(ctx).Err(); err != nil {
		t.Logf("Failed to flush Redis: %v", err)
	}
	testDB.Redis.Close()

	// Clean Postgres tables
	tables := []string{"sessions", "refresh_tokens", "users"}
	for _, table := range tables {
		if _, err := testDB.DB.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			t.Logf("Failed to truncate table %s: %v", table, err)
		}
	}

	testDB.DB.Close()
}

func (tdb *TestDB) TruncateTables(t *testing.T, tables ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, table := range tables {
		if _, err := tdb.DB.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			t.Fatalf("Failed to truncate table %s: %v", table, err)
		}
	}
}

func (tdb *TestDB) WithTx(t *testing.T, fn func(*sqlx.Tx)) {
	t.Helper()

	tx, err := tdb.DB.Beginx()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Logf("Failed to rollback transaction: %v", err)
		}
	}()

	fn(tx)
}
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
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
	// Try loading env files commonly used in tests
	// Load .env.test first (overrides), then .env if present; ignore errors silently
	_ = godotenv.Load(".env.test")
	_ = godotenv.Load()

	// Prefer an explicit test URL if provided
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		// Assemble from granular TEST_DB_* envs if provided
		host := getEnvDefault("TEST_DB_HOST", "localhost")
		port := getEnvDefault("TEST_DB_PORT", "5433")
		name := getEnvDefault("TEST_DB_NAME", "athena_test")
		user := getEnvDefault("TEST_DB_USER", "test_user")
		pass := getEnvDefault("TEST_DB_PASSWORD", "test_password")
		ssl := getEnvDefault("TEST_DB_SSLMODE", "disable")
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to test database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupRedis() (*redis.Client, error) {
	// Use environment variable if set (for CI), otherwise use local test setup
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6380/0"
	}

	opt, err := redis.ParseURL(redisURL)
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
	if testDB.Redis != nil {
		if err := testDB.Redis.FlushDB(ctx).Err(); err != nil {
			t.Logf("Failed to flush Redis: %v", err)
		}
		err := testDB.Redis.Close()
		if err != nil {
			t.Logf("Failed to close Redis client: %v", err)
		}
	}

	// Clean Postgres tables
	if testDB.DB != nil {
		tables := []string{"sessions", "refresh_tokens", "users"}
		for _, table := range tables {
			if _, err := testDB.DB.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
				t.Logf("Failed to truncate table %s: %v", table, err)
			}
		}
		err := testDB.DB.Close()
		if err != nil {
			t.Logf("Failed to close Postgres DB: %v", err)
		}
	}
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

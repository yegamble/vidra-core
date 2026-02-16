package setup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

func CreateDatabaseIfNotExists(ctx context.Context, databaseURL string) error {
	dbName := extractDatabaseName(databaseURL)
	if dbName == "" {
		return fmt.Errorf("invalid database URL: cannot extract database name")
	}

	postgresURL := replaceDatabaseName(databaseURL, "postgres")
	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres database: %w", err)
	}
	defer db.Close()

	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	if err := db.QueryRowContext(ctx, query, dbName).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !exists {
		quotedDBName := pq.QuoteIdentifier(dbName)
		createQuery := fmt.Sprintf("CREATE DATABASE %s", quotedDBName)
		if _, err := db.ExecContext(ctx, createQuery); err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	}

	targetDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to target database: %w", err)
	}
	defer targetDB.Close()

	extensions := []string{
		"CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"",
		"CREATE EXTENSION IF NOT EXISTS \"pgcrypto\"",
		"CREATE EXTENSION IF NOT EXISTS \"pg_trgm\"",
	}

	for _, ext := range extensions {
		if _, err := targetDB.ExecContext(ctx, ext); err != nil {
			return fmt.Errorf("failed to create extension: %w", err)
		}
	}

	return nil
}

func CreateAdminUser(ctx context.Context, databaseURL, username, email, password string) error {
	if username == "" || email == "" || password == "" {
		return fmt.Errorf("username, email, and password are required")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	var exists bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM users WHERE username = $1 OR email = $2)"
	if err := db.QueryRowContext(ctx, checkQuery, username, email).Scan(&exists); err != nil {
		if !strings.Contains(err.Error(), "does not exist") {
			return fmt.Errorf("failed to check if user exists: %w", err)
		}
	}

	if exists {
		return nil
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	insertQuery := `
		INSERT INTO users (id, username, email, password, role, email_verified, created_at, updated_at)
		VALUES (uuid_generate_v4(), $1, $2, $3, 'admin', true, NOW(), NOW())
	`
	if _, err := db.ExecContext(ctx, insertQuery, username, email, string(hashedPassword)); err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}

	return nil
}

func extractDatabaseName(url string) string {
	// URL format: postgres://user:pass@host:port/dbname
	parts := strings.Split(url, "/")
	if len(parts) < 4 {
		return ""
	}
	dbName := parts[len(parts)-1]
	if idx := strings.Index(dbName, "?"); idx != -1 {
		dbName = dbName[:idx]
	}
	return dbName
}

func replaceDatabaseName(url, newName string) string {
	parts := strings.Split(url, "/")
	if len(parts) < 4 {
		return url
	}
	parts[len(parts)-1] = newName
	return strings.Join(parts, "/")
}

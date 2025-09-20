package testutil

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"athena/internal/domain"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// SetupTestDBWithMigration creates a test database connection
func SetupTestDBWithMigration(t *testing.T) *sqlx.DB {
	// Get test database URL from environment or use default
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// Default to local test database
		dbURL = "postgres://postgres:postgres@localhost:5432/athena_test?sslmode=disable"
	}

	// Connect to database
	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// Set connection pool settings for tests
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	// Clean up on test completion
	t.Cleanup(func() {
		CleanupTestDB(t, db)
		_ = db.Close()
	})

	return db
}

// CleanupTestDB cleans up test data
func CleanupTestDB(t *testing.T, db *sqlx.DB) {
	// Delete test data in reverse order of foreign key dependencies
	tables := []string{
		"email_verification_tokens",
		"notifications",
		"messages",
		"subscriptions",
		"video_views",
		"video_comments",
		"videos",
		"user_avatars",
		"users",
	}

	for _, table := range tables {
		_, err := db.Exec(fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			// Log but don't fail - table might not exist
			t.Logf("Failed to truncate %s: %v", table, err)
		}
	}
}

// RunMigrations runs database migrations for tests
func RunMigrations(t *testing.T, db *sqlx.DB) {
	// Read and execute migration files
	migrationDir := "../../migrations"

	// Check if running from different directory
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		migrationDir = "migrations"
	}

	files, err := os.ReadDir(migrationDir)
	if err != nil {
		t.Fatalf("Failed to read migration directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Read migration file
		content, err := os.ReadFile(fmt.Sprintf("%s/%s", migrationDir, file.Name()))
		if err != nil {
			t.Fatalf("Failed to read migration %s: %v", file.Name(), err)
		}

		// Execute migration
		_, err = db.Exec(string(content))
		if err != nil {
			// Check if it's a "already exists" error which we can ignore
			if !isAlreadyExistsError(err) {
				t.Fatalf("Failed to execute migration %s: %v", file.Name(), err)
			}
		}
	}
}

// isAlreadyExistsError checks if the error is due to object already existing
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "already exists") ||
		contains(errStr, "duplicate key") ||
		contains(errStr, "relation") && contains(errStr, "exists")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		len(s) >= len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Now returns current time for testing
func Now() time.Time {
	return time.Now().UTC()
}

// NullTime creates a sql.NullTime with the given time
func NullTime(t time.Time) sql.NullTime {
	return sql.NullTime{
		Time:  t,
		Valid: true,
	}
}

// NullString creates a sql.NullString with the given string
func NullString(s string) sql.NullString {
	return sql.NullString{
		String: s,
		Valid:  s != "",
	}
}

// CreateTestUser creates a test user in the database
func CreateTestUser(t *testing.T, db *sqlx.DB, email string, role string) *domain.User {
	t.Helper()

	// Import required packages at the top of the file
	user := &domain.User{
		ID:            fmt.Sprintf("user_%d", time.Now().UnixNano()),
		Username:      fmt.Sprintf("user_%d", time.Now().UnixNano()),
		Email:         email,
		DisplayName:   "Test User",
		Bio:           "Test bio",
		Role:          domain.UserRole(role),
		IsActive:      true,
		EmailVerified: true,
	}

	query := `
		INSERT INTO users (id, username, email, password_hash, display_name, bio, role, is_active, email_verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`

	err := db.QueryRow(
		query,
		user.ID,
		user.Username,
		user.Email,
		"$2a$10$abcdefghijklmnopqrstuvwxyz", // dummy hash
		user.DisplayName,
		user.Bio,
		user.Role,
		user.IsActive,
		user.EmailVerified,
	).Scan(&user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}
	return user
}

// CreateTestVideo creates a test video in the database
func CreateTestVideo(t *testing.T, db *sqlx.DB, userID, title string) *domain.Video {
	t.Helper()

	video := &domain.Video{
		ID:          fmt.Sprintf("video_%d", time.Now().UnixNano()),
		UserID:      userID,
		Title:       title,
		Description: "Test video description",
		Privacy:     domain.VideoPrivacyPublic,
		Duration:    120,
		Views:       0,
	}

	query := `
		INSERT INTO videos (id, user_id, title, description, privacy, duration)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`

	err := db.QueryRow(
		query,
		video.ID,
		video.UserID,
		video.Title,
		video.Description,
		video.Privacy,
		video.Duration,
	).Scan(&video.CreatedAt, &video.UpdatedAt)

	if err != nil {
		t.Fatalf("Failed to create test video: %v", err)
	}
	return video
}

package testutil

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// SetupTestDBWithMigration creates a test database connection
func SetupTestDBWithMigration(t *testing.T) *sqlx.DB {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
		return nil
	}

	db, err := setupPostgres()
	if err != nil {
		t.Skipf("Skipping test: Postgres not available (%v)", err)
		return nil
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
		"comments",
		"videos",
		"user_avatars",
		"users",
	}

	for _, table := range tables {
		// Safe to use fmt.Sprintf here as table names come from hardcoded slice above,
		// not user input. Using string formatting for DDL is acceptable in this test-only context.
		_, err := db.Exec(fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			// Log but don't fail - table might not exist
			t.Logf("Failed to truncate %s: %v", table, err)
		}
	}
}

// RunMigrations runs database migrations for tests
func RunMigrations(t *testing.T, db *sqlx.DB) {
	t.Helper()
	if db == nil {
		t.Fatalf("RunMigrations called with nil DB")
	}

	var schema string
	err := db.Get(&schema, `SELECT current_schema()`)
	if err != nil {
		t.Fatalf("Failed to resolve current schema: %v", err)
	}

	if err := applySchemaMigrations(db, schema); err != nil {
		t.Fatalf("Failed to run schema migrations: %v", err)
	}
	if err := applyPostMigrationCompatibility(db); err != nil {
		t.Fatalf("Failed to apply migration compatibility setup: %v", err)
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

	userID := uuid.New().String()
	timestamp := time.Now().UnixNano()

	// Make email unique if it's a common test email
	if email == "admin@test.com" || email == "user@test.com" || email == "mod@test.com" || email == "target@test.com" {
		email = fmt.Sprintf("%s_%d@test.com", email[:len(email)-9], timestamp)
	}

	user := &domain.User{
		ID:            userID,
		Username:      fmt.Sprintf("user_%d", timestamp),
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

// CreateTestChannel creates a test channel in the database
func CreateTestChannel(t *testing.T, db *sqlx.DB, userID string, handle string) uuid.UUID {
	t.Helper()

	channelID := uuid.New()
	timestamp := time.Now().UnixNano()

	// Make handle unique if it's a common test handle
	if handle == "" {
		handle = fmt.Sprintf("channel_%d", timestamp)
	}

	query := `
		INSERT INTO channels (id, account_id, handle, display_name, description, is_local)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (handle) DO UPDATE SET handle = $3 || '_' || $7
		RETURNING id`

	var returnedID string
	err := db.QueryRow(
		query,
		channelID.String(),
		userID,
		handle,
		"Test Channel",
		"Test channel description",
		true,
		timestamp,
	).Scan(&returnedID)

	if err != nil {
		t.Fatalf("Failed to create test channel: %v", err)
	}
	return channelID
}

// CreateTestVideo creates a test video in the database
func CreateTestVideo(t *testing.T, db *sqlx.DB, userID, title string) *domain.Video {
	t.Helper()

	// Create a channel for the video if not exists
	channelID := CreateTestChannel(t, db, userID, "")

	thumbnailID := uuid.New().String()
	video := &domain.Video{
		ID:          uuid.New().String(),
		UserID:      userID,
		ChannelID:   channelID,
		Title:       title,
		Description: "Test video description",
		Privacy:     domain.PrivacyPublic,
		Duration:    120,
		Views:       0,
		ThumbnailID: thumbnailID,
		Status:      "completed",
	}

	query := `
		INSERT INTO videos (id, user_id, channel_id, title, description, privacy, duration, thumbnail_id, status,
		                   tags, language, file_size, mime_type, original_cid, thumbnail_cid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, updated_at, upload_date`

	err := db.QueryRow(
		query,
		video.ID,
		video.UserID,
		video.ChannelID,
		video.Title,
		video.Description,
		video.Privacy,
		video.Duration,
		video.ThumbnailID,
		video.Status,
		pq.Array([]string{}), // tags
		"en",                 // language
		1024*1024,            // file_size (1MB)
		"video/mp4",          // mime_type
		"",                   // original_cid
		"",                   // thumbnail_cid
	).Scan(&video.CreatedAt, &video.UpdatedAt, &video.UploadDate)

	if err != nil {
		t.Fatalf("Failed to create test video: %v", err)
	}
	return video
}

// RedisTestURL returns the Redis URL for testing
func RedisTestURL() string {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	return redisURL
}

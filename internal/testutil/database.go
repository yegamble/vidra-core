package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	redis "github.com/redis/go-redis/v9"
)

type TestDB struct {
	DB    *sqlx.DB
	Redis *redis.Client
}

var (
	dbAvailability bool
	dbCheckOnce    sync.Once
)

func checkDBAvailability() {
	host := getEnvDefault("TEST_DB_HOST", "localhost")
	port := getEnvDefault("TEST_DB_PORT", "5432")
	address := net.JoinHostPort(host, port)

	conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err != nil {
		dbAvailability = false
		return
	}
	conn.Close()
	dbAvailability = true
}

func SetupTestDB(t *testing.T) *TestDB {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
		return nil
	}

	dbCheckOnce.Do(checkDBAvailability)
	if !dbAvailability {
		t.Skip("Skipping test: Database not available (Fail Fast)")
		return nil
	}

	db, err := setupPostgres()
	if err != nil {
		t.Skipf("Skipping test: Postgres not available (%v)", err)
		return nil
	}

	redisClient, err := setupRedis()
	if err != nil {
		t.Skipf("Skipping test: Redis not available (%v)", err)
		return nil
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
	// Try loading env files commonly used in tests from multiple locations
	// Load .env.test first (overrides), then .env if present; ignore errors silently
	_ = godotenv.Load("../../.env.test")
	_ = godotenv.Load(".env.test")
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	// Prefer an explicit test URL if provided
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		// Assemble from granular TEST_DB_* envs if provided
		host := getEnvDefault("TEST_DB_HOST", "localhost")
		// Default port logic: use 5432 for both CI and local since Docker is running on 5432
		defaultPort := "5432"
		port := getEnvDefault("TEST_DB_PORT", defaultPort)
		name := getEnvDefault("TEST_DB_NAME", "athena_test")
		user := getEnvDefault("TEST_DB_USER", "test_user")
		pass := getEnvDefault("TEST_DB_PASSWORD", "test_password")
		ssl := getEnvDefault("TEST_DB_SSLMODE", "disable")
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
	}

	// Derive an isolated schema per calling test package to avoid cross-package interference
	schema := deriveTestSchema()

	// First connect without custom search_path to create the schema if needed
	// Use a short deadline so tests skip fast when DB isn't available locally
	db, err := connectWithRetry(dbURL, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to test database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	// Create schema if needed
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pqQuoteIdent(schema))); err != nil {
		return nil, fmt.Errorf("failed to create test schema: %w", err)
	}
	// Close and reconnect with search_path set to the schema for all pooled conns
	_ = db.Close()

	// Append search_path to the DSN (lib/pq honors search_path param in URL form)
	if strings.Contains(dbURL, "://") {
		u, parseErr := url.Parse(dbURL)
		if parseErr == nil {
			q := u.Query()
			q.Set("search_path", fmt.Sprintf("%s,public", schema))
			u.RawQuery = q.Encode()
			dbURL = u.String()
		}
	} else {
		// Fallback DSN key/value form
		dbURL = dbURL + fmt.Sprintf(" search_path='%s,public'", schema)
	}

	db, err = connectWithRetry(dbURL, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to reconnect to test database with schema: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ensure schema exists for tests (idempotent)
	if err := ensureTestSchema(db); err != nil {
		return nil, err
	}

	return db, nil
}

// deriveTestSchema attempts to create a stable, package-specific schema name
func deriveTestSchema() string {
	if v := os.Getenv("TEST_SCHEMA"); v != "" {
		return sanitizeSchema(v)
	}
	// Walk up the call stack to find first test file outside testutil
	for i := 1; i < 15; i++ {
		if _, file, _, ok := runtime.Caller(i); ok {
			base := filepath.Base(file)
			if strings.HasSuffix(base, "_test.go") && !strings.Contains(file, filepath.Join("internal", "testutil")) {
				// Use directory name as package differentiator
				dir := filepath.Dir(file)
				// e.g., internal/repository or internal/httpapi
				parts := strings.Split(dir, string(filepath.Separator))
				if len(parts) >= 2 {
					pkg := strings.Join(parts[len(parts)-2:], "_")
					return sanitizeSchema("test_" + pkg)
				}
				return sanitizeSchema("test_unknown")
			}
		}
	}
	return sanitizeSchema("test_default")
}

func sanitizeSchema(s string) string {
	s = strings.ToLower(s)
	// Replace any non [a-z0-9_] with underscore
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	// Ensure it starts with a letter
	if b.Len() == 0 || (b.String()[0] < 'a' || b.String()[0] > 'z') {
		return "t_" + b.String()
	}
	return b.String()
}

// pqQuoteIdent quotes an identifier minimally for CREATE SCHEMA
func pqQuoteIdent(id string) string {
	// Very simple quote for safety; our sanitize already removed bad chars
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

// ensureTestSchema creates required tables/extensions for integration tests if missing.
// It is safe to run multiple times.
func ensureTestSchema(db *sqlx.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE EXTENSION IF NOT EXISTS unaccent`,
		`CREATE EXTENSION IF NOT EXISTS btree_gin`,
		`CREATE OR REPLACE FUNCTION update_updated_at_column() RETURNS TRIGGER AS $$
        BEGIN NEW.updated_at = NOW(); RETURN NEW; END; $$ language 'plpgsql';`,
		`CREATE TABLE IF NOT EXISTS users (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            username VARCHAR(50) UNIQUE NOT NULL,
            email VARCHAR(255) UNIQUE NOT NULL,
            display_name VARCHAR(100),
            bio TEXT,
            bitcoin_wallet VARCHAR(62),
            role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin', 'moderator')),
            password_hash TEXT NOT NULL,
            is_active BOOLEAN NOT NULL DEFAULT true,
            subscriber_count BIGINT NOT NULL DEFAULT 0,
            email_verified BOOLEAN NOT NULL DEFAULT false,
            email_verified_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS subscriber_count BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMP WITH TIME ZONE`,
		// Subscriptions table
		`CREATE TABLE IF NOT EXISTS subscriptions (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    subscriber_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    channel_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    UNIQUE(subscriber_id, channel_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions(subscriber_id)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_channel ON subscriptions(channel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		`CREATE INDEX IF NOT EXISTS idx_users_role ON users(role)`,
		`CREATE INDEX IF NOT EXISTS idx_users_is_active ON users(is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_users_bitcoin_wallet ON users(bitcoin_wallet)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email_verified ON users(email_verified)`,
		`DROP TRIGGER IF EXISTS update_users_updated_at ON users`,
		`CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Email verification tokens table
		`CREATE TABLE IF NOT EXISTS email_verification_tokens (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    token VARCHAR(255) NOT NULL UNIQUE,
		    code VARCHAR(6) NOT NULL,
		    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    used_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_user_id ON email_verification_tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_token ON email_verification_tokens(token)`,
		`CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_code_user ON email_verification_tokens(code, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_expires_at ON email_verification_tokens(expires_at)`,
		// User avatars table
		`CREATE TABLE IF NOT EXISTS user_avatars (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
            ipfs_cid TEXT,
            webp_ipfs_cid TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`DROP TRIGGER IF EXISTS update_user_avatars_updated_at ON user_avatars`,
		`CREATE TRIGGER update_user_avatars_updated_at BEFORE UPDATE ON user_avatars FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            token TEXT UNIQUE NOT NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            revoked_at TIMESTAMP WITH TIME ZONE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token ON refresh_tokens(token)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_revoked_at ON refresh_tokens(revoked_at)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_active ON refresh_tokens(user_id, expires_at) WHERE revoked_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS sessions (
            id VARCHAR(255) PRIMARY KEY,
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`DROP INDEX IF EXISTS idx_sessions_active`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(user_id, expires_at)`,
		// Video categories table (must be created before videos table for foreign key)
		`CREATE TABLE IF NOT EXISTS video_categories (
		    id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
		    name VARCHAR(100) NOT NULL UNIQUE,
		    slug VARCHAR(100) NOT NULL UNIQUE,
		    description TEXT,
		    icon VARCHAR(50),
		    color VARCHAR(7),
		    display_order INTEGER DEFAULT 0,
		    is_active BOOLEAN DEFAULT true,
		    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		    created_by UUID REFERENCES users(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_video_categories_slug ON video_categories(slug)`,
		`CREATE INDEX IF NOT EXISTS idx_video_categories_is_active ON video_categories(is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_video_categories_display_order ON video_categories(display_order)`,
		// Insert default categories
		`INSERT INTO video_categories (name, slug, description, icon, color, display_order, is_active) VALUES
		    ('Music', 'music', 'Music videos, concerts, and audio content', '🎵', '#FF0000', 1, true),
		    ('Gaming', 'gaming', 'Gaming videos, walkthroughs, and streams', '🎮', '#00FF00', 2, true),
		    ('Education', 'education', 'Educational content and tutorials', '📚', '#0066CC', 3, true),
		    ('Entertainment', 'entertainment', 'Entertainment and comedy content', '🎭', '#FF9900', 4, true),
		    ('News & Politics', 'news-politics', 'News and political content', '📰', '#666666', 5, true),
		    ('Science & Technology', 'science-technology', 'Science and technology content', '🔬', '#00CCFF', 6, true),
		    ('Sports', 'sports', 'Sports and fitness content', '⚽', '#009900', 7, true),
		    ('Travel & Events', 'travel-events', 'Travel vlogs and event coverage', '✈️', '#FF6600', 8, true),
		    ('Film & Animation', 'film-animation', 'Movies, animations, and visual content', '🎬', '#CC00CC', 9, true),
		    ('People & Blogs', 'people-blogs', 'Personal vlogs and lifestyle content', '👥', '#FF3366', 10, true),
		    ('Pets & Animals', 'pets-animals', 'Animal and pet related content', '🐾', '#996633', 11, true),
		    ('How-to & Style', 'howto-style', 'DIY, tutorials, and fashion content', '💄', '#FF66CC', 12, true),
		    ('Autos & Vehicles', 'autos-vehicles', 'Automotive and vehicle content', '🚗', '#000099', 13, true),
		    ('Nonprofits & Activism', 'nonprofits-activism', 'Charity and social cause content', '🤝', '#339966', 14, true),
		    ('Other', 'other', 'Uncategorized content', '📁', '#999999', 999, true)
		ON CONFLICT (slug) DO NOTHING`,
		// Videos table
		`CREATE TABLE IF NOT EXISTS videos (
	            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	            thumbnail_id UUID NOT NULL,
	            title VARCHAR(255) NOT NULL,
	            description TEXT,
	            duration INTEGER NOT NULL DEFAULT 0,
	            views BIGINT NOT NULL DEFAULT 0,
	            privacy VARCHAR(20) NOT NULL CHECK (privacy IN ('public','unlisted','private')),
	            status VARCHAR(20) NOT NULL CHECK (status IN ('uploading','queued','processing','completed','failed')),
	            upload_date TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	            original_cid TEXT,
	            processed_cids JSONB NOT NULL DEFAULT '{}'::jsonb,
	            thumbnail_cid TEXT,
	            tags TEXT[] NOT NULL DEFAULT '{}',
	            category_id UUID REFERENCES video_categories(id) ON DELETE SET NULL,
	            language VARCHAR(10),
	            file_size BIGINT NOT NULL DEFAULT 0,
	            mime_type VARCHAR(120),
	            metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
	            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
	        )`,
		`ALTER TABLE videos ADD COLUMN IF NOT EXISTS category_id UUID REFERENCES video_categories(id) ON DELETE SET NULL`,
		`ALTER TABLE videos ADD COLUMN IF NOT EXISTS output_paths JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`ALTER TABLE videos ADD COLUMN IF NOT EXISTS thumbnail_path TEXT`,
		`ALTER TABLE videos ADD COLUMN IF NOT EXISTS preview_path TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_videos_user_id ON videos(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_privacy ON videos(privacy)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_upload_date ON videos(upload_date)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_category_id ON videos(category_id)`,
		`DROP TRIGGER IF EXISTS update_videos_updated_at ON videos`,
		`CREATE TRIGGER update_videos_updated_at BEFORE UPDATE ON videos FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Upload sessions table
		`CREATE TABLE IF NOT EXISTS upload_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            filename VARCHAR(255) NOT NULL,
            file_size BIGINT NOT NULL CHECK (file_size > 0),
            chunk_size BIGINT NOT NULL CHECK (chunk_size > 0),
            total_chunks INTEGER NOT NULL CHECK (total_chunks > 0),
            uploaded_chunks INTEGER[] NOT NULL DEFAULT '{}',
            status VARCHAR(20) NOT NULL CHECK (status IN ('active','completed','expired','failed')) DEFAULT 'active',
            temp_file_path TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
        )`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_video_id ON upload_sessions(video_id)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_user_id ON upload_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_status ON upload_sessions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires_at ON upload_sessions(expires_at)`,
		`DROP TRIGGER IF EXISTS update_upload_sessions_updated_at ON upload_sessions`,
		`CREATE TRIGGER update_upload_sessions_updated_at BEFORE UPDATE ON upload_sessions FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Encoding jobs table
		`CREATE TABLE IF NOT EXISTS encoding_jobs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
            source_file_path TEXT NOT NULL,
            source_resolution VARCHAR(10) NOT NULL,
            target_resolutions TEXT[] NOT NULL DEFAULT '{}',
            status VARCHAR(20) NOT NULL CHECK (status IN ('pending','processing','completed','failed')) DEFAULT 'pending',
            progress INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
            error_message TEXT,
            started_at TIMESTAMP WITH TIME ZONE,
            completed_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_encoding_jobs_video_id ON encoding_jobs(video_id)`,
		`CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status ON encoding_jobs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_encoding_jobs_created_at ON encoding_jobs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status_created ON encoding_jobs(status, created_at)`,
		`DROP TRIGGER IF EXISTS update_encoding_jobs_updated_at ON encoding_jobs`,
		`CREATE TRIGGER update_encoding_jobs_updated_at BEFORE UPDATE ON encoding_jobs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Unique active job per video (avoid duplicate concurrent encodes)
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_encoding_jobs_active_video ON encoding_jobs (video_id) WHERE status IN ('pending','processing')`,
		// Messages table for user messaging
		`CREATE TABLE IF NOT EXISTS messages (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    content TEXT NOT NULL,
		    message_type VARCHAR(20) NOT NULL DEFAULT 'text' CHECK (message_type IN ('text','system')),
		    is_read BOOLEAN NOT NULL DEFAULT false,
		    is_deleted_by_sender BOOLEAN NOT NULL DEFAULT false,
		    is_deleted_by_recipient BOOLEAN NOT NULL DEFAULT false,
		    parent_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    read_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_sender_id ON messages(sender_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_recipient_id ON messages(recipient_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(sender_id, recipient_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_is_read ON messages(is_read)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_parent_id ON messages(parent_message_id)`,
		`DROP TRIGGER IF EXISTS update_messages_updated_at ON messages`,
		`CREATE TRIGGER update_messages_updated_at BEFORE UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Conversations table to track threads
		`CREATE TABLE IF NOT EXISTS conversations (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    participant_one_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    participant_two_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    last_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
		    last_message_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    UNIQUE(participant_one_id, participant_two_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_participant_one ON conversations(participant_one_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_participant_two ON conversations(participant_two_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_last_message_at ON conversations(last_message_at)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_participants ON conversations(participant_one_id, participant_two_id)`,
		`DROP TRIGGER IF EXISTS update_conversations_updated_at ON conversations`,
		`CREATE TRIGGER update_conversations_updated_at BEFORE UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Ensure participant ordering consistency
		`CREATE OR REPLACE FUNCTION ensure_conversation_order() RETURNS TRIGGER AS $$
		DECLARE temp_id UUID; BEGIN
		    IF NEW.participant_one_id > NEW.participant_two_id THEN
		        temp_id := NEW.participant_one_id;
		        NEW.participant_one_id := NEW.participant_two_id;
		        NEW.participant_two_id := temp_id;
		    END IF; RETURN NEW; END; $$ language 'plpgsql'`,
		`DROP TRIGGER IF EXISTS ensure_conversation_order_trigger ON conversations`,
		`CREATE TRIGGER ensure_conversation_order_trigger BEFORE INSERT OR UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION ensure_conversation_order()`,
		// User views tracking tables
		`CREATE TABLE IF NOT EXISTS user_views (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
		    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
		    session_id UUID NOT NULL,
		    fingerprint_hash TEXT NOT NULL,
		    watch_duration INTEGER NOT NULL DEFAULT 0,
		    video_duration INTEGER NOT NULL DEFAULT 0,
		    completion_percentage DECIMAL(5,2) NOT NULL DEFAULT 0.0,
		    is_completed BOOLEAN NOT NULL DEFAULT false,
		    seek_count INTEGER NOT NULL DEFAULT 0,
		    pause_count INTEGER NOT NULL DEFAULT 0,
		    replay_count INTEGER NOT NULL DEFAULT 0,
		    quality_changes INTEGER NOT NULL DEFAULT 0,
		    initial_load_time INTEGER,
		    buffer_events INTEGER NOT NULL DEFAULT 0,
		    connection_type VARCHAR(20),
		    video_quality VARCHAR(10),
		    referrer_url TEXT,
		    referrer_type VARCHAR(20),
		    utm_source VARCHAR(50),
		    utm_medium VARCHAR(50),
		    utm_campaign VARCHAR(100),
		    device_type VARCHAR(20),
		    os_name VARCHAR(50),
		    browser_name VARCHAR(50),
		    screen_resolution VARCHAR(20),
		    is_mobile BOOLEAN NOT NULL DEFAULT false,
		    country_code CHAR(2),
		    region_code VARCHAR(10),
		    city_name VARCHAR(100),
		    timezone VARCHAR(50),
		    is_anonymous BOOLEAN NOT NULL DEFAULT false,
		    tracking_consent BOOLEAN NOT NULL DEFAULT true,
		    gdpr_consent BOOLEAN,
		    view_date DATE NOT NULL DEFAULT CURRENT_DATE,
		    view_hour INTEGER NOT NULL DEFAULT EXTRACT(HOUR FROM NOW()),
		    weekday INTEGER NOT NULL DEFAULT EXTRACT(DOW FROM NOW()),
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    CONSTRAINT check_completion_percentage CHECK (completion_percentage >= 0.0 AND completion_percentage <= 100.0),
		    CONSTRAINT check_watch_duration CHECK (watch_duration >= 0),
		    CONSTRAINT check_positive_counts CHECK (
		        seek_count >= 0 AND pause_count >= 0 AND replay_count >= 0 AND
		        quality_changes >= 0 AND buffer_events >= 0
		    )
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_video_id ON user_views(video_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_user_id ON user_views(user_id) WHERE user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_session_fingerprint ON user_views(session_id, fingerprint_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_view_date ON user_views(view_date)`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_created_at ON user_views(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_user_views_analytics ON user_views(video_id, view_date, completion_percentage, watch_duration)`,
		`DROP TRIGGER IF EXISTS update_user_views_updated_at ON user_views`,
		`CREATE TRIGGER update_user_views_updated_at BEFORE UPDATE ON user_views FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Daily video stats aggregation table
		`CREATE TABLE IF NOT EXISTS daily_video_stats (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
		    stat_date DATE NOT NULL,
		    total_views BIGINT NOT NULL DEFAULT 0,
		    unique_views BIGINT NOT NULL DEFAULT 0,
		    authenticated_views BIGINT NOT NULL DEFAULT 0,
		    anonymous_views BIGINT NOT NULL DEFAULT 0,
		    total_watch_time BIGINT NOT NULL DEFAULT 0,
		    avg_watch_duration DECIMAL(10,2) NOT NULL DEFAULT 0.0,
		    avg_completion_percentage DECIMAL(5,2) NOT NULL DEFAULT 0.0,
		    completed_views BIGINT NOT NULL DEFAULT 0,
		    avg_initial_load_time DECIMAL(10,2),
		    total_buffer_events BIGINT NOT NULL DEFAULT 0,
		    avg_seek_count DECIMAL(5,2) NOT NULL DEFAULT 0.0,
		    desktop_views BIGINT NOT NULL DEFAULT 0,
		    mobile_views BIGINT NOT NULL DEFAULT 0,
		    tablet_views BIGINT NOT NULL DEFAULT 0,
		    tv_views BIGINT NOT NULL DEFAULT 0,
		    top_countries JSONB NOT NULL DEFAULT '[]'::jsonb,
		    top_regions JSONB NOT NULL DEFAULT '[]'::jsonb,
		    referrer_breakdown JSONB NOT NULL DEFAULT '{}'::jsonb,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    UNIQUE(video_id, stat_date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_video_stats_video_date ON daily_video_stats(video_id, stat_date)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_video_stats_date ON daily_video_stats(stat_date)`,
		`DROP TRIGGER IF EXISTS update_daily_video_stats_updated_at ON daily_video_stats`,
		`CREATE TRIGGER update_daily_video_stats_updated_at BEFORE UPDATE ON daily_video_stats FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// User engagement stats table
		`CREATE TABLE IF NOT EXISTS user_engagement_stats (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    stat_date DATE NOT NULL,
		    videos_watched BIGINT NOT NULL DEFAULT 0,
		    total_watch_time BIGINT NOT NULL DEFAULT 0,
		    avg_session_duration DECIMAL(10,2) NOT NULL DEFAULT 0.0,
		    unique_videos_watched BIGINT NOT NULL DEFAULT 0,
		    avg_completion_rate DECIMAL(5,2) NOT NULL DEFAULT 0.0,
		    completed_videos BIGINT NOT NULL DEFAULT 0,
		    sessions_count BIGINT NOT NULL DEFAULT 0,
		    total_seeks BIGINT NOT NULL DEFAULT 0,
		    total_pauses BIGINT NOT NULL DEFAULT 0,
		    total_replays BIGINT NOT NULL DEFAULT 0,
		    preferred_device VARCHAR(20),
		    device_diversity INTEGER NOT NULL DEFAULT 0,
		    top_categories JSONB NOT NULL DEFAULT '[]'::jsonb,
		    avg_video_duration_preference DECIMAL(10,2),
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    UNIQUE(user_id, stat_date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_engagement_user_date ON user_engagement_stats(user_id, stat_date)`,
		`DROP TRIGGER IF EXISTS update_user_engagement_stats_updated_at ON user_engagement_stats`,
		`CREATE TRIGGER update_user_engagement_stats_updated_at BEFORE UPDATE ON user_engagement_stats FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		// Trending videos table
		`CREATE TABLE IF NOT EXISTS trending_videos (
		    video_id UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
		    views_last_hour BIGINT NOT NULL DEFAULT 0,
		    views_last_24h BIGINT NOT NULL DEFAULT 0,
		    views_last_7d BIGINT NOT NULL DEFAULT 0,
		    engagement_score DECIMAL(10,4) NOT NULL DEFAULT 0.0,
		    velocity_score DECIMAL(10,4) NOT NULL DEFAULT 0.0,
		    hourly_rank INTEGER,
		    daily_rank INTEGER,
		    weekly_rank INTEGER,
		    last_updated TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    is_trending BOOLEAN NOT NULL DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS idx_trending_videos_scores ON trending_videos(engagement_score DESC, velocity_score DESC)`,
		// Utility functions for view tracking
		`CREATE OR REPLACE FUNCTION increment_video_views(p_video_id UUID)
		RETURNS void AS $$
		BEGIN
		    UPDATE videos
		    SET views = views + 1, updated_at = NOW()
		    WHERE id = p_video_id;
		END;
		$$ LANGUAGE plpgsql`,
		`CREATE OR REPLACE FUNCTION get_unique_views(
		    p_video_id UUID,
		    p_start_date TIMESTAMP WITH TIME ZONE DEFAULT NOW() - INTERVAL '30 days',
		    p_end_date TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
		RETURNS BIGINT AS $$
		DECLARE
		    unique_count BIGINT;
		BEGIN
		    SELECT COUNT(DISTINCT session_id)
		    INTO unique_count
		    FROM user_views
		    WHERE video_id = p_video_id
		    AND created_at BETWEEN p_start_date AND p_end_date;

		    RETURN COALESCE(unique_count, 0);
		END;
		$$ LANGUAGE plpgsql`,
		`CREATE OR REPLACE FUNCTION calculate_engagement_score(
		    p_video_id UUID,
		    p_hours_back INTEGER DEFAULT 24
		)
		RETURNS DECIMAL(10,4) AS $$
		DECLARE
		    score DECIMAL(10,4) := 0.0;
		    view_count BIGINT;
		    avg_completion DECIMAL(5,2);
		    unique_viewers BIGINT;
		    recency_weight DECIMAL(4,2);
		BEGIN
		    SELECT
		        COUNT(*),
		        AVG(completion_percentage),
		        COUNT(DISTINCT session_id)
		    INTO view_count, avg_completion, unique_viewers
		    FROM user_views
		    WHERE video_id = p_video_id
		    AND created_at >= NOW() - (p_hours_back || ' hours')::INTERVAL;

		    recency_weight := CASE
		        WHEN p_hours_back <= 1 THEN 2.0
		        WHEN p_hours_back <= 6 THEN 1.5
		        WHEN p_hours_back <= 24 THEN 1.2
		        ELSE 1.0
		    END;

		    score := (
		        (COALESCE(view_count, 0) * 1.0) +
		        (COALESCE(unique_viewers, 0) * 1.5) +
		        (COALESCE(avg_completion, 0) / 100.0 * view_count * 2.0)
		    ) * recency_weight;

		    RETURN score;
		END;
		$$ LANGUAGE plpgsql`,
	}

	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("schema setup failed: %w (stmt: %s)", err, s)
		}
	}
	return nil
}

// connectWithRetry attempts to connect and ping the database until the deadline,
// returning the first successful connection or the last error.
func connectWithRetry(dsn string, deadline time.Duration) (*sqlx.DB, error) {
	start := time.Now()
	var last error
	for time.Since(start) < deadline {
		db, err := sqlx.Connect("postgres", dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			pingErr := db.PingContext(ctx)
			cancel()
			if pingErr == nil {
				return db, nil
			}
			_ = db.Close()
			last = pingErr
		} else {
			last = err
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("database not ready after %s: %w", deadline, last)
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupRedis() (*redis.Client, error) {
	// Use TEST_REDIS_URL first, then REDIS_URL, then default
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = os.Getenv("REDIS_URL")
	}
	if redisURL == "" {
		// Default Redis URL: use 6379 for both CI and local since Docker is running on 6379
		redisURL = "redis://localhost:6379/1"
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

	// Clean Postgres tables (in reverse dependency order)
	if testDB.DB != nil {
		tables := []string{
			"abuse_reports",
			"blocklist",
			"instance_config",
			"comment_flags",
			"comments",
			"video_ratings",
			"playlist_items",
			"playlists",
			"captions",
			"subscriptions",
			"channels",
			"oauth_access_tokens",
			"oauth_authorization_codes",
			"oauth_clients",
			"notifications",
			"user_views",
			"daily_video_stats",
			"user_engagement_stats",
			"trending_videos",
			"messages",
			"conversations",
			"encoding_jobs",
			"upload_sessions",
			"videos",
			"video_categories",
			"subscriptions",
			"sessions",
			"refresh_tokens",
			"email_verification_tokens",
			"user_avatars",
			"users",
		}
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

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	redis "github.com/redis/go-redis/v9"
)

var (
	infraCheckOnce   sync.Once
	infraReady       bool
	schemaMigrations sync.Map
)

type schemaMigrationState struct {
	once sync.Once
	err  error
}

type TestDB struct {
	DB    *sqlx.DB
	Redis *redis.Client
}

func SetupTestDB(t testing.TB) *TestDB {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
		return nil
	}

	if !verifyInfra() {
		t.Skip("Skipping test: Infrastructure (Postgres/Redis) not available")
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

func verifyInfra() bool {
	infraCheckOnce.Do(func() {
		_ = godotenv.Load("../../.env.test")
		_ = godotenv.Load(".env.test")
		_ = godotenv.Load("../../.env")
		_ = godotenv.Load(".env")

		if !checkTCP(getPostgresDSN(), "5432") {
			return
		}

		if !checkTCP(getRedisURL(), "6379") {
			return
		}

		infraReady = true
	})
	return infraReady
}

func checkTCP(urlStr, defaultPort string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	host := u.Host
	if host == "" {
		return false
	}

	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, defaultPort)
	}

	conn, err := net.DialTimeout("tcp", host, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func getPostgresDSN() string {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		host := getEnvDefault("TEST_DB_HOST", "localhost")
		defaultPort := "5432"
		port := getEnvDefault("TEST_DB_PORT", defaultPort)
		name := getEnvDefault("TEST_DB_NAME", "vidra_test")
		user := getEnvDefault("TEST_DB_USER", "test_user")
		pass := getEnvDefault("TEST_DB_PASSWORD", "test_password")
		ssl := getEnvDefault("TEST_DB_SSLMODE", "disable")
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, pass, host, port, name, ssl)
	}
	return dbURL
}

func getRedisURL() string {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = os.Getenv("REDIS_URL")
	}
	if redisURL == "" {
		redisURL = "redis://localhost:6379/1"
	}
	return redisURL
}

func setupPostgres() (*sqlx.DB, error) {
	_ = godotenv.Load("../../.env.test")
	_ = godotenv.Load(".env.test")
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	dbURL := getPostgresDSN()
	schema := deriveTestSchema()

	db, err := connectWithRetry(dbURL, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to test database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pqQuoteIdent(schema))); err != nil {
		return nil, fmt.Errorf("failed to create test schema: %w", err)
	}
	_ = db.Close()

	dbURL = withSearchPath(dbURL, schema)

	db, err = connectWithRetry(dbURL, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to reconnect to test database with schema: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := applySchemaMigrations(db, schema); err != nil {
		return nil, err
	}
	if err := applyPostMigrationCompatibility(db); err != nil {
		return nil, err
	}

	return db, nil
}

func deriveTestSchema() string {
	if v := os.Getenv("TEST_SCHEMA"); v != "" {
		return sanitizeSchema(v)
	}
	for i := 1; i < 15; i++ {
		if _, file, _, ok := runtime.Caller(i); ok {
			base := filepath.Base(file)
			if strings.HasSuffix(base, "_test.go") && !strings.Contains(file, filepath.Join("internal", "testutil")) {
				dir := filepath.Dir(file)
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
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 || (b.String()[0] < 'a' || b.String()[0] > 'z') {
		return "t_" + b.String()
	}
	return b.String()
}

func pqQuoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

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
            email_verified BOOLEAN NOT NULL DEFAULT false,
            email_verified_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMP WITH TIME ZONE`,
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
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_encoding_jobs_active_video ON encoding_jobs (video_id) WHERE status IN ('pending','processing')`,
		`CREATE TABLE IF NOT EXISTS messages (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    content TEXT,
		    message_type VARCHAR(20) NOT NULL DEFAULT 'text' CHECK (message_type IN ('text','system','secure','key_exchange')),
		    is_read BOOLEAN NOT NULL DEFAULT false,
		    is_deleted_by_sender BOOLEAN NOT NULL DEFAULT false,
		    is_deleted_by_recipient BOOLEAN NOT NULL DEFAULT false,
		    parent_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
		    encrypted_content TEXT,
		    content_nonce TEXT,
		    pgp_signature TEXT,
		    is_encrypted BOOLEAN NOT NULL DEFAULT false,
		    encryption_version INTEGER DEFAULT 1,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    read_at TIMESTAMP WITH TIME ZONE,
		    CONSTRAINT check_encrypted_content CHECK (
		        (is_encrypted = true AND encrypted_content IS NOT NULL AND content_nonce IS NOT NULL) OR
		        (is_encrypted = false AND content IS NOT NULL AND encrypted_content IS NULL AND content_nonce IS NULL)
		    )
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_sender_id ON messages(sender_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_recipient_id ON messages(recipient_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(sender_id, recipient_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_is_read ON messages(is_read)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_parent_id ON messages(parent_message_id)`,
		`DROP TRIGGER IF EXISTS update_messages_updated_at ON messages`,
		`CREATE TRIGGER update_messages_updated_at BEFORE UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		`CREATE TABLE IF NOT EXISTS conversations (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    participant_one_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    participant_two_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    last_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
		    last_message_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    is_encrypted BOOLEAN NOT NULL DEFAULT false,
		    key_exchange_complete BOOLEAN NOT NULL DEFAULT false,
		    encryption_version INTEGER DEFAULT 1,
		    last_key_rotation TIMESTAMP WITH TIME ZONE,
		    encryption_status VARCHAR(20) NOT NULL DEFAULT 'none' CHECK (encryption_status IN ('none','pending','active')),
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
		`CREATE OR REPLACE FUNCTION ensure_conversation_order() RETURNS TRIGGER AS $$
		DECLARE temp_id UUID; BEGIN
		    IF NEW.participant_one_id > NEW.participant_two_id THEN
		        temp_id := NEW.participant_one_id;
		        NEW.participant_one_id := NEW.participant_two_id;
		        NEW.participant_two_id := temp_id;
		    END IF; RETURN NEW; END; $$ language 'plpgsql'`,
		`DROP TRIGGER IF EXISTS ensure_conversation_order_trigger ON conversations`,
		`CREATE TRIGGER ensure_conversation_order_trigger BEFORE INSERT OR UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION ensure_conversation_order()`,
		`CREATE TABLE IF NOT EXISTS user_master_keys (
		    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    encrypted_master_key TEXT NOT NULL,
		    argon2_salt TEXT NOT NULL,
		    argon2_memory INTEGER NOT NULL DEFAULT 65536,
		    argon2_time INTEGER NOT NULL DEFAULT 3,
		    argon2_parallelism INTEGER NOT NULL DEFAULT 4,
		    key_version INTEGER NOT NULL DEFAULT 1,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    CONSTRAINT check_argon2_params CHECK (argon2_memory >= 32768 AND argon2_time >= 2 AND argon2_parallelism >= 1)
		)`,
		`DROP TRIGGER IF EXISTS update_user_master_keys_updated_at ON user_master_keys`,
		`CREATE TRIGGER update_user_master_keys_updated_at BEFORE UPDATE ON user_master_keys FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
		`CREATE TABLE IF NOT EXISTS conversation_keys (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    encrypted_private_key TEXT NOT NULL,
		    public_key TEXT NOT NULL,
		    encrypted_shared_secret TEXT,
		    key_version INTEGER NOT NULL DEFAULT 1,
		    is_active BOOLEAN NOT NULL DEFAULT true,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    expires_at TIMESTAMP WITH TIME ZONE,
		    UNIQUE(conversation_id, user_id, key_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_keys_conversation_user ON conversation_keys(conversation_id, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_keys_active ON conversation_keys(is_active) WHERE is_active = true`,
		`CREATE TABLE IF NOT EXISTS key_exchange_messages (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    exchange_type VARCHAR(20) NOT NULL CHECK (exchange_type IN ('offer','accept','confirm')),
		    public_key TEXT NOT NULL,
		    signature TEXT NOT NULL,
		    nonce TEXT NOT NULL,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
		    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (NOW() + INTERVAL '1 hour')
		)`,
		`CREATE INDEX IF NOT EXISTS idx_key_exchange_conversation ON key_exchange_messages(conversation_id)`,
		`CREATE TABLE IF NOT EXISTS user_signing_keys (
		    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		    encrypted_private_key TEXT,
		    public_key TEXT NOT NULL,
		    public_identity_key TEXT,
		    key_version INTEGER NOT NULL DEFAULT 1,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS crypto_audit_log (
		    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		    conversation_id UUID REFERENCES conversations(id) ON DELETE CASCADE,
		    operation VARCHAR(50) NOT NULL,
		    success BOOLEAN NOT NULL,
		    error_message TEXT,
		    client_ip INET,
		    user_agent TEXT,
		    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_crypto_audit_user ON crypto_audit_log(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_crypto_audit_conversation ON crypto_audit_log(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_encrypted ON messages(is_encrypted) WHERE is_encrypted = true`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_encrypted ON conversations(is_encrypted) WHERE is_encrypted = true`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_encryption_status ON conversations(encryption_status) WHERE encryption_status != 'none'`,
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

func applySchemaMigrations(db *sqlx.DB, schema string) error {
	stateAny, _ := schemaMigrations.LoadOrStore(schema, &schemaMigrationState{})
	state := stateAny.(*schemaMigrationState)

	state.once.Do(func() {
		state.err = runSchemaMigrations(db, schema)
	})

	return state.err
}

func applyPostMigrationCompatibility(db *sqlx.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	compatStmts := []string{
		`ALTER TABLE notifications ALTER COLUMN title DROP NOT NULL`,
		`ALTER TABLE videos ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL`,
		`ALTER TABLE videos ALTER COLUMN channel_id DROP NOT NULL`,
		`ALTER TABLE videos DROP CONSTRAINT IF EXISTS unique_remote_uri`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_videos_remote_uri_unique_nonnull
            ON videos(remote_uri) WHERE remote_uri IS NOT NULL`,
		`ALTER TABLE iota_payment_intents DROP CONSTRAINT IF EXISTS fk_iota_payment_intents_transaction`,
	}

	for _, stmt := range compatStmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "does not exist") {
				continue
			}
			return fmt.Errorf("failed to apply post-migration compatibility statement %q: %w", stmt, err)
		}
	}
	return nil
}

func runSchemaMigrations(db *sqlx.DB, schema string) error {
	_ = db

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("failed to resolve testutil path for migrations")
	}

	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	migrationFiles, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("failed to list migrations: %w", err)
	}
	if len(migrationFiles) == 0 {
		return fmt.Errorf("no migration files found at %s", migrationsDir)
	}
	sort.Strings(migrationFiles)

	baseDSN := getPostgresDSN()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resetCmd := exec.CommandContext(ctx, "psql",
		baseDSN,
		"-v", "ON_ERROR_STOP=1",
		"-c", fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s;", pqQuoteIdent(schema), pqQuoteIdent(schema)),
	)
	resetCmd.Stdin = strings.NewReader("")
	resetOut, err := resetCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reset schema %s: %w\n%s", schema, err, string(resetOut))
	}

	var script strings.Builder
	fmt.Fprintf(&script, "SET search_path TO %s,public;\n\n", pqQuoteIdent(schema))
	for _, file := range migrationFiles {
		content, readErr := os.ReadFile(file)
		if readErr != nil {
			return fmt.Errorf("failed to read migration %s: %w", file, readErr)
		}
		upSQL := extractGooseUpSQL(string(content))
		if strings.TrimSpace(upSQL) == "" {
			continue
		}
		script.WriteString("-- " + filepath.Base(file) + "\n")
		script.WriteString(upSQL)
		if !strings.HasSuffix(upSQL, "\n") {
			script.WriteString("\n")
		}
		script.WriteString("\n")
	}

	tmpFile, err := os.CreateTemp("", "vidra-test-migrations-*.sql")
	if err != nil {
		return fmt.Errorf("failed to create temp migration script: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)
	if _, err := tmpFile.WriteString(script.String()); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp migration script: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp migration script: %w", err)
	}

	applyCmd := exec.CommandContext(ctx, "psql",
		baseDSN,
		"-v", "ON_ERROR_STOP=1",
		"-f", tmpName,
	)
	applyCmd.Stdin = strings.NewReader("")
	applyOut, err := applyCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to apply schema migrations for %s: %w\n%s", schema, err, string(applyOut))
	}

	return nil
}

func extractGooseUpSQL(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	inUp := false
	foundUpMarker := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- +goose Up") {
			inUp = true
			foundUpMarker = true
			continue
		}
		if strings.HasPrefix(trimmed, "-- +goose Down") {
			break
		}
		if !inUp {
			continue
		}
		if strings.HasPrefix(trimmed, "-- +goose StatementBegin") ||
			strings.HasPrefix(trimmed, "-- +goose StatementEnd") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if foundUpMarker {
		return b.String()
	}

	return content
}

func withSearchPath(dbURL, schema string) string {
	if strings.Contains(dbURL, "://") {
		u, parseErr := url.Parse(dbURL)
		if parseErr == nil {
			q := u.Query()
			q.Set("search_path", fmt.Sprintf("%s,public", schema))
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return dbURL + fmt.Sprintf(" search_path='%s,public'", schema)
}

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
	redisURL := getRedisURL()
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

func cleanupTestDB(t testing.TB, testDB *TestDB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if testDB.Redis != nil {
		if err := testDB.Redis.FlushDB(ctx).Err(); err != nil {
			t.Logf("Failed to flush Redis: %v", err)
		}
		err := testDB.Redis.Close()
		if err != nil {
			t.Logf("Failed to close Redis client: %v", err)
		}
	}

	if testDB.DB != nil {
		var tables []string
		if err := testDB.DB.SelectContext(ctx, &tables, `
			SELECT tablename
			FROM pg_tables
			WHERE schemaname = current_schema()
			  AND tablename <> 'goose_db_version'
			ORDER BY tablename`); err != nil {
			t.Logf("Failed to list tables for cleanup: %v", err)
		} else if len(tables) > 0 {
			quoted := make([]string, 0, len(tables))
			for _, table := range tables {
				quoted = append(quoted, pqQuoteIdent(table))
			}
			stmt := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", strings.Join(quoted, ", "))
			if _, err := testDB.DB.ExecContext(ctx, stmt); err != nil {
				t.Logf("Failed to truncate tables: %v", err)
			}
		}
		err := testDB.DB.Close()
		if err != nil {
			t.Logf("Failed to close Postgres DB: %v", err)
		}
	}
}

func (tdb *TestDB) TruncateTables(t testing.TB, tables ...string) {
	t.Helper()

	for _, table := range tables {
		stmt := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)

		var err error
		for attempt := 0; attempt < 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err = tdb.DB.ExecContext(ctx, stmt)
			cancel()
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("Failed to truncate table %s: %v", table, err)
		}
	}
}

func (tdb *TestDB) WithTx(t testing.TB, fn func(*sqlx.Tx)) {
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

-- Enable required PostgreSQL extensions for Athena test database
-- As specified in CLAUDE.md

-- UUID generation extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
-- Crypto functions for gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Full-text search with trigram matching
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Accent-insensitive text search
CREATE EXTENSION IF NOT EXISTS unaccent;

-- Generalized Inverted Index (GIN) for B-tree operations
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Create update trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Create users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(50) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(100),
    bio TEXT,
    bitcoin_wallet VARCHAR(62),
    role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin', 'moderator')),
    password_hash TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for users
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_is_active ON users(is_active);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at);
CREATE INDEX IF NOT EXISTS idx_users_bitcoin_wallet ON users(bitcoin_wallet);

-- Create trigger for users
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
CREATE TRIGGER update_users_updated_at 
    BEFORE UPDATE ON users 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Create user_avatars table for tests
CREATE TABLE IF NOT EXISTS user_avatars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID,
    ipfs_cid TEXT,
    webp_ipfs_cid TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS update_user_avatars_updated_at ON user_avatars;
CREATE TRIGGER update_user_avatars_updated_at 
    BEFORE UPDATE ON user_avatars 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Create refresh_tokens table
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMP WITH TIME ZONE
);

-- Create indexes for refresh_tokens
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token ON refresh_tokens(token);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_revoked_at ON refresh_tokens(revoked_at);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_active ON refresh_tokens(user_id, expires_at) 
    WHERE revoked_at IS NULL;

-- Create sessions table
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(255) PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for sessions
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
-- Active sessions index (avoid non-immutable NOW() in predicate)
DROP INDEX IF EXISTS idx_sessions_active;
CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(user_id, expires_at);

-- Create videos table
CREATE TABLE IF NOT EXISTS videos (
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
    -- New media pipeline columns
    output_paths JSONB NOT NULL DEFAULT '{}'::jsonb,
    thumbnail_path TEXT,
    preview_path TEXT,
    -- Tags are now nullable per migration 010
    tags TEXT[] DEFAULT '{}',
    category_id UUID,
    language VARCHAR(10),
    file_size BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(120),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_videos_user_id ON videos(user_id);
CREATE INDEX IF NOT EXISTS idx_videos_privacy ON videos(privacy);
CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status);
CREATE INDEX IF NOT EXISTS idx_videos_upload_date ON videos(upload_date);

-- Trigger for videos
DROP TRIGGER IF EXISTS update_videos_updated_at ON videos;
CREATE TRIGGER update_videos_updated_at 
    BEFORE UPDATE ON videos 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Create upload_sessions table (test)
CREATE TABLE IF NOT EXISTS upload_sessions (
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
    expected_checksum TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_video_id ON upload_sessions(video_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_user_id ON upload_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_status ON upload_sessions(status);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires_at ON upload_sessions(expires_at);

DROP TRIGGER IF EXISTS update_upload_sessions_updated_at ON upload_sessions;
CREATE TRIGGER update_upload_sessions_updated_at BEFORE UPDATE ON upload_sessions FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create encoding_jobs table (test)
CREATE TABLE IF NOT EXISTS encoding_jobs (
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
);

CREATE INDEX IF NOT EXISTS idx_encoding_jobs_video_id ON encoding_jobs(video_id);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status ON encoding_jobs(status);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_created_at ON encoding_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status_created ON encoding_jobs(status, created_at);

-- Ensure at most one active job per video
CREATE UNIQUE INDEX IF NOT EXISTS uq_encoding_jobs_active_video
ON encoding_jobs (video_id)
WHERE status IN ('pending','processing');

DROP TRIGGER IF EXISTS update_encoding_jobs_updated_at ON encoding_jobs;
CREATE TRIGGER update_encoding_jobs_updated_at BEFORE UPDATE ON encoding_jobs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Subscriptions for tests: table and triggers
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS subscriber_count BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(subscriber_id, channel_id)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions(subscriber_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_channel ON subscriptions(channel_id);

CREATE OR REPLACE FUNCTION increment_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE users SET subscriber_count = subscriber_count + 1, updated_at = NOW()
    WHERE id = NEW.channel_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION decrement_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE users SET subscriber_count = GREATEST(subscriber_count - 1, 0), updated_at = NOW()
    WHERE id = OLD.channel_id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_subscriptions_inc ON subscriptions;
CREATE TRIGGER trg_subscriptions_inc
AFTER INSERT ON subscriptions
FOR EACH ROW EXECUTE FUNCTION increment_subscriber_count();

DROP TRIGGER IF EXISTS trg_subscriptions_dec ON subscriptions;
CREATE TRIGGER trg_subscriptions_dec
AFTER DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION decrement_subscriber_count();

-- Log successful initialization
\echo 'PostgreSQL test database initialized successfully for Athena platform with all tables and indexes';

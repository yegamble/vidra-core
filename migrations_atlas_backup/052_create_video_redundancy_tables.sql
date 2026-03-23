-- Migration: Create video redundancy tables
-- Description: Adds tables for video redundancy management across instances
-- Version: 052
-- Date: 2025-10-23

-- Create redundancy status enum
CREATE TYPE redundancy_status AS ENUM (
    'pending',
    'syncing',
    'synced',
    'failed',
    'cancelled'
);

-- Create redundancy strategy enum
CREATE TYPE redundancy_strategy AS ENUM (
    'recent',
    'most_viewed',
    'trending',
    'manual',
    'all'
);

-- Table: instance_peers
-- Stores information about known peer instances for redundancy
CREATE TABLE IF NOT EXISTS instance_peers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    instance_url TEXT NOT NULL UNIQUE,
    instance_name TEXT,
    instance_host TEXT NOT NULL,
    software TEXT, -- peertube, vidra, mastodon
    version TEXT,

    -- Redundancy configuration
    auto_accept_redundancy BOOLEAN DEFAULT false,
    max_redundancy_size_gb INTEGER DEFAULT 0, -- 0 = unlimited
    accepts_new_redundancy BOOLEAN DEFAULT true,

    -- Health metrics
    last_contacted_at TIMESTAMP,
    last_sync_success_at TIMESTAMP,
    last_sync_error TEXT,
    failed_sync_count INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT true,

    -- ActivityPub actor information
    actor_url TEXT,
    inbox_url TEXT,
    shared_inbox_url TEXT,
    public_key TEXT,

    -- Statistics
    total_videos_stored INTEGER DEFAULT 0,
    total_storage_bytes BIGINT DEFAULT 0,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT valid_max_size CHECK (max_redundancy_size_gb >= 0)
);

-- Table: video_redundancy
-- Tracks redundancy copies of videos on peer instances
CREATE TABLE IF NOT EXISTS video_redundancy (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    target_instance_id UUID NOT NULL REFERENCES instance_peers(id) ON DELETE CASCADE,

    -- URLs and identifiers
    target_video_url TEXT,
    target_video_id TEXT, -- Video ID on target instance

    -- Status and strategy
    status redundancy_status NOT NULL DEFAULT 'pending',
    strategy redundancy_strategy NOT NULL DEFAULT 'manual',

    -- File information
    file_size_bytes BIGINT DEFAULT 0,
    checksum_sha256 TEXT,
    checksum_verified_at TIMESTAMP,

    -- Sync progress
    bytes_transferred BIGINT DEFAULT 0,
    transfer_speed_bps BIGINT DEFAULT 0, -- bytes per second
    estimated_completion_at TIMESTAMP,

    -- Sync status
    sync_started_at TIMESTAMP,
    last_sync_at TIMESTAMP,
    next_sync_at TIMESTAMP,
    sync_attempt_count INTEGER DEFAULT 0,
    max_sync_attempts INTEGER DEFAULT 5,
    sync_error TEXT,

    -- Priority and scheduling
    priority INTEGER DEFAULT 0, -- Higher = more important
    auto_resync BOOLEAN DEFAULT true, -- Weekly checksum verification

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(video_id, target_instance_id),
    CONSTRAINT valid_file_size CHECK (file_size_bytes >= 0),
    CONSTRAINT valid_bytes_transferred CHECK (bytes_transferred >= 0 AND bytes_transferred <= file_size_bytes),
    CONSTRAINT valid_sync_attempts CHECK (sync_attempt_count >= 0 AND sync_attempt_count <= max_sync_attempts),
    CONSTRAINT valid_priority CHECK (priority >= 0)
);

-- Table: redundancy_policies
-- Defines automatic redundancy policies for video distribution
CREATE TABLE IF NOT EXISTS redundancy_policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    description TEXT,

    -- Policy configuration
    strategy redundancy_strategy NOT NULL DEFAULT 'manual',
    enabled BOOLEAN DEFAULT true,

    -- Selection criteria
    min_views INTEGER DEFAULT 0,
    min_age_days INTEGER DEFAULT 0,
    max_age_days INTEGER, -- NULL = no limit
    privacy_types TEXT[] DEFAULT '{"public"}', -- public, unlisted, private

    -- Redundancy targets
    target_instance_count INTEGER DEFAULT 1,
    min_instance_count INTEGER DEFAULT 1, -- Minimum redundancy copies

    -- Size limits
    max_video_size_gb INTEGER, -- NULL = no limit
    max_total_size_gb INTEGER, -- Total size for this policy

    -- Scheduling
    evaluation_interval_hours INTEGER DEFAULT 24,
    last_evaluated_at TIMESTAMP,
    next_evaluation_at TIMESTAMP,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT valid_instance_counts CHECK (target_instance_count >= min_instance_count AND min_instance_count > 0),
    CONSTRAINT valid_age_range CHECK (max_age_days IS NULL OR max_age_days > min_age_days),
    CONSTRAINT valid_evaluation_interval CHECK (evaluation_interval_hours > 0)
);

-- Table: redundancy_sync_log
-- Detailed log of sync operations for debugging and monitoring
CREATE TABLE IF NOT EXISTS redundancy_sync_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    redundancy_id UUID NOT NULL REFERENCES video_redundancy(id) ON DELETE CASCADE,

    -- Sync attempt information
    attempt_number INTEGER NOT NULL,
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,

    -- Transfer metrics
    bytes_transferred BIGINT DEFAULT 0,
    transfer_duration_seconds INTEGER,
    average_speed_bps BIGINT,

    -- Result
    success BOOLEAN,
    error_message TEXT,
    error_type TEXT, -- network, auth, storage, checksum, timeout

    -- Additional context
    http_status_code INTEGER,
    retry_after_seconds INTEGER,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT valid_transfer_duration CHECK (transfer_duration_seconds IS NULL OR transfer_duration_seconds >= 0)
);

-- Indexes for instance_peers
CREATE INDEX idx_instance_peers_url ON instance_peers(instance_url);
CREATE INDEX idx_instance_peers_host ON instance_peers(instance_host);
CREATE INDEX idx_instance_peers_active ON instance_peers(is_active) WHERE is_active = true;
CREATE INDEX idx_instance_peers_auto_accept ON instance_peers(auto_accept_redundancy) WHERE auto_accept_redundancy = true;
CREATE INDEX idx_instance_peers_last_contacted ON instance_peers(last_contacted_at DESC);

-- Indexes for video_redundancy
CREATE INDEX idx_video_redundancy_video_id ON video_redundancy(video_id);
CREATE INDEX idx_video_redundancy_instance ON video_redundancy(target_instance_id);
CREATE INDEX idx_video_redundancy_status ON video_redundancy(status);
CREATE INDEX idx_video_redundancy_strategy ON video_redundancy(strategy);
CREATE INDEX idx_video_redundancy_next_sync ON video_redundancy(next_sync_at) WHERE status IN ('pending', 'failed');
CREATE INDEX idx_video_redundancy_priority ON video_redundancy(priority DESC, created_at ASC);
CREATE INDEX idx_video_redundancy_failed ON video_redundancy(status, sync_attempt_count) WHERE status = 'failed';
CREATE INDEX idx_video_redundancy_auto_resync ON video_redundancy(auto_resync, last_sync_at) WHERE auto_resync = true;

-- Indexes for redundancy_policies
CREATE INDEX idx_redundancy_policies_enabled ON redundancy_policies(enabled) WHERE enabled = true;
CREATE INDEX idx_redundancy_policies_next_eval ON redundancy_policies(next_evaluation_at) WHERE enabled = true;

-- Indexes for redundancy_sync_log
CREATE INDEX idx_redundancy_sync_log_redundancy_id ON redundancy_sync_log(redundancy_id);
CREATE INDEX idx_redundancy_sync_log_created_at ON redundancy_sync_log(created_at DESC);
CREATE INDEX idx_redundancy_sync_log_success ON redundancy_sync_log(success);

-- Function: Update instance peer statistics
CREATE OR REPLACE FUNCTION update_instance_peer_stats()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.status = 'synced' AND OLD.status != 'synced') THEN
        UPDATE instance_peers
        SET
            total_videos_stored = total_videos_stored + 1,
            total_storage_bytes = total_storage_bytes + NEW.file_size_bytes,
            last_sync_success_at = NOW(),
            failed_sync_count = 0,
            updated_at = NOW()
        WHERE id = NEW.target_instance_id;
    ELSIF TG_OP = 'UPDATE' AND NEW.status = 'failed' AND OLD.status != 'failed' THEN
        UPDATE instance_peers
        SET
            failed_sync_count = failed_sync_count + 1,
            last_sync_error = NEW.sync_error,
            updated_at = NOW()
        WHERE id = NEW.target_instance_id;
    ELSIF TG_OP = 'DELETE' AND OLD.status = 'synced' THEN
        UPDATE instance_peers
        SET
            total_videos_stored = GREATEST(0, total_videos_stored - 1),
            total_storage_bytes = GREATEST(0, total_storage_bytes - OLD.file_size_bytes),
            updated_at = NOW()
        WHERE id = OLD.target_instance_id;
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- Trigger: Update instance peer stats on redundancy changes
CREATE TRIGGER trigger_update_instance_peer_stats
AFTER INSERT OR UPDATE OF status OR DELETE ON video_redundancy
FOR EACH ROW
EXECUTE FUNCTION update_instance_peer_stats();

-- Function: Calculate next sync time based on failures
CREATE OR REPLACE FUNCTION calculate_next_sync_time(
    attempt_count INTEGER,
    base_delay_minutes INTEGER DEFAULT 60
) RETURNS TIMESTAMP AS $$
DECLARE
    delay_minutes INTEGER;
BEGIN
    -- Exponential backoff: 1h, 2h, 4h, 8h, 16h, then cap at 24h
    delay_minutes := LEAST(base_delay_minutes * POWER(2, attempt_count), 1440);
    RETURN NOW() + (delay_minutes || ' minutes')::INTERVAL;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Function: Cleanup old sync logs (keep last 100 per redundancy)
CREATE OR REPLACE FUNCTION cleanup_old_redundancy_logs()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    WITH logs_to_keep AS (
        SELECT id
        FROM (
            SELECT id,
                   ROW_NUMBER() OVER (PARTITION BY redundancy_id ORDER BY created_at DESC) as rn
            FROM redundancy_sync_log
        ) ranked
        WHERE rn <= 100
    )
    DELETE FROM redundancy_sync_log
    WHERE id NOT IN (SELECT id FROM logs_to_keep)
      AND created_at < NOW() - INTERVAL '30 days';

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Function: Get redundancy health score for a video
CREATE OR REPLACE FUNCTION get_video_redundancy_health(p_video_id UUID)
RETURNS DECIMAL AS $$
DECLARE
    total_redundancies INTEGER;
    synced_redundancies INTEGER;
    failed_redundancies INTEGER;
    health_score DECIMAL;
BEGIN
    SELECT
        COUNT(*),
        COUNT(*) FILTER (WHERE status = 'synced'),
        COUNT(*) FILTER (WHERE status = 'failed')
    INTO total_redundancies, synced_redundancies, failed_redundancies
    FROM video_redundancy
    WHERE video_id = p_video_id;

    IF total_redundancies = 0 THEN
        RETURN 0.0;
    END IF;

    -- Health score: (synced - (failed * 0.5)) / total
    health_score := (synced_redundancies - (failed_redundancies * 0.5)) / total_redundancies;
    RETURN GREATEST(0.0, LEAST(1.0, health_score));
END;
$$ LANGUAGE plpgsql;

-- Function: Mark instance as inactive if too many failures
CREATE OR REPLACE FUNCTION check_instance_health()
RETURNS INTEGER AS $$
DECLARE
    updated_count INTEGER;
BEGIN
    UPDATE instance_peers
    SET
        is_active = false,
        updated_at = NOW()
    WHERE is_active = true
      AND failed_sync_count >= 10
      AND last_contacted_at < NOW() - INTERVAL '7 days';

    GET DIAGNOSTICS updated_count = ROW_COUNT;
    RETURN updated_count;
END;
$$ LANGUAGE plpgsql;

-- Insert default redundancy policy
INSERT INTO redundancy_policies (name, description, strategy, enabled, min_views, target_instance_count, evaluation_interval_hours)
VALUES
    ('trending-videos', 'Replicate trending videos with high view counts', 'trending', true, 1000, 2, 12),
    ('recent-uploads', 'Replicate recently uploaded public videos', 'recent', true, 0, 1, 6)
ON CONFLICT (name) DO NOTHING;

-- Add comments for documentation
COMMENT ON TABLE instance_peers IS 'Known peer instances for video redundancy distribution';
COMMENT ON TABLE video_redundancy IS 'Redundancy copies of videos on peer instances';
COMMENT ON TABLE redundancy_policies IS 'Automatic redundancy policies for video distribution';
COMMENT ON TABLE redundancy_sync_log IS 'Detailed log of sync operations for monitoring';

COMMENT ON COLUMN instance_peers.auto_accept_redundancy IS 'Whether this instance automatically accepts redundancy requests';
COMMENT ON COLUMN video_redundancy.checksum_sha256 IS 'SHA256 checksum of the video file for integrity verification';
COMMENT ON COLUMN video_redundancy.auto_resync IS 'Whether to automatically verify checksum weekly';
COMMENT ON COLUMN redundancy_policies.strategy IS 'Strategy for selecting videos to replicate';

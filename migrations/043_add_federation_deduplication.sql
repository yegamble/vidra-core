-- +goose Up
-- +goose StatementBegin
-- Federation deduplication and conflict resolution enhancements

-- Add content hash to federated_posts for deduplication
ALTER TABLE federated_posts
ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64),
ADD COLUMN IF NOT EXISTS duplicate_of UUID REFERENCES federated_posts(id),
ADD COLUMN IF NOT EXISTS conflict_resolution_strategy VARCHAR(32) DEFAULT 'latest',
ADD COLUMN IF NOT EXISTS version_number INTEGER DEFAULT 1,
ADD COLUMN IF NOT EXISTS is_canonical BOOLEAN DEFAULT true;

-- Create index for efficient duplicate detection
CREATE INDEX IF NOT EXISTS idx_federated_posts_content_hash
ON federated_posts(content_hash)
WHERE content_hash IS NOT NULL;

-- Create index for finding duplicates
CREATE INDEX IF NOT EXISTS idx_federated_posts_duplicate_of
ON federated_posts(duplicate_of)
WHERE duplicate_of IS NOT NULL;

-- Create index for canonical posts
CREATE INDEX IF NOT EXISTS idx_federated_posts_canonical
ON federated_posts(is_canonical)
WHERE is_canonical = true;

-- Table to track duplicate resolution history
CREATE TABLE IF NOT EXISTS federation_duplicate_resolutions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    original_post_id UUID REFERENCES federated_posts(id) ON DELETE CASCADE,
    duplicate_post_id UUID REFERENCES federated_posts(id) ON DELETE CASCADE,
    resolution_type VARCHAR(32) NOT NULL, -- 'merge', 'keep_latest', 'keep_original', 'manual'
    resolution_metadata JSONB,
    resolved_by VARCHAR(256),
    resolved_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(original_post_id, duplicate_post_id)
);

CREATE INDEX IF NOT EXISTS idx_duplicate_resolutions_resolved_at
ON federation_duplicate_resolutions(resolved_at DESC);

-- Circuit breaker state for federation endpoints
CREATE TABLE IF NOT EXISTS federation_circuit_breakers (
    endpoint VARCHAR(512) PRIMARY KEY,
    state VARCHAR(16) NOT NULL DEFAULT 'closed', -- 'closed', 'open', 'half_open'
    failure_count INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    last_failure_at TIMESTAMP,
    last_success_at TIMESTAMP,
    opens_at TIMESTAMP,
    half_opens_at TIMESTAMP,
    consecutive_failures INTEGER DEFAULT 0,
    consecutive_successes INTEGER DEFAULT 0,
    error_rate FLOAT DEFAULT 0.0,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_circuit_breakers_state
ON federation_circuit_breakers(state);

CREATE INDEX IF NOT EXISTS idx_circuit_breakers_half_opens
ON federation_circuit_breakers(half_opens_at)
WHERE state = 'open' AND half_opens_at IS NOT NULL;

-- Backpressure monitoring
CREATE TABLE IF NOT EXISTS federation_backpressure (
    instance_domain VARCHAR(256) PRIMARY KEY,
    queue_depth INTEGER DEFAULT 0,
    processing_rate FLOAT DEFAULT 0.0, -- items per second
    error_rate FLOAT DEFAULT 0.0,      -- percentage
    throttle_factor FLOAT DEFAULT 1.0, -- multiplier for rate limiting
    is_throttled BOOLEAN DEFAULT false,
    last_measured_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_backpressure_throttled
ON federation_backpressure(is_throttled)
WHERE is_throttled = true;

-- Function to calculate content hash for deduplication
CREATE OR REPLACE FUNCTION calculate_content_hash(
    actor_did TEXT,
    uri TEXT,
    text TEXT,
    embed_url TEXT,
    cid TEXT
) RETURNS VARCHAR(64) AS $$
BEGIN
    -- Use SHA256 hash of concatenated key fields
    RETURN encode(
        digest(
            COALESCE(actor_did, '') || '|' ||
            COALESCE(uri, '') || '|' ||
            COALESCE(text, '') || '|' ||
            COALESCE(embed_url, '') || '|' ||
            COALESCE(cid, ''),
            'sha256'
        ),
        'hex'
    );
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Trigger to automatically calculate content hash
CREATE OR REPLACE FUNCTION update_content_hash() RETURNS TRIGGER AS $$
BEGIN
    NEW.content_hash := calculate_content_hash(
        NEW.actor_did,
        NEW.uri,
        NEW.text,
        NEW.embed_url,
        NEW.cid
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_federated_posts_content_hash
BEFORE INSERT OR UPDATE ON federated_posts
FOR EACH ROW EXECUTE FUNCTION update_content_hash();

-- Function to handle duplicate detection
CREATE OR REPLACE FUNCTION detect_and_handle_duplicate() RETURNS TRIGGER AS $$
DECLARE
    existing_id UUID;
    existing_version INTEGER;
BEGIN
    -- Check for existing post with same content hash
    SELECT id, version_number INTO existing_id, existing_version
    FROM federated_posts
    WHERE content_hash = NEW.content_hash
    AND id != NEW.id
    AND is_canonical = true
    LIMIT 1;

    IF existing_id IS NOT NULL THEN
        -- Mark new post as duplicate
        NEW.duplicate_of := existing_id;
        NEW.is_canonical := false;
        NEW.version_number := existing_version + 1;

        -- Record the duplicate resolution
        INSERT INTO federation_duplicate_resolutions (
            original_post_id,
            duplicate_post_id,
            resolution_type,
            resolution_metadata
        ) VALUES (
            existing_id,
            NEW.id,
            'automatic',
            jsonb_build_object(
                'strategy', NEW.conflict_resolution_strategy,
                'version_diff', NEW.version_number - existing_version
            )
        ) ON CONFLICT DO NOTHING;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER detect_duplicate_posts
BEFORE INSERT ON federated_posts
FOR EACH ROW EXECUTE FUNCTION detect_and_handle_duplicate();

-- Update existing posts with content hash
UPDATE federated_posts
SET content_hash = calculate_content_hash(
    actor_did,
    uri,
    text,
    embed_url,
    cid
)
WHERE content_hash IS NULL;

-- Add triggers for updated_at
CREATE TRIGGER update_circuit_breakers_updated_at
BEFORE UPDATE ON federation_circuit_breakers
FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_backpressure_updated_at
BEFORE UPDATE ON federation_backpressure
FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Configuration for deduplication and retry settings
INSERT INTO instance_config (key, value, description, is_public) VALUES
    ('federation_dedup_enabled', 'true'::jsonb, 'Enable automatic deduplication of federated posts', false),
    ('federation_conflict_strategy', '"latest"'::jsonb, 'Default conflict resolution strategy: latest, original, merge', false),
    ('federation_circuit_breaker_threshold', '5'::jsonb, 'Number of failures before opening circuit', false),
    ('federation_circuit_breaker_timeout', '60'::jsonb, 'Seconds before attempting to half-open circuit', false),
    ('federation_backpressure_threshold', '1000'::jsonb, 'Queue depth before applying backpressure', false),
    ('federation_backpressure_throttle_factor', '0.5'::jsonb, 'Rate reduction factor when throttled', false)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

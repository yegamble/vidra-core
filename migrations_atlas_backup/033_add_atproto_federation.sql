-- ATProto federation: jobs queue and ingested posts

CREATE TABLE IF NOT EXISTS federation_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type VARCHAR(64) NOT NULL, -- e.g., publish_post, ingest_actor
    payload JSONB NOT NULL,        -- parameters (videoId, actor, etc.)
    status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending|processing|completed|failed
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fed_jobs_status_next ON federation_jobs(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_fed_jobs_created ON federation_jobs(created_at);

CREATE TRIGGER update_federation_jobs_updated_at BEFORE UPDATE ON federation_jobs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Minimal table to store ingested remote posts (timeline)
CREATE TABLE IF NOT EXISTS federated_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_did VARCHAR(256) NOT NULL,
    actor_handle VARCHAR(256),
    uri VARCHAR(512) NOT NULL,  -- at://did:.../app.bsky.feed.post/...
    cid VARCHAR(128),
    text TEXT,
    created_at TIMESTAMP,
    indexed_at TIMESTAMP,
    embed_url TEXT,
    embed_title TEXT,
    embed_description TEXT,
    labels JSONB,
    raw JSONB,
    inserted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (uri)
);

CREATE INDEX IF NOT EXISTS idx_fed_posts_actor ON federated_posts(actor_did);
CREATE INDEX IF NOT EXISTS idx_fed_posts_indexed ON federated_posts(indexed_at DESC);

CREATE TRIGGER update_federated_posts_updated_at BEFORE UPDATE ON federated_posts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Instance configuration keys for ingestion sources and label moderation
INSERT INTO instance_config (key, value, description, is_public)
VALUES (
    'atproto_ingest_actors', '[]'::jsonb, 'Array of ATProto actors (handles or DIDs) to ingest', false
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO instance_config (key, value, description, is_public)
VALUES (
    'atproto_block_labels', '[]'::jsonb, 'Array of label values to block from ingestion (e.g., "porn", "sexual")', false
)
ON CONFLICT (key) DO NOTHING;

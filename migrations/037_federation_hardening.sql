-- +goose Up
-- +goose StatementBegin
-- Federation Hardening: DLQ, blocklists, metrics, and reliability features

-- Dead Letter Queue for failed federation jobs
CREATE TABLE IF NOT EXISTS federation_dlq (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    original_job_id UUID,
    job_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    error_message TEXT,
    error_count INTEGER DEFAULT 1,
    last_error_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    can_retry BOOLEAN DEFAULT true,
    metadata JSONB -- Additional debugging info
);

CREATE INDEX idx_fed_dlq_created ON federation_dlq(created_at DESC);
CREATE INDEX idx_fed_dlq_can_retry ON federation_dlq(can_retry, created_at DESC);
CREATE INDEX idx_fed_dlq_job_type ON federation_dlq(job_type);

-- Instance blocklist for federation
CREATE TABLE IF NOT EXISTS federation_instance_blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_domain VARCHAR(256) NOT NULL UNIQUE,
    reason TEXT,
    severity VARCHAR(16) NOT NULL DEFAULT 'block', -- block, shadowban, quarantine
    blocked_by VARCHAR(256), -- Admin who blocked
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    metadata JSONB -- Additional info (abuse reports, etc.)
);

CREATE INDEX idx_instance_blocks_domain ON federation_instance_blocks(instance_domain);
-- Avoid non-immutable CURRENT_TIMESTAMP in predicates; split into two indexes
CREATE INDEX IF NOT EXISTS idx_instance_blocks_active_null ON federation_instance_blocks(instance_domain)
    WHERE expires_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_instance_blocks_expires ON federation_instance_blocks(expires_at);
-- Composite index to aid queries filtering by domain and expiration
CREATE INDEX IF NOT EXISTS idx_instance_blocks_domain_expires ON federation_instance_blocks(instance_domain, expires_at);

-- Actor blocklist for federation
CREATE TABLE IF NOT EXISTS federation_actor_blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_did VARCHAR(256),
    actor_handle VARCHAR(256),
    reason TEXT,
    severity VARCHAR(16) NOT NULL DEFAULT 'block', -- block, shadowban, mute
    blocked_by VARCHAR(256),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    metadata JSONB,
    UNIQUE(actor_did),
    CHECK (actor_did IS NOT NULL OR actor_handle IS NOT NULL)
);

CREATE INDEX idx_actor_blocks_did ON federation_actor_blocks(actor_did);
CREATE INDEX idx_actor_blocks_handle ON federation_actor_blocks(actor_handle);
-- Avoid non-immutable CURRENT_TIMESTAMP in predicates; split into two indexes
CREATE INDEX IF NOT EXISTS idx_actor_blocks_active_null ON federation_actor_blocks(actor_did, actor_handle)
    WHERE expires_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_actor_blocks_expires ON federation_actor_blocks(expires_at);
-- Composite indexes to aid queries filtering by actor and expiration
CREATE INDEX IF NOT EXISTS idx_actor_blocks_did_expires ON federation_actor_blocks(actor_did, expires_at);
CREATE INDEX IF NOT EXISTS idx_actor_blocks_handle_expires ON federation_actor_blocks(actor_handle, expires_at);

-- Federation health metrics table
CREATE TABLE IF NOT EXISTS federation_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    metric_type VARCHAR(64) NOT NULL, -- job_success, job_failure, ingest_rate, etc.
    metric_value DECIMAL NOT NULL,
    instance_domain VARCHAR(256),
    actor_did VARCHAR(256),
    job_type VARCHAR(64),
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata JSONB
);

CREATE INDEX idx_fed_metrics_timestamp ON federation_metrics(timestamp DESC);
CREATE INDEX idx_fed_metrics_type_time ON federation_metrics(metric_type, timestamp DESC);
CREATE INDEX idx_fed_metrics_instance ON federation_metrics(instance_domain, timestamp DESC) WHERE instance_domain IS NOT NULL;
CREATE INDEX idx_fed_metrics_actor ON federation_metrics(actor_did, timestamp DESC) WHERE actor_did IS NOT NULL;

-- Idempotency keys for federation operations
CREATE TABLE IF NOT EXISTS federation_idempotency (
    idempotency_key VARCHAR(256) PRIMARY KEY,
    operation_type VARCHAR(64) NOT NULL,
    payload JSONB,
    result JSONB,
    status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending, success, failed
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '24 hours'
);

CREATE INDEX idx_idempotency_expires ON federation_idempotency(expires_at);
CREATE INDEX idx_idempotency_created ON federation_idempotency(created_at DESC);

-- Request signatures cache for preventing replay attacks
CREATE TABLE IF NOT EXISTS federation_request_signatures (
    signature_hash VARCHAR(256) PRIMARY KEY,
    instance_domain VARCHAR(256) NOT NULL,
    request_path VARCHAR(512),
    received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '5 minutes'
);

CREATE INDEX idx_signatures_expires ON federation_request_signatures(expires_at);
CREATE INDEX idx_signatures_instance ON federation_request_signatures(instance_domain, received_at DESC);

-- Abuse reports for federation content
CREATE TABLE IF NOT EXISTS federation_abuse_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_did VARCHAR(256),
    reported_content_uri VARCHAR(512),
    reported_actor_did VARCHAR(256),
    report_type VARCHAR(64) NOT NULL, -- spam, harassment, illegal, impersonation
    description TEXT,
    evidence JSONB, -- Screenshots, links, etc.
    status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending, reviewing, resolved, rejected
    resolution TEXT,
    resolved_by VARCHAR(256),
    resolved_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_abuse_reports_status ON federation_abuse_reports(status, created_at DESC);
CREATE INDEX idx_abuse_reports_actor ON federation_abuse_reports(reported_actor_did) WHERE reported_actor_did IS NOT NULL;
CREATE INDEX idx_abuse_reports_content ON federation_abuse_reports(reported_content_uri) WHERE reported_content_uri IS NOT NULL;

CREATE TRIGGER update_abuse_reports_updated_at BEFORE UPDATE ON federation_abuse_reports
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Rate limit tracking for federation endpoints
CREATE TABLE IF NOT EXISTS federation_rate_limits (
    id VARCHAR(256) PRIMARY KEY, -- instance_domain or actor_did
    request_count INTEGER NOT NULL DEFAULT 0,
    window_start TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_request TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_blocked BOOLEAN DEFAULT false,
    blocked_until TIMESTAMP
);

CREATE INDEX idx_rate_limits_blocked ON federation_rate_limits(is_blocked, blocked_until) WHERE is_blocked = true;
CREATE INDEX idx_rate_limits_window ON federation_rate_limits(window_start);

-- Materialized view for federation health dashboard
CREATE MATERIALIZED VIEW IF NOT EXISTS federation_health_summary AS
SELECT
    DATE_TRUNC('hour', timestamp) as hour,
    metric_type,
    COUNT(*) as event_count,
    AVG(metric_value) as avg_value,
    MIN(metric_value) as min_value,
    MAX(metric_value) as max_value,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY metric_value) as median_value,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY metric_value) as p95_value
FROM federation_metrics
WHERE timestamp > CURRENT_TIMESTAMP - INTERVAL '7 days'
GROUP BY DATE_TRUNC('hour', timestamp), metric_type;

CREATE UNIQUE INDEX idx_health_summary ON federation_health_summary(hour, metric_type);

-- Function to refresh federation health summary
CREATE OR REPLACE FUNCTION refresh_federation_health()
RETURNS void AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY federation_health_summary;
END;
$$ LANGUAGE plpgsql;

-- Function to clean up expired data
CREATE OR REPLACE FUNCTION cleanup_federation_expired()
RETURNS void AS $$
BEGIN
    -- Delete expired idempotency keys
    DELETE FROM federation_idempotency WHERE expires_at < CURRENT_TIMESTAMP;

    -- Delete expired request signatures
    DELETE FROM federation_request_signatures WHERE expires_at < CURRENT_TIMESTAMP;

    -- Delete old metrics (keep 30 days)
    DELETE FROM federation_metrics WHERE timestamp < CURRENT_TIMESTAMP - INTERVAL '30 days';

    -- Delete old rate limit entries
    DELETE FROM federation_rate_limits WHERE window_start < CURRENT_TIMESTAMP - INTERVAL '1 hour' AND is_blocked = false;
END;
$$ LANGUAGE plpgsql;

-- Update federation_jobs table with backoff fields if not exists
ALTER TABLE federation_jobs ADD COLUMN IF NOT EXISTS backoff_multiplier DECIMAL DEFAULT 1.5;
ALTER TABLE federation_jobs ADD COLUMN IF NOT EXISTS max_backoff_seconds INTEGER DEFAULT 3600;
ALTER TABLE federation_jobs ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(256);

CREATE INDEX IF NOT EXISTS idx_fed_jobs_idempotency ON federation_jobs(idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Configuration for federation hardening
INSERT INTO instance_config (key, value, description, is_public)
VALUES
    ('federation_max_request_size', '10485760'::jsonb, 'Maximum request size in bytes (10MB default)', false),
    ('federation_signature_window_seconds', '300'::jsonb, 'Time window for request signatures (5 minutes)', false),
    ('federation_rate_limit_requests', '1000'::jsonb, 'Max requests per hour per instance', false),
    ('federation_rate_limit_window_seconds', '3600'::jsonb, 'Rate limit window in seconds', false),
    ('federation_dlq_max_retries', '3'::jsonb, 'Max retries before moving to DLQ', false),
    ('federation_backoff_initial_seconds', '5'::jsonb, 'Initial backoff delay in seconds', false),
    ('federation_backoff_max_seconds', '3600'::jsonb, 'Maximum backoff delay in seconds', false),
    ('federation_enable_abuse_reporting', 'true'::jsonb, 'Enable abuse reporting for federated content', false),
    ('federation_metrics_enabled', 'true'::jsonb, 'Enable federation metrics collection', false)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

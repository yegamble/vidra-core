-- Create abuse reports table for reporting videos, comments, and users
CREATE TABLE IF NOT EXISTS abuse_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    details TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'investigating')),
    moderator_notes TEXT,
    moderated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    moderated_at TIMESTAMP,

    -- Polymorphic reference: can report videos, comments, or users
    reported_entity_type VARCHAR(20) NOT NULL CHECK (reported_entity_type IN ('video', 'comment', 'user', 'channel')),
    reported_video_id UUID REFERENCES videos(id) ON DELETE CASCADE,
    reported_comment_id UUID REFERENCES comments(id) ON DELETE CASCADE,
    reported_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    reported_channel_id UUID REFERENCES channels(id) ON DELETE CASCADE,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Ensure only one entity is reported per report
    CONSTRAINT check_single_entity CHECK (
        (CASE WHEN reported_video_id IS NOT NULL THEN 1 ELSE 0 END +
         CASE WHEN reported_comment_id IS NOT NULL THEN 1 ELSE 0 END +
         CASE WHEN reported_user_id IS NOT NULL THEN 1 ELSE 0 END +
         CASE WHEN reported_channel_id IS NOT NULL THEN 1 ELSE 0 END) = 1
    )
);

-- Create blocklist table for blocking users, domains, or IPs
CREATE TABLE IF NOT EXISTS blocklist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    block_type VARCHAR(20) NOT NULL CHECK (block_type IN ('user', 'domain', 'ip', 'email')),
    blocked_value TEXT NOT NULL,
    reason TEXT,
    blocked_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Ensure uniqueness of active blocks
    CONSTRAINT unique_active_block UNIQUE (block_type, blocked_value, is_active)
);

-- Create instance configuration table for storing instance metadata
CREATE TABLE IF NOT EXISTS instance_config (
    key VARCHAR(255) PRIMARY KEY,
    value JSONB NOT NULL,
    description TEXT,
    is_public BOOLEAN NOT NULL DEFAULT false, -- Whether this config is exposed in public /about endpoint
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Add indexes for performance
CREATE INDEX idx_abuse_reports_status ON abuse_reports(status) WHERE status = 'pending';
CREATE INDEX idx_abuse_reports_reporter ON abuse_reports(reporter_id);
CREATE INDEX idx_abuse_reports_created ON abuse_reports(created_at DESC);
CREATE INDEX idx_abuse_reports_entity ON abuse_reports(reported_entity_type);

CREATE INDEX idx_blocklist_type_value ON blocklist(block_type, blocked_value) WHERE is_active = true;
CREATE INDEX idx_blocklist_expires ON blocklist(expires_at) WHERE expires_at IS NOT NULL AND is_active = true;

CREATE INDEX idx_instance_config_public ON instance_config(key) WHERE is_public = true;

-- Add trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_abuse_reports_updated_at BEFORE UPDATE ON abuse_reports
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_blocklist_updated_at BEFORE UPDATE ON blocklist
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_instance_config_updated_at BEFORE UPDATE ON instance_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert default instance configuration
INSERT INTO instance_config (key, value, description, is_public) VALUES
    ('instance_name', '"Athena"'::jsonb, 'The name of this instance', true),
    ('instance_description', '"A decentralized video platform"'::jsonb, 'Description of this instance', true),
    ('instance_version', '"1.0.0"'::jsonb, 'Current version of the platform', true),
    ('instance_contact_email', '"admin@example.com"'::jsonb, 'Contact email for the instance', true),
    ('instance_terms_url', '""'::jsonb, 'URL to the terms of service', true),
    ('instance_privacy_url', '""'::jsonb, 'URL to the privacy policy', true),
    ('instance_rules', '[]'::jsonb, 'Instance rules as an array of strings', true),
    ('instance_languages', '["en"]'::jsonb, 'Supported languages', true),
    ('instance_categories', '[]'::jsonb, 'Enabled video categories', true),
    ('instance_default_nsfw_policy', '"blur"'::jsonb, 'Default NSFW content policy', true),
    ('signup_enabled', 'true'::jsonb, 'Whether new user registration is enabled', false),
    ('upload_limit_per_user', '52428800'::jsonb, 'Upload limit per user in bytes (50MB default)', false),
    ('video_quota_per_user', '5368709120'::jsonb, 'Video quota per user in bytes (5GB default)', false)
ON CONFLICT (key) DO NOTHING;
-- ATProto social interactions: follows, likes, comments, moderation

-- ATProto actors table
CREATE TABLE IF NOT EXISTS atproto_actors (
    did VARCHAR(256) PRIMARY KEY,
    handle VARCHAR(256) NOT NULL,
    display_name TEXT,
    bio TEXT,
    avatar_url TEXT,
    banner_url TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    indexed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    labels JSONB,
    local_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE(handle)
);

CREATE INDEX idx_atproto_actors_handle ON atproto_actors(handle);
CREATE INDEX idx_atproto_actors_local_user ON atproto_actors(local_user_id);
CREATE INDEX idx_atproto_actors_indexed ON atproto_actors(indexed_at DESC);

-- Follows table
CREATE TABLE IF NOT EXISTS atproto_follows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    follower_did VARCHAR(256) NOT NULL,
    following_did VARCHAR(256) NOT NULL,
    uri VARCHAR(512) NOT NULL UNIQUE,
    cid VARCHAR(128),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMP,
    raw JSONB,
    FOREIGN KEY (follower_did) REFERENCES atproto_actors(did) ON DELETE CASCADE,
    FOREIGN KEY (following_did) REFERENCES atproto_actors(did) ON DELETE CASCADE,
    UNIQUE(follower_did, following_did)
);

CREATE INDEX idx_follows_follower ON atproto_follows(follower_did);
CREATE INDEX idx_follows_following ON atproto_follows(following_did);
CREATE INDEX idx_follows_created ON atproto_follows(created_at DESC);
CREATE INDEX idx_follows_active ON atproto_follows(follower_did, following_did) WHERE revoked_at IS NULL;

-- Likes table
CREATE TABLE IF NOT EXISTS atproto_likes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_did VARCHAR(256) NOT NULL,
    subject_uri VARCHAR(512) NOT NULL,
    subject_cid VARCHAR(128),
    uri VARCHAR(512) NOT NULL UNIQUE,
    cid VARCHAR(128),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    video_id UUID REFERENCES videos(id) ON DELETE CASCADE,
    post_id UUID REFERENCES federated_posts(id) ON DELETE CASCADE,
    raw JSONB,
    FOREIGN KEY (actor_did) REFERENCES atproto_actors(did) ON DELETE CASCADE,
    UNIQUE(actor_did, subject_uri)
);

CREATE INDEX idx_likes_actor ON atproto_likes(actor_did);
CREATE INDEX idx_likes_subject ON atproto_likes(subject_uri);
CREATE INDEX idx_likes_video ON atproto_likes(video_id) WHERE video_id IS NOT NULL;
CREATE INDEX idx_likes_post ON atproto_likes(post_id) WHERE post_id IS NOT NULL;
CREATE INDEX idx_likes_created ON atproto_likes(created_at DESC);

-- Comments table
CREATE TABLE IF NOT EXISTS atproto_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_did VARCHAR(256) NOT NULL,
    actor_handle VARCHAR(256),
    uri VARCHAR(512) NOT NULL UNIQUE,
    cid VARCHAR(128),
    text TEXT NOT NULL,
    parent_uri VARCHAR(512),
    parent_cid VARCHAR(128),
    root_uri VARCHAR(512) NOT NULL,
    root_cid VARCHAR(128),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    indexed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    video_id UUID REFERENCES videos(id) ON DELETE CASCADE,
    post_id UUID REFERENCES federated_posts(id) ON DELETE CASCADE,
    labels JSONB,
    blocked BOOLEAN NOT NULL DEFAULT FALSE,
    raw JSONB,
    FOREIGN KEY (actor_did) REFERENCES atproto_actors(did) ON DELETE CASCADE
);

CREATE INDEX idx_comments_actor ON atproto_comments(actor_did);
CREATE INDEX idx_comments_root ON atproto_comments(root_uri);
CREATE INDEX idx_comments_parent ON atproto_comments(parent_uri) WHERE parent_uri IS NOT NULL;
CREATE INDEX idx_comments_video ON atproto_comments(video_id) WHERE video_id IS NOT NULL;
CREATE INDEX idx_comments_post ON atproto_comments(post_id) WHERE post_id IS NOT NULL;
CREATE INDEX idx_comments_created ON atproto_comments(created_at DESC);
CREATE INDEX idx_comments_visible ON atproto_comments(root_uri, created_at DESC) WHERE blocked = FALSE;

-- Moderation labels table
CREATE TABLE IF NOT EXISTS atproto_moderation_labels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_did VARCHAR(256) NOT NULL,
    label_type VARCHAR(128) NOT NULL,
    reason TEXT,
    applied_by VARCHAR(256) NOT NULL,
    uri VARCHAR(512),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    raw JSONB,
    FOREIGN KEY (actor_did) REFERENCES atproto_actors(did) ON DELETE CASCADE
);

CREATE INDEX idx_mod_labels_actor ON atproto_moderation_labels(actor_did);
CREATE INDEX idx_mod_labels_type ON atproto_moderation_labels(label_type);
CREATE INDEX idx_mod_labels_uri ON atproto_moderation_labels(uri) WHERE uri IS NOT NULL;
CREATE INDEX idx_mod_labels_actor_type ON atproto_moderation_labels(actor_did, label_type);
CREATE INDEX idx_mod_labels_expires ON atproto_moderation_labels(expires_at) WHERE expires_at IS NOT NULL;

-- Social stats materialized view for performance
CREATE MATERIALIZED VIEW IF NOT EXISTS social_stats AS
SELECT
    a.did,
    a.handle,
    COUNT(DISTINCT f1.follower_did) as followers,
    COUNT(DISTINCT f2.following_did) as following,
    COUNT(DISTINCT l.id) as likes_given,
    COUNT(DISTINCT c.id) as comments_made
FROM atproto_actors a
LEFT JOIN atproto_follows f1 ON f1.following_did = a.did AND f1.revoked_at IS NULL
LEFT JOIN atproto_follows f2 ON f2.follower_did = a.did AND f2.revoked_at IS NULL
LEFT JOIN atproto_likes l ON l.actor_did = a.did
LEFT JOIN atproto_comments c ON c.actor_did = a.did AND c.blocked = FALSE
GROUP BY a.did, a.handle;

CREATE UNIQUE INDEX idx_social_stats_did ON social_stats(did);

-- Function to refresh social stats
CREATE OR REPLACE FUNCTION refresh_social_stats()
RETURNS void AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY social_stats;
END;
$$ LANGUAGE plpgsql;

-- Triggers for auto-updating timestamps
CREATE TRIGGER update_atproto_actors_updated_at BEFORE UPDATE ON atproto_actors
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Configuration for social features
INSERT INTO instance_config (key, value, description, is_public)
VALUES
    ('atproto_enable_follows', 'true'::jsonb, 'Enable ATProto follow functionality', false),
    ('atproto_enable_likes', 'true'::jsonb, 'Enable ATProto likes functionality', false),
    ('atproto_enable_comments', 'true'::jsonb, 'Enable ATProto comments functionality', false),
    ('atproto_moderation_labels', '["spam", "impersonation", "harassment"]'::jsonb, 'Moderation label types to apply', false)
ON CONFLICT (key) DO NOTHING;
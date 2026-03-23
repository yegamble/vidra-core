-- +goose Up
-- +goose StatementBegin
-- ActivityPub Support Migration
-- This migration adds tables for ActivityPub federation support

-- Table to store local actor key pairs
CREATE TABLE IF NOT EXISTS ap_actor_keys (
    actor_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    public_key_pem TEXT NOT NULL,
    private_key_pem TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Table to cache remote actors
CREATE TABLE IF NOT EXISTS ap_remote_actors (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_uri TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL, -- Person, Group, Service
    username TEXT NOT NULL,
    domain TEXT NOT NULL,
    display_name TEXT,
    summary TEXT,
    inbox_url TEXT NOT NULL,
    outbox_url TEXT,
    shared_inbox TEXT,
    followers_url TEXT,
    following_url TEXT,
    public_key_id TEXT NOT NULL,
    public_key_pem TEXT NOT NULL,
    icon_url TEXT,
    image_url TEXT,
    last_fetched_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_username_domain UNIQUE(username, domain)
);

CREATE INDEX IF NOT EXISTS idx_ap_remote_actors_actor_uri ON ap_remote_actors(actor_uri);
CREATE INDEX IF NOT EXISTS idx_ap_remote_actors_domain ON ap_remote_actors(domain);
CREATE INDEX IF NOT EXISTS idx_ap_remote_actors_username_domain ON ap_remote_actors(username, domain);

-- Table to store activities (both local and remote)
CREATE TABLE IF NOT EXISTS ap_activities (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    activity_uri TEXT NOT NULL UNIQUE,
    actor_id TEXT NOT NULL, -- Can be local user ID or remote actor URI
    type TEXT NOT NULL, -- Create, Update, Delete, Follow, Like, etc.
    object_id TEXT,
    object_type TEXT, -- Video, Note, Person, etc.
    target_id TEXT,
    published TIMESTAMP NOT NULL,
    activity_json JSONB NOT NULL,
    local BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ap_activities_actor_id ON ap_activities(actor_id);
CREATE INDEX IF NOT EXISTS idx_ap_activities_type ON ap_activities(type);
CREATE INDEX IF NOT EXISTS idx_ap_activities_object_id ON ap_activities(object_id);
CREATE INDEX IF NOT EXISTS idx_ap_activities_published ON ap_activities(published DESC);
CREATE INDEX IF NOT EXISTS idx_ap_activities_local ON ap_activities(local);
CREATE INDEX IF NOT EXISTS idx_ap_activities_actor_local ON ap_activities(actor_id, local);

-- Table to store follower relationships
CREATE TABLE IF NOT EXISTS ap_followers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_id TEXT NOT NULL, -- The actor being followed (local user ID or URI)
    follower_id TEXT NOT NULL, -- The follower (local user ID or remote actor URI)
    state TEXT NOT NULL DEFAULT 'pending', -- pending, accepted, rejected
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_actor_follower UNIQUE(actor_id, follower_id)
);

CREATE INDEX IF NOT EXISTS idx_ap_followers_actor_id ON ap_followers(actor_id);
CREATE INDEX IF NOT EXISTS idx_ap_followers_follower_id ON ap_followers(follower_id);
CREATE INDEX IF NOT EXISTS idx_ap_followers_state ON ap_followers(state);

-- Table for activity delivery queue
CREATE TABLE IF NOT EXISTS ap_delivery_queue (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    activity_id UUID NOT NULL REFERENCES ap_activities(id) ON DELETE CASCADE,
    inbox_url TEXT NOT NULL,
    actor_id TEXT NOT NULL, -- Local actor sending the activity
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    next_attempt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, processing, completed, failed
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ap_delivery_queue_status ON ap_delivery_queue(status);
CREATE INDEX IF NOT EXISTS idx_ap_delivery_queue_next_attempt ON ap_delivery_queue(next_attempt);
CREATE INDEX IF NOT EXISTS idx_ap_delivery_queue_activity_id ON ap_delivery_queue(activity_id);
CREATE INDEX IF NOT EXISTS idx_ap_delivery_queue_inbox_url ON ap_delivery_queue(inbox_url);

-- Table to track received activities (for deduplication)
CREATE TABLE IF NOT EXISTS ap_received_activities (
    activity_uri TEXT PRIMARY KEY,
    received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ap_received_activities_received_at ON ap_received_activities(received_at);

-- Table for storing likes/dislikes from remote actors
CREATE TABLE IF NOT EXISTS ap_video_reactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    actor_uri TEXT NOT NULL,
    reaction_type TEXT NOT NULL, -- like, dislike
    activity_uri TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_video_actor_reaction UNIQUE(video_id, actor_uri, reaction_type)
);

CREATE INDEX IF NOT EXISTS idx_ap_video_reactions_video_id ON ap_video_reactions(video_id);
CREATE INDEX IF NOT EXISTS idx_ap_video_reactions_actor_uri ON ap_video_reactions(actor_uri);

-- Table for storing shares/announces
CREATE TABLE IF NOT EXISTS ap_video_shares (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    actor_uri TEXT NOT NULL,
    activity_uri TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_video_actor_share UNIQUE(video_id, actor_uri)
);

CREATE INDEX IF NOT EXISTS idx_ap_video_shares_video_id ON ap_video_shares(video_id);
CREATE INDEX IF NOT EXISTS idx_ap_video_shares_actor_uri ON ap_video_shares(actor_uri);

-- Add triggers for updated_at
CREATE TRIGGER update_ap_actor_keys_updated_at
    BEFORE UPDATE ON ap_actor_keys
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ap_remote_actors_updated_at
    BEFORE UPDATE ON ap_remote_actors
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ap_followers_updated_at
    BEFORE UPDATE ON ap_followers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ap_delivery_queue_updated_at
    BEFORE UPDATE ON ap_delivery_queue
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

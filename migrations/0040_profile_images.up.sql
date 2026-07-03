-- 0040: profile images. Avatar/banner blobs for user accounts and channels.
-- Dedicated side tables (not columns on users/channels) so the shared sqlc
-- models and their RETURNING lists stay stable (same reasoning as the P10
-- Slice 2a actor-key tables). storage_key is the backend object key, laid out
-- PeerTube-style — one top-level dir per asset kind (avatars/users/<id><ext>,
-- banners/channels/<id><ext>, …; see .ralph/specs/storage-layout.md) — and is
-- opaque to the database. One row per (owner, kind); re-upload replaces it.

CREATE TABLE user_images (
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('avatar', 'banner')),
    storage_key  TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind)
);

CREATE TABLE channel_images (
    channel_id   UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('avatar', 'banner')),
    storage_key  TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, kind)
);

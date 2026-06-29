-- 0019: Named playlists. A playlist is an ordered, named collection of videos
-- owned by a user, with public/unlisted/private visibility. playlist_items hold
-- the membership + ordering (position). Reorder, quick-add, and custom thumbnails
-- are later slices.
CREATE TABLE playlists (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'unlisted', 'private')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Listing a user's playlists newest-first is the hot path.
CREATE INDEX playlists_owner_created_idx ON playlists (owner_id, created_at DESC);

CREATE TABLE playlist_items (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    playlist_id UUID NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    video_id    UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (playlist_id, video_id)
);

-- Listing a playlist's items in order is the hot path.
CREATE INDEX playlist_items_playlist_position_idx ON playlist_items (playlist_id, position);

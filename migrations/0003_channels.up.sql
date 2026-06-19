-- 0003: Channels. A channel is a publishing identity owned by a user; an account
-- may own several. Videos (a later migration) will reference a channel. Kept
-- deliberately small — avatar/banner, follower counts, and federation actor
-- fields land in later migrations as those features are implemented.

CREATE TABLE channels (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    handle        TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Handles are the public, case-insensitive identifier for a channel.
CREATE UNIQUE INDEX channels_handle_lower_idx ON channels (lower(handle));
CREATE INDEX channels_owner_id_idx ON channels (owner_id);

-- Trigram index to support fuzzy channel search.
CREATE INDEX channels_handle_trgm_idx ON channels USING gin (handle gin_trgm_ops);

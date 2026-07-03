-- 0050: Remote videos — metadata-only mirrors of federated videos ingested from
-- inbound Create{Video}/Announce activities (.ralph/specs/federation-remote-content.md §1-2).
-- Vidra never copies or re-hosts the media file: playback uses stream_url from
-- the origin (or links out to watch_url). Deduped/upserted by object_url (the
-- ActivityPub object id). thumbnail_key is a locally cached poster (§5,
-- "remote-thumbnails/<id>.jpg"), best-effort — NULL means no cached preview.
CREATE TABLE remote_videos (
    id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    object_url       TEXT        NOT NULL UNIQUE,
    remote_actor_url TEXT        NOT NULL REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    title            TEXT        NOT NULL DEFAULT '',
    description      TEXT        NOT NULL DEFAULT '',
    duration_seconds INT,
    published_at     TIMESTAMPTZ,
    watch_url        TEXT        NOT NULL DEFAULT '',
    stream_url       TEXT,
    thumbnail_key    TEXT,
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- All of an actor's videos (actor Delete cascade lookups, per-channel listings).
CREATE INDEX remote_videos_actor_idx ON remote_videos (remote_actor_url, published_at DESC);

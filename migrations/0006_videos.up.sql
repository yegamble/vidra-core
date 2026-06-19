-- 0006: videos. A video belongs to a channel and starts life as a draft. This
-- is the metadata row only — files, renditions, thumbnails, and the transcode
-- pipeline land in later migrations. The id doubles as the public identifier.
--
-- privacy: who can see it. state: where it is in the publish lifecycle.

CREATE TABLE videos (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id    UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    privacy       TEXT NOT NULL DEFAULT 'private'
                      CHECK (privacy IN ('public', 'unlisted', 'private')),
    state         TEXT NOT NULL DEFAULT 'draft'
                      CHECK (state IN ('draft', 'processing', 'published', 'failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX videos_channel_id_idx ON videos (channel_id);

-- Supports the public "recently published" feed (a later slice).
CREATE INDEX videos_public_published_idx ON videos (created_at DESC)
    WHERE privacy = 'public' AND state = 'published';

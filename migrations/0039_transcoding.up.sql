-- 0039: HLS transcoding pipeline (P6.3). Three tables:
--
-- transcode_jobs — durable work queue mirroring federation_deliveries (0038):
-- the publish transition enqueues a job; an in-process worker claims due rows,
-- runs the ffmpeg HLS transcode, and marks them done — or reschedules with
-- exponential backoff, dead-lettering (state 'failed') after max attempts.
-- States: pending → running → done | failed (running rows are re-marked pending
-- on retry).
CREATE TABLE transcode_jobs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id        UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    source_key      TEXT        NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending jobs cheaply.
CREATE INDEX transcode_jobs_due_idx
    ON transcode_jobs (next_attempt_at)
    WHERE state = 'pending';

-- At most one live (pending/running) job per video, so a re-upload while a job
-- is queued does not double the work (enqueue is ON CONFLICT DO NOTHING).
CREATE UNIQUE INDEX transcode_jobs_active_video_idx
    ON transcode_jobs (video_id)
    WHERE state IN ('pending', 'running');

-- streaming_playlists — one row per video: the HLS master playlist's storage
-- key and the pipeline outcome. hls_url is exposed on the video detail only
-- when state = 'ready'.
CREATE TABLE streaming_playlists (
    video_id   UUID        PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    master_key TEXT        NOT NULL DEFAULT '',
    state      TEXT        NOT NULL DEFAULT 'pending'
               CHECK (state IN ('pending', 'ready', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- video_renditions — the HLS ladder rungs produced for a video. key_prefix is
-- the storage-key directory holding that rung's variant playlist + segments
-- (streaming-playlists/<video_id>/<height>p). Replaced wholesale when a video
-- is re-transcoded.
CREATE TABLE video_renditions (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id   UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    height     INT         NOT NULL CHECK (height > 0),
    width      INT         NOT NULL CHECK (width > 0),
    key_prefix TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, height)
);

CREATE INDEX video_renditions_video_idx ON video_renditions (video_id);

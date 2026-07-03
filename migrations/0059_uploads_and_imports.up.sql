-- 0059: resumable upload sessions + async URL-import jobs (fix_plan P6.1
-- resumable upload / progress / cancellation / failed-upload cleanup + P2.2
-- "video imports table").
--
-- upload_sessions — a chunked/resumable upload in progress for a video the
-- caller owns. POST /videos/:id/upload-session opens one (validated against
-- quota/UPLOAD_MAX_SIZE/extension up front); the client PUTs fixed-size chunks
-- (chunk_size bytes, last one shorter); POST .../complete assembles them in
-- order through the existing AttachOriginal → Process pipeline. Sessions expire
-- 24h after creation; a sweeper deletes expired/cancelled sessions' chunk blobs
-- (this is the failed-upload cleanup) — orphan draft videos are left alone.
-- Progress is derivable from the received-chunk bookkeeping (Redis not
-- required): GET /uploads/:id returns which chunk indices have landed.
-- States: active → completed | cancelled.
CREATE TABLE upload_sessions (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id     UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    filename     TEXT        NOT NULL,
    total_size   BIGINT      NOT NULL CHECK (total_size > 0),
    chunk_size   INTEGER     NOT NULL CHECK (chunk_size > 0),
    state        TEXT        NOT NULL DEFAULT 'active'
                 CHECK (state IN ('active', 'completed', 'cancelled')),
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The sweeper scans cancelled sessions and any session past its expiry.
CREATE INDEX upload_sessions_sweep_idx
    ON upload_sessions (expires_at);

-- upload_chunks — the received-chunk ledger for a session: one row per chunk
-- index that has landed, with its stored byte size. The chunk bytes themselves
-- live in the blob backend at uploads/<session_id>/<n> (so the S3 backend works
-- unchanged); this table is the resume contract. A re-PUT of the same index is
-- idempotent (upsert).
CREATE TABLE upload_chunks (
    upload_id   UUID        NOT NULL REFERENCES upload_sessions (id) ON DELETE CASCADE,
    n           INTEGER     NOT NULL CHECK (n >= 0),
    size_bytes  BIGINT      NOT NULL CHECK (size_bytes >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, n)
);

-- import_jobs — durable queue for asynchronous URL import (P2.2). POST
-- /videos/:id/import now enqueues a pending row and returns 202 instead of
-- blocking the request on the SSRF-guarded fetch; an in-process worker claims
-- due rows, runs the fetch + AttachOriginal + Process, and marks them done — or
-- reschedules with exponential backoff, dead-lettering (state 'failed') after
-- max attempts. Mirrors transcode_jobs / account_exports. States: pending →
-- running → done | failed. `error` is a SAFE, client-visible reason (never a
-- raw internal error or the attacker-controlled URL).
CREATE TABLE import_jobs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id        UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    url             TEXT        NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    error           TEXT        NOT NULL DEFAULT '',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending jobs cheaply (worker claim scan).
CREATE INDEX import_jobs_due_idx
    ON import_jobs (next_attempt_at)
    WHERE state = 'pending';

-- Single ACTIVE import per video: while one is pending/running, a re-request is
-- a no-op (the insert is ON CONFLICT DO NOTHING against this index) and the
-- endpoint returns the in-flight job.
CREATE UNIQUE INDEX import_jobs_active_video_idx
    ON import_jobs (video_id)
    WHERE state IN ('pending', 'running');

-- Status lookups read a video's latest import job.
CREATE INDEX import_jobs_video_created_idx
    ON import_jobs (video_id, created_at DESC);

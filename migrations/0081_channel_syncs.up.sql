-- 0081: channel auto-sync (backport W2.C4, UPLOAD-13). A channel_sync mirrors an
-- external platform channel's recent uploads into a LOCAL channel the caller
-- owns: a periodic worker lists the remote channel via the sandboxed yt-dlp
-- extractor (--flat-playlist), and for each not-yet-seen entry it creates a draft
-- video in the target channel and enqueues a `ytdlp` import job (migration 0079).
-- Bytes therefore still land ONLY through video.AttachOriginal → video.Process,
-- so the ClamAV scan hook fires before anything is servable — the W2 ingestion
-- invariant holds by construction (no new write path into blob storage).
--
-- OFF by default: the feature runs only when CHANNEL_SYNC_ENABLED=true AND the
-- yt-dlp import resolver is enabled (YTDLP_IMPORT_ENABLED). See
-- .ralph/specs/backport-w2-upload-import.md §5 (yt-dlp sandboxing/residual risk).

-- channel_syncs — one auto-sync binding: a LOCAL channel + the external channel
-- URL to mirror. States: waiting_first_run (never synced) → syncing (a tick is
-- claiming/processing it) → idle (last run succeeded) | failed (last run errored;
-- last_error is a SAFE, client-visible reason, never a raw error or credentials).
-- next_run_at drives the cadence; POST .../sync-now sets it to now() for a manual
-- trigger. UNIQUE (channel_id, external_channel_url) makes a re-create idempotent
-- at the DB level.
CREATE TABLE channel_syncs (
    id                   UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id           UUID        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    user_id              UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    external_channel_url TEXT        NOT NULL,
    state                TEXT        NOT NULL DEFAULT 'waiting_first_run'
                         CHECK (state IN ('waiting_first_run', 'syncing', 'idle', 'failed')),
    last_sync_at         TIMESTAMPTZ,
    last_error           TEXT        NOT NULL DEFAULT '',
    next_run_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, external_channel_url)
);

-- Worker claim scan: find due syncs that are not already running. A failed sync
-- is retryable on the next cadence (its last_error stays visible until a run
-- succeeds), matching the durable-queue retry philosophy of import_jobs.
CREATE INDEX channel_syncs_due_idx
    ON channel_syncs (next_run_at)
    WHERE state IN ('waiting_first_run', 'idle', 'failed');

-- List / per-user cap (CHANNEL_SYNC_MAX_PER_USER) lookups.
CREATE INDEX channel_syncs_user_idx
    ON channel_syncs (user_id);

-- channel_sync_seen — the dedupe ledger: one row per external video id a sync has
-- already handled, so a later tick never re-imports the same upload. Rows cascade
-- away with their sync.
CREATE TABLE channel_sync_seen (
    sync_id     UUID        NOT NULL REFERENCES channel_syncs (id) ON DELETE CASCADE,
    external_id TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (sync_id, external_id)
);

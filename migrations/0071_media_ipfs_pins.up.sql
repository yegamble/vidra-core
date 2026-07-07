-- 0071: hybrid IPFS media mirroring — the pin ledger + durable pin queue
-- (fix_plan P19.1, .ralph/specs/ipfs-media.md). IPFS is a MIRROR SIDECAR, never
-- an authoritative backend (STORAGE_BACKEND=ipfs stays rejected): local/S3 keeps
-- the only authoritative copy, and this table records what the mirror has (or
-- intends to) add+pin to IPFS.
--
-- One row per storage object the mirror tracks. object_key is the authoritative
-- storage.Backend key (opaque, forward-slash) — the universal handle across every
-- media class, so no new column is needed when a class is added. A trailing slash
-- (e.g. streaming-playlists/<id>/) denotes a directory/wrap add (HLS tree). cid is
-- CIDv1 (empty until pinned); car_root is the wrap-directory root CID for
-- multi-file adds (== cid for those; empty for single-file). state drives the
-- worker; the same pending → pinned | failed shape as transcode_jobs/import_jobs,
-- plus unpinning → unpinned for the delete/GC path.
--
-- PRIVACY INVARIANT: a row is only ever enqueued for an ALREADY-PUBLIC object
-- (spec §3 eligibility gate). Nothing private/unlisted/quarantined/DM/export/
-- transient/remote is written here in v1. This schema carries no capability to
-- distinguish "private but encrypted" content — by construction it only mirrors
-- public bytes.
CREATE TABLE media_ipfs_pins (
    object_key      TEXT        PRIMARY KEY,          -- storage.Backend key (e.g. web-videos/<id>.mp4, streaming-playlists/<id>/)
    media_class     TEXT        NOT NULL              -- 'video_original','hls','thumbnail','storyboard','storyboard_vtt','caption','webm','user_avatar','user_banner','channel_avatar','channel_banner','playlist_cover'
                        CHECK (media_class <> ''),
    cid             TEXT        NOT NULL DEFAULT '',   -- CIDv1; '' until pinned
    car_root        TEXT        NOT NULL DEFAULT '',   -- directory/wrap root CID for multi-file (HLS); '' for single-file
    byte_size       BIGINT      NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
    state           TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (state IN ('pending', 'pinned', 'failed', 'unpinning', 'unpinned')),
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',   -- SAFE, client-invisible; never the raw node error verbatim
    -- Provenance for admin views + privacy re-evaluation. Nullable; a class that
    -- has no owning video leaves video_id NULL, an instance-owned object leaves
    -- owner_user_id NULL. SET NULL on delete so a pin-ledger row outlives its
    -- source row long enough for the worker to unpin it.
    video_id        UUID        REFERENCES videos (id) ON DELETE SET NULL,
    owner_user_id   UUID        REFERENCES users (id)  ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Worker claim scan: due, still-actionable rows (mirrors transcode_jobs_due_idx).
CREATE INDEX media_ipfs_pins_due_idx
    ON media_ipfs_pins (next_attempt_at)
    WHERE state IN ('pending', 'unpinning');

-- Admin counts per class/state, and the reconciliation backfill scan.
CREATE INDEX media_ipfs_pins_state_class_idx ON media_ipfs_pins (state, media_class);

-- Privacy re-evaluation & cascade cleanup by video.
CREATE INDEX media_ipfs_pins_video_idx ON media_ipfs_pins (video_id) WHERE video_id IS NOT NULL;

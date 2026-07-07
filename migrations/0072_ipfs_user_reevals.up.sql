-- 0072: durable per-user IPFS mirror re-evaluation queue (fix_plan P19 round-2
-- audit, MAJOR). Toggling users.unlisted (or a deactivate/reactivate) changes the
-- mirror eligibility of EVERY one of an owner's objects — their avatars/banners AND
-- all of their videos' derivatives (original, VP9, HLS, thumbnail, storyboard jpg+
-- vtt, captions). The HTTP handler must NOT run that fan-out synchronously on the
-- request goroutine: enumerating every owned video + image (2+ DB round-trips each,
-- previously first-error-abort) under the request context stranded videos k+1..N of
-- a now-unlisted owner still pinned while the client got 200 OK.
--
-- Instead the handler enqueues ONE compact job here (idempotent upsert, coalescing
-- rapid toggles) and the mirror worker EXPANDS it off the request path with per-video
-- error isolation. The row is committed durably, so a crash immediately after the
-- toggle still converges once the worker resumes; the periodic eligibility sweep
-- (SweepIneligibleIPFSPins) is the final backstop if the enqueue itself is ever lost.
CREATE TABLE media_ipfs_user_reevals (
    user_id         UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Worker claim scan: due jobs (mirrors media_ipfs_pins_due_idx).
CREATE INDEX media_ipfs_user_reevals_due_idx ON media_ipfs_user_reevals (next_attempt_at);

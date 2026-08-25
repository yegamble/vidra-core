-- 0117: a give-up ledger for storyboard generation.
--
-- Storyboards have only ever been generated on two seams: a video finishing
-- Process, and ReplaceSource swapping a new original in. Both are best-effort
-- and both are skipped outright when ffmpeg is absent or the storyboards_enabled
-- overlay is off — and NOTHING has ever retried them. A video published on a box
-- that had no ffmpeg that afternoon has no seek preview today and would never
-- get one. A PeerTube migration widens the same hole from the other side: the
-- source only generates storyboards from 6.0 onward and never for media under
-- three seconds, so a large part of an imported catalogue arrives with no sheet
-- to carry across. This table is what lets a worker walk that backlog.
--
-- Why a ledger at all, when the backfill could just re-scan for videos with no
-- storyboard row?
--
-- Because the failures are not uniform, and the expensive ones repeat. Each
-- attempt is a FULL DECODE of the original — the sprite sheet samples one frame
-- per interval, but ffmpeg still walks the whole file to find them. A source
-- that cannot produce a sheet (a zero-byte original, a container ffmpeg refuses,
-- audio-only media with no measurable duration) would be re-decoded on every
-- tick, for the life of the install, at full CPU cost. That is not hypothetical
-- here: the actor-image import spent five oversized rows' worth of fetches
-- against a live production instance on every single run until its outcomes were
-- split into terminal and retryable ones, and internal/mediahash writes a
-- 'missing' sentinel into the very column it is filling for the same reason.
--
-- A storyboard has no column to put a sentinel in — its absence IS the absence
-- of two video_files rows — so the memory needs somewhere else to live, and this
-- is it. One row per video that has ever failed; no row at all for the videos
-- that succeeded (they are excluded by their storyboard file from then on) or
-- for the ones the backfill has not reached yet.
--
--   attempts        how many times generation has been tried and failed.
--   next_attempt_at when it may be tried again (the backoff).
--   given_up        terminal. The selection query never returns these rows
--                   again, so the CPU is spent once and then never. It is set
--                   either by exhausting the attempt budget or, immediately, by
--                   a failure that is provably permanent — a source with no
--                   measurable duration, which is the sole input to the sprite
--                   layout, so no number of retries can change the answer.
--   last_error      the operator-facing reason, kept for both states. On a
--                   given-up row it is the whole explanation of why that video
--                   will never have a seek preview.
--
-- A row is deleted on success, so the table stays proportional to the problem
-- rather than to the catalogue.
CREATE TABLE video_storyboard_attempts (
    video_id        UUID        PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    given_up        BOOLEAN     NOT NULL DEFAULT false,
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The backfill's scan joins this table to find the videos it may retry NOW.
-- Partial on the live half: a given-up row is never a candidate again, so it
-- does not belong in the index, and on a catalogue whose unfixable videos have
-- all been booked the index shrinks back towards empty.
CREATE INDEX video_storyboard_attempts_due_idx
    ON video_storyboard_attempts (next_attempt_at)
    WHERE NOT given_up;

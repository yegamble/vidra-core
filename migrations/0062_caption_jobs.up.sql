-- 0062: Auto-caption (Whisper) job queue (fix_plan P13). A video owner may
-- request an auto-generated WebVTT caption track; POST /videos/:id/captions/auto
-- enqueues a pending row and returns 202, and an in-process worker (WHISPER_ENABLED)
-- claims due rows, extracts the audio (ffmpeg → 16 kHz mono WAV), POSTs it to the
-- configured Whisper transcription service, renders the response to WebVTT, and
-- upserts the caption through the existing AddCaption path (replace-by-language).
-- Mirrors the import_jobs / transcode_jobs durable-queue pattern: pending →
-- running → done | failed, exponential backoff on retry, dead-letter after max
-- attempts. `language` is the BCP-47-ish caption tag (also the Whisper language
-- hint); `error` is a SAFE, client-visible reason (never a raw internal error or
-- the Whisper endpoint URL). States: pending → running → done | failed.
CREATE TABLE caption_jobs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id        UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    language        TEXT        NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    error           TEXT        NOT NULL DEFAULT '',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending jobs cheaply (worker claim scan).
CREATE INDEX caption_jobs_due_idx
    ON caption_jobs (next_attempt_at)
    WHERE state = 'pending';

-- Single ACTIVE auto-caption job per video: while one is pending/running, a
-- re-request conflicts (the insert is ON CONFLICT DO NOTHING against this index)
-- and the endpoint returns 409.
CREATE UNIQUE INDEX caption_jobs_active_video_idx
    ON caption_jobs (video_id)
    WHERE state IN ('pending', 'running');

-- Status lookups read a video's latest auto-caption job.
CREATE INDEX caption_jobs_video_created_idx
    ON caption_jobs (video_id, created_at DESC);

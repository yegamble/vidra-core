-- 0094: operator video controls and resolution-level transcode visibility.
--
-- A transcode queue row remains the retry/lease source of truth, but each
-- concrete HLS/Web Video rendition now has its own durable operational step.
-- The step trigger projects those rows into job_runs so the executions screen
-- shows honest per-resolution percentages instead of one opaque ladder job.

ALTER TABLE transcode_jobs
    ADD COLUMN transcode_type TEXT NOT NULL DEFAULT 'all'
        CHECK (transcode_type IN ('all', 'hls', 'web_video'));

ALTER TABLE video_renditions
    ADD COLUMN size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);

ALTER TABLE caption_jobs
    ADD COLUMN stage TEXT NOT NULL DEFAULT '',
    ADD COLUMN progress_percent SMALLINT NOT NULL DEFAULT 0
        CHECK (progress_percent BETWEEN 0 AND 100);

CREATE TABLE transcode_steps (
    id                 UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    transcode_job_id   UUID        NOT NULL REFERENCES transcode_jobs (id) ON DELETE CASCADE,
    video_id           UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    format             TEXT        NOT NULL CHECK (format IN ('hls', 'web_video')),
    height             INTEGER     NOT NULL CHECK (height > 0),
    width              INTEGER     NOT NULL CHECK (width > 0),
    state              TEXT        NOT NULL DEFAULT 'queued'
                       CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    stage              TEXT        NOT NULL DEFAULT '',
    progress_percent   SMALLINT    NOT NULL DEFAULT 0
                       CHECK (progress_percent BETWEEN 0 AND 100),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at         TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at        TIMESTAMPTZ,
    UNIQUE (transcode_job_id, format, height)
);

CREATE INDEX transcode_steps_video_created_idx
    ON transcode_steps (video_id, created_at DESC, id DESC);

-- Keep a resolution step and its canonical operational run transactionally in
-- sync. Only allowlisted format/dimension/stage fields enter metadata; storage
-- keys and ffmpeg output never do.
CREATE FUNCTION sync_transcode_step_job_run() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    parent_id UUID;
    event_kind TEXT;
    run_type TEXT;
    display_stage TEXT;
BEGIN
    SELECT id INTO parent_id
    FROM job_runs
    WHERE queue = 'transcode_jobs' AND source_id = NEW.transcode_job_id::text;

    run_type := CASE NEW.format
        WHEN 'hls' THEN 'video_transcode_hls'
        ELSE 'video_transcode_web_video'
    END;
    display_stage := NEW.height::text || 'p' ||
        CASE WHEN NEW.stage <> '' THEN ' · ' || NEW.stage ELSE '' END;

    INSERT INTO job_runs (
        id, parent_job_id, type, queue, source_id, state, stage,
        progress_percent, priority, attempt, max_attempts, resource_type,
        resource_id, input_metadata, output_metadata, error_class, error_code,
        error_detail, error_retryable, created_at, started_at, updated_at,
        finished_at
    ) VALUES (
        NEW.id, parent_id, run_type, 'transcode_steps', NEW.id::text,
        NEW.state, display_stage, NEW.progress_percent, 0, 0, 5, 'video',
        NEW.video_id::text,
        jsonb_build_object('format', NEW.format, 'height', NEW.height, 'width', NEW.width),
        '{}'::jsonb,
        CASE WHEN NEW.state = 'failed' THEN 'execution' ELSE '' END,
        CASE WHEN NEW.state = 'failed' THEN run_type || '_failed' ELSE '' END,
        CASE WHEN NEW.state = 'failed'
             THEN 'Rendition failed; inspect correlated system logs for diagnostic detail.'
             ELSE '' END,
        CASE WHEN NEW.state = 'failed' THEN true ELSE NULL END,
        NEW.created_at, NEW.started_at, NEW.updated_at, NEW.finished_at
    )
    ON CONFLICT (id) DO UPDATE SET
        parent_job_id = COALESCE(job_runs.parent_job_id, EXCLUDED.parent_job_id),
        state = EXCLUDED.state,
        stage = EXCLUDED.stage,
        progress_percent = EXCLUDED.progress_percent,
        input_metadata = EXCLUDED.input_metadata,
        error_class = EXCLUDED.error_class,
        error_code = EXCLUDED.error_code,
        error_detail = EXCLUDED.error_detail,
        error_retryable = EXCLUDED.error_retryable,
        started_at = COALESCE(job_runs.started_at, EXCLUDED.started_at),
        updated_at = EXCLUDED.updated_at,
        finished_at = EXCLUDED.finished_at;

    IF TG_OP = 'INSERT' THEN
        event_kind := 'enqueued';
    ELSIF OLD.state IS DISTINCT FROM NEW.state THEN
        event_kind := CASE NEW.state
            WHEN 'running' THEN 'started'
            WHEN 'succeeded' THEN 'succeeded'
            WHEN 'failed' THEN 'failed'
            ELSE 'queued'
        END;
    ELSIF OLD.stage IS DISTINCT FROM NEW.stage THEN
        event_kind := 'stage_changed';
    ELSIF OLD.progress_percent IS DISTINCT FROM NEW.progress_percent THEN
        event_kind := 'progress';
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO job_events (
        job_id, kind, state, stage, progress_percent, attempt, message,
        metadata, occurred_at
    ) VALUES (
        NEW.id, event_kind, NEW.state, display_stage, NEW.progress_percent, 0,
        CASE WHEN NEW.state = 'failed'
             THEN 'Rendition failed; inspect correlated system logs for diagnostic detail.'
             ELSE '' END,
        jsonb_build_object('format', NEW.format, 'height', NEW.height, 'width', NEW.width),
        NEW.updated_at
    );

    RETURN NEW;
END;
$$;

CREATE TRIGGER transcode_steps_operational_projection
AFTER INSERT OR UPDATE ON transcode_steps
FOR EACH ROW EXECUTE FUNCTION sync_transcode_step_job_run();

-- The legacy caption trigger owns state transitions. This second projection
-- enriches that same run with safe stage/percentage changes between endpoints.
CREATE FUNCTION sync_caption_job_progress() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    run_id UUID;
BEGIN
    SELECT id INTO run_id
    FROM job_runs
    WHERE queue = 'caption_jobs' AND source_id = NEW.id::text;

    UPDATE job_runs
    SET stage = NEW.stage,
        progress_percent = NEW.progress_percent,
        updated_at = NEW.updated_at
    WHERE id = run_id;

    IF TG_OP = 'UPDATE' AND
       (OLD.stage IS DISTINCT FROM NEW.stage OR
        OLD.progress_percent IS DISTINCT FROM NEW.progress_percent) THEN
        INSERT INTO job_events (
            job_id, kind, state, stage, progress_percent, attempt, metadata,
            occurred_at
        ) VALUES (
            run_id,
            CASE WHEN OLD.stage IS DISTINCT FROM NEW.stage
                 THEN 'stage_changed' ELSE 'progress' END,
            CASE NEW.state
                WHEN 'pending' THEN CASE WHEN NEW.attempts > 0 THEN 'retry_scheduled' ELSE 'queued' END
                WHEN 'running' THEN 'running'
                WHEN 'done' THEN 'succeeded'
                ELSE 'dead_lettered'
            END,
            NEW.stage, NEW.progress_percent, NEW.attempts, '{}'::jsonb,
            NEW.updated_at
        );
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER caption_jobs_progress_projection
AFTER INSERT OR UPDATE ON caption_jobs
FOR EACH ROW EXECUTE FUNCTION sync_caption_job_progress();

-- 0120: asynchronous upload-session completion.
--
-- POST /api/v1/uploads/{upload_id}/complete used to do all of this INSIDE the
-- request: stream every 8 MiB chunk back down from object storage, re-upload the
-- assembled file while hashing it (video.AttachOriginal → storage.PutSizedHashed),
-- then ffprobe it and decode it again for the thumbnail and storyboard
-- (video.Process). On a deployment whose object store is across the internet
-- (Backblaze B2) that is minutes of work behind a route carrying the general 30s
-- request deadline — and behind a CDN that caps origin response time at around
-- 100s, so no deadline increase could have made it reliable either. The upload
-- bar reached 100% and completion then 5xx'd.
--
-- Completion is now an ENQUEUE. The request keeps only the cheap validation
-- (ownership, session state, every chunk present at its required size, quota) and
-- answers 202; this queue carries the assembly and the pipeline. It mirrors
-- import_jobs / transcode_jobs exactly — pending → running → done | failed,
-- lease-on-claim with SKIP LOCKED, exponential backoff, dead-letter after max
-- attempts — because those are the semantics the sweep (internal/jobrecovery) and
-- the operational projection already understand.

-- The session state machine grows the two transient states completion now passes
-- through, and the terminal failure the worker can reach:
--   active     — chunks still arriving (unchanged)
--   queued     — complete accepted, finalize job enqueued
--   processing — a worker claimed the job and is assembling/probing
--   completed  — the pipeline finished (the video row carries the outcome)
--   failed     — the finalize job dead-lettered; failure_reason says why
--   cancelled  — the client cancelled (unchanged)
-- Widening the CHECK is the drop/re-add idiom migrate-lint allows: every value
-- the previous release writes stays legal.
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_state_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_state_check
    CHECK (state IN ('active', 'queued', 'processing', 'completed', 'failed', 'cancelled'));

-- failure_reason is a SAFE, client-visible sentence (never a raw internal error,
-- storage key, or process output) explaining a 'failed' session, surfaced on
-- GET /api/v1/uploads/{upload_id} so the poller can say something true. Empty
-- for every other state.
ALTER TABLE upload_sessions
    ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE upload_finalize_jobs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    upload_id       UUID        NOT NULL REFERENCES upload_sessions (id) ON DELETE CASCADE,
    video_id        UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    -- Mirrors upload_sessions.purpose: 'upload' runs AttachOriginal → Process,
    -- 'replace' runs ReplaceSource + the re-transcode orchestration.
    purpose         TEXT        NOT NULL DEFAULT 'upload'
                    CHECK (purpose IN ('upload', 'replace')),
    -- The authorisation decision made at request time by the authenticated
    -- caller (staff or an editor collaborator replacing a video they do not
    -- own). It is carried rather than re-derived because the worker has no
    -- principal: authorisation belongs to the request, not to the queue.
    can_manage      BOOLEAN     NOT NULL DEFAULT false,
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    error           TEXT        NOT NULL DEFAULT '',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending jobs cheaply (worker claim scan), and the elapsed
-- leases the sweep returns to the queue.
CREATE INDEX upload_finalize_jobs_due_idx
    ON upload_finalize_jobs (next_attempt_at)
    WHERE state = 'pending';
CREATE INDEX upload_finalize_jobs_lease_idx
    ON upload_finalize_jobs (next_attempt_at)
    WHERE state = 'running';

-- Single ACTIVE finalize per session: re-POSTing complete while one is
-- pending/running is a no-op insert (ON CONFLICT DO NOTHING against this index)
-- and the endpoint answers 202 with the session's current state. That is the
-- whole of the idempotency guarantee — a client retrying a completion it never
-- saw the response to must not queue the pipeline twice.
CREATE UNIQUE INDEX upload_finalize_jobs_active_upload_idx
    ON upload_finalize_jobs (upload_id)
    WHERE state IN ('pending', 'running');

-- Status lookups read a session's latest finalize job.
CREATE INDEX upload_finalize_jobs_upload_created_idx
    ON upload_finalize_jobs (upload_id, created_at DESC);

-- Operational projection (0083). The shared sync_legacy_job_run() raises on an
-- unknown table, and 0107 established the pattern for a queue that arrived after
-- it: give the new table its own small trigger function rather than rewriting
-- the shared one. 0083 deliberately left upload SESSIONS unprojected because they
-- "need distinct execution rows before they can be represented honestly" — this
-- table IS that execution row, so the projection is now honest.
CREATE FUNCTION sync_upload_finalize_job_run() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    canonical_state    TEXT;
    canonical_progress SMALLINT;
    run_started_at     TIMESTAMPTZ;
    run_finished_at    TIMESTAMPTZ;
    safe_error_class   TEXT := '';
    safe_error_code    TEXT := '';
    safe_error_detail  TEXT := '';
    safe_retryable     BOOLEAN;
    event_kind         TEXT;
    canonical_job_id   UUID;
BEGIN
    CASE NEW.state
        WHEN 'pending' THEN
            canonical_state := CASE WHEN NEW.attempts > 0 THEN 'retry_scheduled' ELSE 'queued' END;
        WHEN 'running' THEN canonical_state := 'running';
        WHEN 'done'    THEN canonical_state := 'succeeded';
        WHEN 'failed'  THEN canonical_state := 'dead_lettered';
        ELSE canonical_state := 'failed';
    END CASE;

    IF canonical_state IN ('queued', 'retry_scheduled') THEN
        canonical_progress := 0;
    ELSIF canonical_state = 'succeeded' THEN
        canonical_progress := 100;
    END IF;

    IF canonical_state = 'running' THEN
        run_started_at := NEW.updated_at;
    END IF;
    IF canonical_state IN ('succeeded', 'dead_lettered') THEN
        run_finished_at := NEW.updated_at;
    END IF;

    -- The stored `error` is already a safe, client-visible sentence (the worker
    -- never records a raw internal error there), but the projection keeps the
    -- same generic wording every other queue uses so an operator reads one
    -- vocabulary across the whole board.
    IF canonical_state = 'dead_lettered' THEN
        safe_error_class := 'execution';
        safe_error_code := 'upload_finalize_failed';
        safe_error_detail := 'Execution failed; inspect correlated system logs for diagnostic detail.';
        safe_retryable := false;
    ELSIF canonical_state = 'retry_scheduled' THEN
        safe_error_class := 'execution';
        safe_error_code := 'retry_scheduled';
        safe_error_detail := 'A bounded retry was scheduled after an execution failure.';
        safe_retryable := true;
    END IF;

    INSERT INTO job_runs (
        type, queue, source_id, state, stage, progress_percent, priority, attempt,
        resource_type, resource_id, input_metadata, output_metadata,
        error_class, error_code, error_detail, error_retryable,
        created_at, started_at, updated_at, finished_at
    ) VALUES (
        'upload_finalize', 'upload_finalize_jobs', NEW.id::text, canonical_state,
        '', canonical_progress, 0, NEW.attempts, 'video', NEW.video_id::text,
        '{}'::jsonb, '{}'::jsonb,
        safe_error_class, safe_error_code, safe_error_detail, safe_retryable,
        NEW.created_at, run_started_at, NEW.updated_at, run_finished_at
    )
    ON CONFLICT (queue, source_id) WHERE source_id <> '' DO UPDATE SET
        state = EXCLUDED.state,
        progress_percent = EXCLUDED.progress_percent,
        attempt = EXCLUDED.attempt,
        resource_type = EXCLUDED.resource_type,
        resource_id = EXCLUDED.resource_id,
        error_class = EXCLUDED.error_class,
        error_code = EXCLUDED.error_code,
        error_detail = EXCLUDED.error_detail,
        error_retryable = EXCLUDED.error_retryable,
        started_at = COALESCE(job_runs.started_at, EXCLUDED.started_at),
        updated_at = EXCLUDED.updated_at,
        finished_at = EXCLUDED.finished_at
    RETURNING id INTO canonical_job_id;

    IF TG_OP = 'INSERT' THEN
        event_kind := 'enqueued';
    ELSIF OLD.state IS DISTINCT FROM NEW.state THEN
        event_kind := CASE canonical_state
            WHEN 'queued'          THEN 'queued'
            WHEN 'retry_scheduled' THEN 'retry_scheduled'
            WHEN 'running'         THEN 'started'
            WHEN 'succeeded'       THEN 'succeeded'
            WHEN 'dead_lettered'   THEN 'dead_lettered'
            ELSE 'failed'
        END;
    ELSIF OLD.attempts IS DISTINCT FROM NEW.attempts THEN
        event_kind := 'attempt_changed';
    ELSE
        -- A lease renewal touches updated_at every thirty seconds and changes
        -- nothing an operator watches; it must not manufacture an event.
        RETURN NEW;
    END IF;

    INSERT INTO job_events (
        job_id, kind, state, stage, progress_percent, attempt, message, metadata, occurred_at
    ) VALUES (
        canonical_job_id, event_kind, canonical_state, '', canonical_progress,
        NEW.attempts, safe_error_detail, '{}'::jsonb, NEW.updated_at
    );

    RETURN NEW;
END;
$$;

CREATE TRIGGER upload_finalize_jobs_operational_projection
AFTER INSERT OR UPDATE ON upload_finalize_jobs
FOR EACH ROW EXECUTE FUNCTION sync_upload_finalize_job_run();

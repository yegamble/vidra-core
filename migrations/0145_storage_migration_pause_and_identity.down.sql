-- Revert 0145. The two new states must be gone from every row before the CHECK
-- narrows again, so a live paused or aborting campaign is put back into the
-- state its resume_state names (or 'copying' when it names nothing, which is
-- the state a campaign with work left in its ledger belongs in). Nothing is
-- destroyed: the ledger is untouched and the next sweep re-derives everything
-- from it.
UPDATE storage_migrations
   SET state = CASE WHEN resume_state <> '' THEN resume_state ELSE 'copying' END
 WHERE state = 'paused';
UPDATE storage_migrations
   SET state = 'cancelled'
 WHERE state = 'aborting';

DROP TRIGGER IF EXISTS storage_migrations_run_identity ON storage_migrations;
DROP FUNCTION IF EXISTS storage_migration_run_identity();

ALTER TABLE storage_migrations
    DROP CONSTRAINT storage_migrations_state_check,
    ADD CONSTRAINT storage_migrations_state_check
        CHECK (state IN ('enumerating', 'copying', 'synced', 'cutover',
                         'deleting_source', 'done', 'cancelled', 'failed'));

-- 0107's projection, restored verbatim.
CREATE OR REPLACE FUNCTION sync_storage_migration_job_run() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    canonical_state TEXT;
    canonical_stage TEXT;
    canonical_progress SMALLINT;
    event_kind TEXT;
    run_started_at TIMESTAMPTZ;
    run_finished_at TIMESTAMPTZ;
    safe_error_class TEXT := '';
    safe_error_code TEXT := '';
    safe_error_detail TEXT := '';
    safe_retryable BOOLEAN;
BEGIN
    -- The campaign's own vocabulary is richer than the run states, so the phase
    -- rides in `stage` and every working phase maps to 'running'. An operator
    -- reading the executions list sees one row moving through named phases.
    CASE NEW.state
        WHEN 'enumerating'     THEN canonical_state := 'running';
        WHEN 'copying'         THEN canonical_state := 'running';
        WHEN 'synced'          THEN canonical_state := 'running';
        WHEN 'cutover'         THEN canonical_state := 'running';
        WHEN 'deleting_source' THEN canonical_state := 'running';
        WHEN 'done'            THEN canonical_state := 'succeeded';
        WHEN 'cancelled'       THEN canonical_state := 'cancelled';
        ELSE                        canonical_state := 'failed';
    END CASE;
    canonical_stage := NEW.state;

    IF NEW.state = 'done' THEN
        canonical_progress := 100;
    ELSE
        canonical_progress := LEAST(100, (NEW.objects_done * 100 / GREATEST(NEW.objects_total, 1)))::smallint;
    END IF;

    run_started_at := NEW.created_at;
    IF NEW.state IN ('done', 'cancelled', 'failed') THEN
        run_finished_at := NEW.updated_at;
    END IF;

    IF NEW.state = 'failed' THEN
        safe_error_class := 'execution';
        safe_error_code := 'storage_migration_failed';
        safe_error_detail := 'The storage migration stopped; inspect correlated system logs for diagnostic detail.';
        safe_retryable := false;
    END IF;

    INSERT INTO job_runs (
        type, queue, source_id, state, stage, progress_percent, priority, attempt,
        resource_type, resource_id, input_metadata, output_metadata,
        error_class, error_code, error_detail, error_retryable,
        created_at, started_at, updated_at, finished_at
    ) VALUES (
        'storage_migration', 'storage_migrations', NEW.id::text, canonical_state,
        canonical_stage, canonical_progress, 0, 0, 'storage_migration', NEW.id::text,
        '{}'::jsonb, '{}'::jsonb,
        safe_error_class, safe_error_code, safe_error_detail, safe_retryable,
        NEW.created_at, run_started_at, NEW.updated_at, run_finished_at
    )
    ON CONFLICT (queue, source_id) WHERE source_id <> '' DO UPDATE SET
        state = EXCLUDED.state,
        stage = EXCLUDED.stage,
        progress_percent = EXCLUDED.progress_percent,
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
        event_kind := CASE canonical_state
            WHEN 'succeeded' THEN 'succeeded'
            WHEN 'cancelled' THEN 'cancelled'
            WHEN 'failed'    THEN 'failed'
            ELSE 'stage_changed'
        END;
    ELSIF OLD.objects_done IS DISTINCT FROM NEW.objects_done
       OR OLD.objects_total IS DISTINCT FROM NEW.objects_total THEN
        event_kind := 'progress';
    ELSE
        -- A touch that changed nothing an operator watches (a counter refresh
        -- that found the same numbers) must not manufacture an event: the copy
        -- worker ticks every ten seconds and would otherwise flood the stream.
        RETURN NEW;
    END IF;

    INSERT INTO job_events (
        job_id, kind, state, stage, progress_percent, attempt, message, metadata, occurred_at
    )
    SELECT r.id, event_kind, canonical_state, canonical_stage, canonical_progress, 0,
           safe_error_detail, '{}'::jsonb, NEW.updated_at
    FROM job_runs r
    WHERE r.queue = 'storage_migrations' AND r.source_id = NEW.id::text;

    RETURN NEW;
END;
$$;

ALTER TABLE storage_migrations
    DROP COLUMN IF EXISTS worker_id,
    DROP COLUMN IF EXISTS correlation_id,
    DROP COLUMN IF EXISTS request_id,
    DROP COLUMN IF EXISTS resume_state,
    DROP COLUMN IF EXISTS paused_reason;

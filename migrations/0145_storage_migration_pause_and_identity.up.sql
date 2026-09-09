-- 0145: a storage migration can be PAUSED, can clean up after itself, and
-- remembers which request started it.
--
-- Three findings from A34, one migration, because they are three columns on one
-- table.
--
-- PAUSE. A migration TARGET the process cannot write to was a fatal boot
-- refusal: booting with a read-only credential produced `fatal error="storage
-- migration target: … Access Denied. [write_denied]"` and the process exited
-- BEFORE the listener opened, for the api as well as the workers. So an
-- operator who rotates or narrows the target bucket's credential loses the whole
-- instance — every video, every page, and the admin console they would fix it
-- from — over a store the instance needs only in order to FINISH A MOVE. The
-- ruling: degrade, do not refuse. The campaign parks in 'paused', the storage
-- component degrades with the class, reads keep being served by whichever store
-- holds authority, and the campaign resumes when the write probe next succeeds.
-- Only the AUTHORITATIVE store being unwritable stays fatal, because an instance
-- that cannot write to the store it serves from is a different fact.
--
-- ABORT-WITH-CLEANUP. A cancelled campaign left its partial copies on the
-- destination forever and nothing in the product removed them; emptying the
-- bucket was the operator's job, on a store they may not have console access to.
-- 'aborting' is the state that removes them, batch by batch, through the same
-- leader-gated sweep that deletes the source at the other end of a successful
-- move — and for the same reason: it is destructive, so exactly one process may
-- do it and every batch must be interruptible.
--
-- IDENTITY. storage_migrations IS projected into job_runs (the 0107 trigger), so
-- A17's "does not call the job recorder yet" list is stale for this queue — but
-- the projected rows carry EMPTY request_id/correlation_id/worker_id, so a
-- campaign cannot be tied back to the admin request that started it. Same
-- columns, same second-trigger idiom, and the same reasoning as 0139: they live
-- on the QUEUE ROW rather than only on the projection, because the queue row is
-- what survives.

ALTER TABLE storage_migrations
    -- Why the campaign is paused. A short FIXED vocabulary, because this column
    -- is rendered to an operator and projected into job_runs.stage's neighbours:
    --   target_write_denied  the migration target refused a write probe
    --   operator             an admin pressed pause
    -- '' whenever the campaign is not paused.
    ADD COLUMN paused_reason  TEXT NOT NULL DEFAULT ''
        CHECK (paused_reason IN ('', 'target_write_denied', 'operator')),
    -- The state to return to on resume. A pause must be reversible without
    -- guessing: a campaign paused out of 'synced' is ready to cut over and one
    -- paused out of 'copying' is not, and resuming both into 'copying' would
    -- silently re-open a finished copy phase.
    ADD COLUMN resume_state   TEXT NOT NULL DEFAULT ''
        CHECK (resume_state IN ('', 'enumerating', 'copying', 'synced')),
    ADD COLUMN request_id     TEXT NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '' CHECK (length(correlation_id) <= 128),
    ADD COLUMN worker_id      TEXT NOT NULL DEFAULT '' CHECK (length(worker_id) <= 255);

-- Widen the state vocabulary. The drop-then-re-add of the SAME constraint name
-- is the one destructive-DDL idiom scripts/migrate-lint.sh allows, and it is
-- allowed precisely because widening a CHECK cannot invalidate a row that
-- already satisfied the narrower one.
ALTER TABLE storage_migrations
    DROP CONSTRAINT storage_migrations_state_check,
    ADD CONSTRAINT storage_migrations_state_check
        CHECK (state IN ('enumerating', 'copying', 'synced', 'paused', 'aborting',
                         'cutover', 'deleting_source', 'done', 'cancelled', 'failed'));

-- NOTE ON THE SINGLE-ACTIVE INDEX, which is deliberately NOT touched.
-- storage_migrations_single_active_idx is `WHERE state NOT IN ('done',
-- 'cancelled', 'failed')`, so both new states count as LIVE and still block a
-- second campaign. That is the answer both of them want: a paused move has not
-- finished and starting another would race it over the same objects, and an
-- aborting one is still deleting from a store.

-- Reproject the campaign with the two new phases.
--
-- 'paused' maps to job_runs 'retry_scheduled' rather than 'queued': the run is
-- not waiting to start, it started and will continue — automatically when the
-- write probe recovers, or when an operator resumes it. 'aborting' maps to
-- 'cancel_requested', which is exactly what that state means: the cancel has
-- been accepted and the work of cancelling is still running.
--
-- Everything else in this function is byte-identical to 0107's. It is repeated
-- in full rather than patched because CREATE OR REPLACE FUNCTION takes a whole
-- body, and a half-copy is how two definitions drift.
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
    CASE NEW.state
        WHEN 'enumerating'     THEN canonical_state := 'running';
        WHEN 'copying'         THEN canonical_state := 'running';
        WHEN 'synced'          THEN canonical_state := 'running';
        WHEN 'paused'          THEN canonical_state := 'retry_scheduled';
        WHEN 'aborting'        THEN canonical_state := 'cancel_requested';
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

-- Carry the campaign's identity onto the job_runs row the projection above
-- creates. A SECOND trigger rather than four more assignments inside that
-- function, exactly as 0139 did for federation_deliveries: row triggers fire in
-- alphabetical order by trigger NAME, and "storage_migrations_operational_
-- projection" sorts before "storage_migrations_run_identity", so the run exists
-- by the time this runs. The guard on the EXISTING value makes it idempotent and
-- keeps a later empty-identity update from erasing an id the start recorded --
-- which matters here more than it did there, because a campaign is written by
-- the admin request that starts it and then by a WORKER on every sweep.
CREATE OR REPLACE FUNCTION storage_migration_run_identity() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.request_id = '' AND NEW.correlation_id = '' AND NEW.worker_id = '' THEN
        RETURN NEW;
    END IF;
    UPDATE job_runs
       SET request_id     = CASE WHEN job_runs.request_id = ''     THEN NEW.request_id     ELSE job_runs.request_id END,
           correlation_id = CASE WHEN job_runs.correlation_id = '' THEN NEW.correlation_id ELSE job_runs.correlation_id END,
           worker_id      = CASE WHEN job_runs.worker_id = ''      THEN NEW.worker_id      ELSE job_runs.worker_id END
     WHERE job_runs.queue = 'storage_migrations'
       AND job_runs.source_id = NEW.id::text;
    RETURN NEW;
END;
$$;

CREATE TRIGGER storage_migrations_run_identity
AFTER INSERT OR UPDATE ON storage_migrations
FOR EACH ROW EXECUTE FUNCTION storage_migration_run_identity();

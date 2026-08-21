-- 0107: storage migration campaigns + the per-object location record
-- (phase-2 storage, work items 4 and 5).
--
-- Moving a media library between backends -- local disk to S3, bucket to bucket
-- -- is a long-running, resumable, integrity-verified job, not a one-shot copy.
-- Two tables carry it:
--
--   storage_migrations         one CAMPAIGN: which store to which store, what
--                              state the move is in, and the counters an
--                              operator watches.
--   storage_migration_objects  one row per OBJECT: the per-object location
--                              record of docs/productionization/interfaces.md
--                              section 3. 'verified' and 'source_deleted' are
--                              the states that mean "this object is in the
--                              target", which is what dual-read and the
--                              delete-source step both key on.
--
-- OBJECT KEYS STAY STABLE. A backend-to-backend move never renames anything, so
-- media_ipfs_pins.object_key (a primary key over storage keys since 0071) and
-- every video_files.storage_key keep pointing at the right bytes and need no
-- migration of their own. That is the whole reason the opaque-relative-key
-- doctrine from 0008 exists.
--
-- The campaign is the queue card. A trigger projects storage_migrations into
-- job_runs/job_events (queue 'storage_migrations') exactly like 0094 does for
-- transcode_steps, so the admin jobs browser shows progress with no new HTTP
-- surface. The per-object rows deliberately do NOT get their own runs: a
-- 40,000-object library would bury every other queue in the executions list.

CREATE TABLE storage_migrations (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Human-readable backend IDENTITY strings ("s3://<endpoint>/<bucket>" or
    -- "local:<abs path>"), never credentials. They are the campaign's memory of
    -- which store was which, and the process compares them against the identity
    -- of the backends it actually holds before it copies or deletes anything --
    -- the guard that makes an env swap detectable instead of catastrophic.
    source_desc     TEXT        NOT NULL CHECK (source_desc <> ''),
    target_desc     TEXT        NOT NULL CHECK (target_desc <> ''),
    state           TEXT        NOT NULL DEFAULT 'enumerating'
                        CHECK (state IN ('enumerating', 'copying', 'synced', 'cutover',
                                         'deleting_source', 'done', 'cancelled', 'failed')),
    objects_total   BIGINT      NOT NULL DEFAULT 0 CHECK (objects_total >= 0),
    objects_done    BIGINT      NOT NULL DEFAULT 0 CHECK (objects_done >= 0),
    objects_failed  BIGINT      NOT NULL DEFAULT 0 CHECK (objects_failed >= 0),
    -- SAFE, operator-facing category text. Never a raw backend error (those name
    -- endpoints, buckets and object keys); 0083's secret-free error discipline.
    last_error      TEXT        NOT NULL DEFAULT '',
    -- When the process FIRST saw the primary backend serving from target_desc,
    -- i.e. when the operator's env swap took effect. The grace period before the
    -- source is deleted is measured from here, not from when copying finished:
    -- the point of the grace is "the new store has been live and serving for
    -- STORAGE_MIGRATION_GRACE_HOURS", which only starts at cutover.
    observed_cutover_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most ONE live campaign. Two concurrent moves would race over the same
-- objects and could not agree on which store is authoritative; a terminal
-- campaign is history and never blocks the next one.
CREATE UNIQUE INDEX storage_migrations_single_active_idx
    ON storage_migrations ((true))
    WHERE state NOT IN ('done', 'cancelled', 'failed');

CREATE INDEX storage_migrations_created_idx ON storage_migrations (created_at DESC);

CREATE TABLE storage_migration_objects (
    -- The storage.Backend key, unchanged by the move. Primary key because an
    -- object is in exactly one campaign's ledger at a time and the key is what
    -- every other subsystem already uses as its handle (0071's doctrine).
    object_key      TEXT        PRIMARY KEY CHECK (object_key <> ''),
    campaign_id     UUID        NOT NULL REFERENCES storage_migrations (id) ON DELETE CASCADE,
    -- pending      -> not yet copied
    -- copying      -> claimed by a worker; next_attempt_at is its LEASE
    -- verified     -> in the target, re-read and re-hashed there
    -- failed       -> terminal; last_error says which category
    -- source_deleted -> verified AND the source copy has been removed
    state           TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (state IN ('pending', 'copying', 'verified', 'failed', 'source_deleted')),
    -- Lowercase hex SHA-256 of the bytes as they were READ BACK from the target,
    -- not merely as they were sent. '' until verified.
    sha256          TEXT        NOT NULL DEFAULT '',
    byte_size       BIGINT      NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
    attempts        INT         NOT NULL DEFAULT 0,
    -- Doubles as the claim LEASE while state='copying' (transcode_jobs pattern):
    -- the worker pushes it forward on claim and renews it while it copies, so a
    -- dead worker's row returns to 'pending' via the jobrecovery sweep.
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- SAFE category text ONLY. It is projected into the admin jobs overview,
    -- which must never carry a storage key.
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Worker claim scan: due, still-actionable rows (mirrors media_ipfs_pins_due_idx).
CREATE INDEX storage_migration_objects_due_idx
    ON storage_migration_objects (next_attempt_at, object_key)
    WHERE state IN ('pending');

-- Lease sweep + per-campaign counters/breakdown.
CREATE INDEX storage_migration_objects_campaign_state_idx
    ON storage_migration_objects (campaign_id, state);

-- Project a campaign into the canonical operational run so the admin jobs
-- browser and its SSE stream show it with no new HTTP surface. Same shape as
-- 0094's sync_transcode_step_job_run: upsert the run, then append an event only
-- when something an operator would care about actually changed.
--
-- progress_percent is honest by construction -- objects_done over objects_total,
-- both of which are recomputed from the object ledger rather than incremented
-- optimistically -- so it can never claim progress the copy did not make.
CREATE FUNCTION sync_storage_migration_job_run() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE TRIGGER storage_migrations_operational_projection
AFTER INSERT OR UPDATE ON storage_migrations
FOR EACH ROW EXECUTE FUNCTION sync_storage_migration_job_run();

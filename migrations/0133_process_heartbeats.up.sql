-- 0133: per-process heartbeats, and identity inheritance for job events.
--
-- TWO GAPS, ONE SENTENCE: the operator cannot see the worker.
--
-- (1) /admin/system reports the health of the process that served the request
-- and nothing else. On a split topology (VIDRA_ROLE=api + VIDRA_ROLE=worker)
-- the settings_sync component is an in-memory read of THIS process's poller, so
-- a worker whose every settings poll fails — or a worker that is not running at
-- all — leaves the page reading "ok" with seven healthy components. The worker
-- is the half that transcodes, imports and sweeps while consulting the same
-- instance-settings overlay, so a frozen or absent worker is precisely the
-- outage an operator must be able to see. process_heartbeats is the shared
-- fact: one row per running process, upserted by the settings poller (which
-- already runs in EVERY role and already keeps the health record this reads),
-- so the api can answer for the fleet instead of for itself.
--
-- A heartbeat can only report a process that is alive enough to write one. A
-- process killed with SIGKILL shows up as ABSENCE (a stale last_seen_at), never
-- as a self-reported failure. That is why staleness, not a status column, is the
-- primary signal.
--
-- (2) job_events (migration 0083) carries request_id, correlation_id, trace_id
-- and worker_id and nothing has ever written them, so the run detail's own
-- advice — "inspect correlated system logs for diagnostic detail" — points at
-- logs that share no id with the run. The columns are filled on job_runs by the
-- enqueue and claim paths (see internal/store/queries/job_runs.sql); this
-- migration makes every EVENT inherit them from its parent run, so a retry or a
-- dead-letter written by the 0083/0094/0107/0120 projection triggers carries the
-- originating request's ids without any of those triggers being rewritten.

CREATE TABLE process_heartbeats (
    -- hostname:pid. Stable for the life of a process and BOUNDED: a restarted
    -- container or pod reuses the same key (pid 1, same pod name) and upserts
    -- over its own row rather than growing a new one every deploy. started_at
    -- distinguishes one run from the next.
    process_id                    TEXT        PRIMARY KEY
                                  CHECK (process_id <> '' AND length(process_id) <= 255),
    role                          TEXT        NOT NULL
                                  CHECK (role IN ('all', 'api', 'worker')),
    hostname                      TEXT        NOT NULL DEFAULT '' CHECK (length(hostname) <= 255),
    pid                           INTEGER     NOT NULL DEFAULT 0,
    version                       TEXT        NOT NULL DEFAULT '' CHECK (length(version) <= 64),
    build_commit                  TEXT        NOT NULL DEFAULT '' CHECK (length(build_commit) <= 64),
    -- 'running' while the process heartbeats; 'stopped' is written by a clean
    -- shutdown so an operator can tell a decommissioned replica from a crashed
    -- one. A crash leaves 'running' with a stale last_seen_at, which is the
    -- honest report: nobody was there to say goodbye.
    state                         TEXT        NOT NULL DEFAULT 'running'
                                  CHECK (state IN ('running', 'stopped')),
    started_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The settings poller's health record, made durable. NULL means this process
    -- has never completed a poll; a non-empty error means the last attempt
    -- failed and this replica may be serving stale settings/documents/branding.
    last_settings_poll_success_at TIMESTAMPTZ,
    last_settings_poll_error      TEXT        NOT NULL DEFAULT ''
                                  CHECK (length(last_settings_poll_error) <= 1024),
    stopped_at                    TIMESTAMPTZ
);

-- The fleet read (staleness) and the forget sweep both order by this.
CREATE INDEX process_heartbeats_last_seen_idx ON process_heartbeats (last_seen_at DESC);

-- Fill the identity columns of an event from the run it belongs to.
--
-- WHY A TRIGGER AND NOT THE PROJECTION FUNCTIONS. The events are written by
-- sync_legacy_job_run() (0083) and its three successors (0094 transcode steps,
-- 0107 storage migrations, 0120 upload finalize). Threading five columns through
-- four large PL/pgSQL functions means re-stating all four in this migration and
-- re-stating them again in every future one. A BEFORE INSERT trigger on the
-- events table itself is one place, applies to every producer including any
-- added later, and cannot disagree with the run it copies from.
--
-- It only ever FILLS: an event that arrives carrying its own id keeps it. The
-- run row is looked up by primary key, so the cost is one index probe per event.
CREATE FUNCTION inherit_job_event_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    run RECORD;
BEGIN
    IF NEW.request_id <> '' AND NEW.correlation_id <> '' AND NEW.trace_id <> ''
       AND NEW.worker_id <> '' AND NEW.pipeline_run_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    SELECT j.request_id, j.correlation_id, j.trace_id, j.worker_id, j.pipeline_run_id
      INTO run
      FROM job_runs j
     WHERE j.id = NEW.job_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    IF NEW.request_id = ''     THEN NEW.request_id     := run.request_id;     END IF;
    IF NEW.correlation_id = '' THEN NEW.correlation_id := run.correlation_id; END IF;
    IF NEW.trace_id = ''       THEN NEW.trace_id       := run.trace_id;       END IF;
    IF NEW.worker_id = ''      THEN NEW.worker_id      := run.worker_id;      END IF;
    IF NEW.pipeline_run_id IS NULL THEN NEW.pipeline_run_id := run.pipeline_run_id; END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER job_events_inherit_identity
BEFORE INSERT ON job_events FOR EACH ROW EXECUTE FUNCTION inherit_job_event_identity();

-- A CHILD run inherits its parent's identity.
--
-- 0094 projects transcode_steps as child runs under their transcode_jobs parent
-- (parent_job_id), and 0083's roll-up hides the parent whenever children exist —
-- so the row an operator actually sees on /admin/jobs is the CHILD. Stamping
-- only the parent would leave that visible row with the empty worker and empty
-- request id this migration exists to remove. The child is created by a trigger,
-- with no Go statement anywhere near it, so the inheritance has to happen here.
--
-- Same rule as the events: FILL only, never overwrite, and one primary-key probe.
CREATE FUNCTION inherit_job_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    parent RECORD;
BEGIN
    IF NEW.parent_job_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NEW.request_id <> '' AND NEW.correlation_id <> '' AND NEW.trace_id <> ''
       AND NEW.worker_id <> '' AND NEW.actor_id IS NOT NULL AND NEW.pipeline_run_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    SELECT j.request_id, j.correlation_id, j.trace_id, j.worker_id, j.actor_id, j.pipeline_run_id
      INTO parent
      FROM job_runs j
     WHERE j.id = NEW.parent_job_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    IF NEW.request_id = ''     THEN NEW.request_id     := parent.request_id;     END IF;
    IF NEW.correlation_id = '' THEN NEW.correlation_id := parent.correlation_id; END IF;
    IF NEW.trace_id = ''       THEN NEW.trace_id       := parent.trace_id;       END IF;
    -- The worker running a step is by construction the worker running its
    -- parent job: the step rows are written by that worker's own progress
    -- callbacks inside the job it claimed.
    IF NEW.worker_id = ''      THEN NEW.worker_id      := parent.worker_id;      END IF;
    IF NEW.actor_id IS NULL        THEN NEW.actor_id        := parent.actor_id;        END IF;
    IF NEW.pipeline_run_id IS NULL THEN NEW.pipeline_run_id := parent.pipeline_run_id; END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER job_runs_inherit_identity
BEFORE INSERT ON job_runs FOR EACH ROW EXECUTE FUNCTION inherit_job_run_identity();

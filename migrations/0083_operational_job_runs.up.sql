-- 0083: unified operational job-run foundation.
--
-- The existing queue tables remain the execution source of truth. This migration
-- adds a secret-free operator projection and durable event log, then maintains it
-- transactionally with AFTER triggers for the seven UUID-backed durable execution
-- tables that have unambiguous one-row/one-run semantics. Recurring schedulers,
-- channel-sync bindings, upload sessions, IPFS ledgers, media GC, and live replay
-- are deliberately not projected: they need distinct execution rows before they
-- can be represented honestly.

CREATE TABLE pipeline_runs (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    schema_version      SMALLINT    NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    type                TEXT        NOT NULL CHECK (type <> '' AND length(type) <= 128),
    state               TEXT        NOT NULL DEFAULT 'queued'
                        CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'partial')),
    actor_id            UUID        REFERENCES users (id) ON DELETE SET NULL,
    resource_type       TEXT        NOT NULL DEFAULT '' CHECK (length(resource_type) <= 64),
    resource_id         TEXT        NOT NULL DEFAULT '' CHECK (length(resource_id) <= 255),
    request_id          TEXT        NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    correlation_id      TEXT        NOT NULL DEFAULT '' CHECK (length(correlation_id) <= 128),
    trace_id            TEXT        NOT NULL DEFAULT '' CHECK (length(trace_id) <= 32),
    idempotency_key     TEXT        NOT NULL DEFAULT '' CHECK (length(idempotency_key) <= 255),
    input_metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(input_metadata) = 'object'
                               AND octet_length(input_metadata::text) <= 16384),
    output_metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(output_metadata) = 'object'
                               AND octet_length(output_metadata::text) <= 16384),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at          TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at         TIMESTAMPTZ
);

CREATE INDEX pipeline_runs_created_idx ON pipeline_runs (created_at DESC, id DESC);
CREATE INDEX pipeline_runs_resource_idx
    ON pipeline_runs (resource_type, resource_id, created_at DESC)
    WHERE resource_type <> '' AND resource_id <> '';
CREATE UNIQUE INDEX pipeline_runs_idempotency_idx
    ON pipeline_runs (type, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE job_runs (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    pipeline_run_id     UUID        REFERENCES pipeline_runs (id) ON DELETE SET NULL,
    parent_job_id       UUID        REFERENCES job_runs (id) ON DELETE SET NULL,
    type                TEXT        NOT NULL CHECK (type <> '' AND length(type) <= 128),
    queue               TEXT        NOT NULL CHECK (queue <> '' AND length(queue) <= 128),
    source_id           TEXT        NOT NULL DEFAULT '' CHECK (length(source_id) <= 255),
    state               TEXT        NOT NULL DEFAULT 'queued'
                        CHECK (state IN ('queued', 'claimed', 'running', 'retry_scheduled',
                                         'succeeded', 'failed', 'dead_lettered',
                                         'cancel_requested', 'cancelled')),
    stage               TEXT        NOT NULL DEFAULT '' CHECK (length(stage) <= 128),
    progress_percent    SMALLINT    CHECK (progress_percent BETWEEN 0 AND 100),
    priority            INTEGER     NOT NULL DEFAULT 0,
    attempt             INTEGER     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts        INTEGER     CHECK (max_attempts IS NULL OR max_attempts > 0),
    idempotency_key     TEXT        NOT NULL DEFAULT '' CHECK (length(idempotency_key) <= 255),
    actor_id            UUID        REFERENCES users (id) ON DELETE SET NULL,
    resource_type       TEXT        NOT NULL DEFAULT '' CHECK (length(resource_type) <= 64),
    resource_id         TEXT        NOT NULL DEFAULT '' CHECK (length(resource_id) <= 255),
    request_id          TEXT        NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    correlation_id      TEXT        NOT NULL DEFAULT '' CHECK (length(correlation_id) <= 128),
    trace_id            TEXT        NOT NULL DEFAULT '' CHECK (length(trace_id) <= 32),
    worker_id           TEXT        NOT NULL DEFAULT '' CHECK (length(worker_id) <= 255),
    lease_token         UUID,
    lease_expires_at    TIMESTAMPTZ,
    heartbeat_at        TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    input_metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(input_metadata) = 'object'
                               AND octet_length(input_metadata::text) <= 16384),
    output_metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(output_metadata) = 'object'
                               AND octet_length(output_metadata::text) <= 16384),
    error_class         TEXT        NOT NULL DEFAULT '' CHECK (length(error_class) <= 64),
    error_code          TEXT        NOT NULL DEFAULT '' CHECK (length(error_code) <= 128),
    error_detail        TEXT        NOT NULL DEFAULT '' CHECK (length(error_detail) <= 2048),
    error_retryable     BOOLEAN,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at          TIMESTAMPTZ,
    started_at          TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at         TIMESTAMPTZ
);

CREATE UNIQUE INDEX job_runs_legacy_source_idx
    ON job_runs (queue, source_id) WHERE source_id <> '';
CREATE UNIQUE INDEX job_runs_idempotency_idx
    ON job_runs (queue, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX job_runs_created_idx ON job_runs (created_at DESC, id DESC);
CREATE INDEX job_runs_state_created_idx ON job_runs (state, created_at DESC, id DESC);
CREATE INDEX job_runs_type_created_idx ON job_runs (type, created_at DESC, id DESC);
CREATE INDEX job_runs_queue_state_created_idx ON job_runs (queue, state, created_at DESC, id DESC);
CREATE INDEX job_runs_resource_created_idx
    ON job_runs (resource_type, resource_id, created_at DESC, id DESC)
    WHERE resource_type <> '' AND resource_id <> '';
CREATE INDEX job_runs_worker_state_idx
    ON job_runs (worker_id, state, updated_at DESC) WHERE worker_id <> '';
CREATE INDEX job_runs_pipeline_idx
    ON job_runs (pipeline_run_id, created_at, id) WHERE pipeline_run_id IS NOT NULL;
CREATE INDEX job_runs_stale_lease_idx
    ON job_runs (lease_expires_at)
    WHERE state IN ('claimed', 'running') AND lease_expires_at IS NOT NULL;

CREATE TABLE job_events (
    cursor              BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id                  UUID        NOT NULL DEFAULT uuid_generate_v4() UNIQUE,
    schema_version      SMALLINT    NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    job_id              UUID        NOT NULL REFERENCES job_runs (id) ON DELETE CASCADE,
    pipeline_run_id     UUID        REFERENCES pipeline_runs (id) ON DELETE SET NULL,
    kind                TEXT        NOT NULL CHECK (kind <> '' AND length(kind) <= 128),
    state               TEXT        NOT NULL
                        CHECK (state IN ('queued', 'claimed', 'running', 'retry_scheduled',
                                         'succeeded', 'failed', 'dead_lettered',
                                         'cancel_requested', 'cancelled')),
    stage               TEXT        NOT NULL DEFAULT '' CHECK (length(stage) <= 128),
    progress_percent    SMALLINT    CHECK (progress_percent BETWEEN 0 AND 100),
    attempt             INTEGER     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    worker_id           TEXT        NOT NULL DEFAULT '' CHECK (length(worker_id) <= 255),
    message             TEXT        NOT NULL DEFAULT '' CHECK (length(message) <= 1024),
    metadata            JSONB       NOT NULL DEFAULT '{}'::jsonb
                        CHECK (jsonb_typeof(metadata) = 'object'
                               AND octet_length(metadata::text) <= 8192),
    request_id          TEXT        NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    correlation_id      TEXT        NOT NULL DEFAULT '' CHECK (length(correlation_id) <= 128),
    trace_id            TEXT        NOT NULL DEFAULT '' CHECK (length(trace_id) <= 32),
    occurred_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX job_events_job_cursor_idx ON job_events (job_id, cursor DESC);
CREATE INDEX job_events_occurred_idx ON job_events (occurred_at, cursor);

-- Map one legacy execution row into the canonical, intentionally secret-free
-- representation. The function reads only explicitly allowlisted identifiers and
-- state fields from to_jsonb(NEW); it never copies a URL, payload, storage key,
-- process output, or legacy free-form error.
CREATE FUNCTION sync_legacy_job_run() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    r                   JSONB := to_jsonb(NEW);
    old_r               JSONB;
    canonical_type      TEXT;
    canonical_state     TEXT;
    canonical_stage     TEXT := '';
    canonical_kind      TEXT := '';
    canonical_resource  TEXT := '';
    canonical_resource_id TEXT := '';
    canonical_actor     UUID;
    canonical_job_id    UUID;
    canonical_progress  SMALLINT;
    attempt_count       INTEGER := COALESCE(NULLIF(r->>'attempts', '')::integer, 0);
    run_started_at      TIMESTAMPTZ;
    run_finished_at     TIMESTAMPTZ;
    safe_error_class    TEXT := '';
    safe_error_code     TEXT := '';
    safe_error_detail   TEXT := '';
    safe_retryable      BOOLEAN;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        old_r := to_jsonb(OLD);
    END IF;

    CASE TG_TABLE_NAME
        WHEN 'transcode_jobs' THEN
            canonical_type := 'video_transcode';
            canonical_resource := 'video';
            canonical_resource_id := r->>'video_id';
        WHEN 'federation_deliveries' THEN
            canonical_type := 'federation_delivery';
            canonical_resource := 'federation_delivery';
            canonical_resource_id := r->>'id';
        WHEN 'import_jobs' THEN
            canonical_type := 'video_import';
            canonical_resource := 'video';
            canonical_resource_id := r->>'video_id';
            canonical_stage := COALESCE(r->>'stage', '');
        WHEN 'caption_jobs' THEN
            canonical_type := 'caption_generate';
            canonical_resource := 'video';
            canonical_resource_id := r->>'video_id';
        WHEN 'account_exports' THEN
            canonical_type := 'account_export';
            canonical_resource := 'user';
            canonical_resource_id := r->>'user_id';
            canonical_actor := NULLIF(r->>'user_id', '')::uuid;
        WHEN 'atproto_posts' THEN
            canonical_type := 'atproto_post';
            canonical_resource := 'video';
            canonical_resource_id := r->>'video_id';
        WHEN 'peertube_import_runs' THEN
            canonical_type := 'peertube_import';
            canonical_resource := 'peertube_import';
            canonical_resource_id := r->>'id';
            canonical_actor := NULLIF(r->>'started_by', '')::uuid;
        ELSE
            RAISE EXCEPTION 'unsupported legacy job table %', TG_TABLE_NAME;
    END CASE;

    CASE r->>'state'
        WHEN 'pending' THEN
            canonical_state := CASE WHEN attempt_count > 0 THEN 'retry_scheduled' ELSE 'queued' END;
        WHEN 'running' THEN canonical_state := 'running';
        WHEN 'done' THEN canonical_state := 'succeeded';
        WHEN 'delivered' THEN canonical_state := 'succeeded';
        WHEN 'posted' THEN canonical_state := 'succeeded';
        WHEN 'failed' THEN canonical_state := 'dead_lettered';
        ELSE canonical_state := 'failed';
    END CASE;

    -- Coarse percentages are intentionally derived only from safe state/stage
    -- vocabulary. They are never parsed from process output. Import jobs have
    -- honest persisted stages; other legacy queues expose only endpoints.
    IF canonical_state IN ('queued', 'retry_scheduled') THEN
        canonical_progress := 0;
    ELSIF canonical_state = 'succeeded' THEN
        canonical_progress := 100;
    ELSIF TG_TABLE_NAME = 'import_jobs' AND canonical_state = 'running' THEN
        canonical_progress := CASE canonical_stage
            WHEN 'resolving' THEN 10
            WHEN 'downloading' THEN 35
            WHEN 'processing' THEN 75
            ELSE NULL
        END;
    END IF;

    IF canonical_state = 'running' THEN
        run_started_at := COALESCE(NULLIF(r->>'started_at', '')::timestamptz,
                                   NULLIF(r->>'updated_at', '')::timestamptz, now());
    END IF;
    IF canonical_state IN ('succeeded', 'failed', 'dead_lettered', 'cancelled') THEN
        run_finished_at := COALESCE(NULLIF(r->>'finished_at', '')::timestamptz,
                                    NULLIF(r->>'updated_at', '')::timestamptz, now());
    END IF;

    IF canonical_state = 'dead_lettered' THEN
        safe_error_class := 'execution';
        safe_error_code := canonical_type || '_failed';
        safe_error_detail := 'Execution failed; inspect correlated system logs for diagnostic detail.';
        safe_retryable := false;
    ELSIF canonical_state = 'retry_scheduled' THEN
        safe_error_class := 'execution';
        safe_error_code := 'retry_scheduled';
        safe_error_detail := 'A bounded retry was scheduled after an execution failure.';
        safe_retryable := true;
    END IF;

    INSERT INTO job_runs (
        type, queue, source_id, state, stage, progress_percent, priority, attempt, actor_id,
        resource_type, resource_id, input_metadata, output_metadata,
        error_class, error_code, error_detail, error_retryable,
        created_at, started_at, updated_at, finished_at
    ) VALUES (
        canonical_type, TG_TABLE_NAME, r->>'id', canonical_state,
        canonical_stage, canonical_progress, 0, attempt_count, canonical_actor, canonical_resource,
        canonical_resource_id, '{}'::jsonb, '{}'::jsonb, safe_error_class,
        safe_error_code, safe_error_detail, safe_retryable,
        COALESCE(NULLIF(r->>'created_at', '')::timestamptz, now()), run_started_at,
        COALESCE(NULLIF(r->>'updated_at', '')::timestamptz, now()), run_finished_at
    )
    ON CONFLICT (queue, source_id) WHERE source_id <> '' DO UPDATE SET
        state = EXCLUDED.state,
        stage = EXCLUDED.stage,
        progress_percent = EXCLUDED.progress_percent,
        attempt = EXCLUDED.attempt,
        actor_id = COALESCE(job_runs.actor_id, EXCLUDED.actor_id),
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
        canonical_kind := CASE canonical_state
            WHEN 'queued' THEN 'enqueued'
            WHEN 'retry_scheduled' THEN 'retry_scheduled'
            WHEN 'running' THEN 'started'
            WHEN 'succeeded' THEN 'succeeded'
            WHEN 'dead_lettered' THEN 'dead_lettered'
            ELSE 'snapshot'
        END;
    ELSIF old_r->>'state' IS DISTINCT FROM r->>'state' THEN
        canonical_kind := CASE canonical_state
            WHEN 'queued' THEN 'queued'
            WHEN 'retry_scheduled' THEN 'retry_scheduled'
            WHEN 'running' THEN 'started'
            WHEN 'succeeded' THEN 'succeeded'
            WHEN 'dead_lettered' THEN 'dead_lettered'
            ELSE 'failed'
        END;
    ELSIF TG_TABLE_NAME = 'import_jobs'
          AND old_r->>'stage' IS DISTINCT FROM r->>'stage' THEN
        canonical_kind := 'stage_changed';
    ELSIF TG_TABLE_NAME = 'peertube_import_runs'
          AND old_r->'progress' IS DISTINCT FROM r->'progress' THEN
        canonical_kind := 'progress';
    ELSIF old_r->>'attempts' IS DISTINCT FROM r->>'attempts' THEN
        canonical_kind := 'attempt_changed';
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO job_events (
        job_id, kind, state, stage, progress_percent, attempt, message, metadata, occurred_at
    ) VALUES (
        canonical_job_id, canonical_kind, canonical_state, canonical_stage,
        canonical_progress, attempt_count,
        CASE
            WHEN canonical_state = 'dead_lettered' THEN safe_error_detail
            WHEN canonical_state = 'retry_scheduled' THEN safe_error_detail
            ELSE ''
        END,
        '{}'::jsonb,
        COALESCE(NULLIF(r->>'updated_at', '')::timestamptz, now())
    );

    RETURN NEW;
END;
$$;

CREATE TRIGGER transcode_jobs_operational_projection
AFTER INSERT OR UPDATE ON transcode_jobs FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER federation_deliveries_operational_projection
AFTER INSERT OR UPDATE ON federation_deliveries FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER import_jobs_operational_projection
AFTER INSERT OR UPDATE ON import_jobs FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER caption_jobs_operational_projection
AFTER INSERT OR UPDATE ON caption_jobs FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER account_exports_operational_projection
AFTER INSERT OR UPDATE ON account_exports FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER atproto_posts_operational_projection
AFTER INSERT OR UPDATE ON atproto_posts FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();
CREATE TRIGGER peertube_import_runs_operational_projection
AFTER INSERT OR UPDATE ON peertube_import_runs FOR EACH ROW EXECUTE FUNCTION sync_legacy_job_run();

-- Backfill every active execution and only 90 days of terminal history. This is
-- a direct read/insert: it does not rewrite or lock every source row, and it does
-- not copy the row JSON into the projection. The JSON representation is merely a
-- convenient, migration-local way to normalize explicitly selected safe fields.
WITH legacy AS (
    SELECT 'transcode_jobs'::text AS queue, 'video_transcode'::text AS type,
           to_jsonb(t) AS r, 'video'::text AS resource_type,
           t.video_id::text AS resource_id, NULL::uuid AS actor_id
    FROM transcode_jobs t
    WHERE t.state IN ('pending', 'running') OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'federation_deliveries', 'federation_delivery', to_jsonb(t),
           'federation_delivery', t.id::text, NULL::uuid
    FROM federation_deliveries t
    WHERE t.state = 'pending' OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'import_jobs', 'video_import', to_jsonb(t), 'video', t.video_id::text, NULL::uuid
    FROM import_jobs t
    WHERE t.state IN ('pending', 'running') OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'caption_jobs', 'caption_generate', to_jsonb(t), 'video', t.video_id::text, NULL::uuid
    FROM caption_jobs t
    WHERE t.state IN ('pending', 'running') OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'account_exports', 'account_export', to_jsonb(t), 'user', t.user_id::text, t.user_id
    FROM account_exports t
    WHERE t.state IN ('pending', 'running') OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'atproto_posts', 'atproto_post', to_jsonb(t), 'video', t.video_id::text, NULL::uuid
    FROM atproto_posts t
    WHERE t.state = 'pending' OR t.updated_at >= now() - interval '90 days'
    UNION ALL
    SELECT 'peertube_import_runs', 'peertube_import', to_jsonb(t),
           'peertube_import', t.id::text, t.started_by
    FROM peertube_import_runs t
    WHERE t.state IN ('pending', 'running') OR t.updated_at >= now() - interval '90 days'
), normalized AS (
    SELECT
        type, queue, r->>'id' AS source_id,
        CASE r->>'state'
            WHEN 'pending' THEN CASE WHEN COALESCE((r->>'attempts')::integer, 0) > 0
                                     THEN 'retry_scheduled' ELSE 'queued' END
            WHEN 'running' THEN 'running'
            WHEN 'done' THEN 'succeeded'
            WHEN 'delivered' THEN 'succeeded'
            WHEN 'posted' THEN 'succeeded'
            WHEN 'failed' THEN 'dead_lettered'
            ELSE 'failed'
        END AS state,
        CASE WHEN queue = 'import_jobs' THEN COALESCE(r->>'stage', '') ELSE '' END AS stage,
        CASE
            WHEN r->>'state' = 'pending' THEN 0
            WHEN r->>'state' IN ('done', 'delivered', 'posted') THEN 100
            WHEN queue = 'import_jobs' AND r->>'state' = 'running' THEN
                CASE r->>'stage' WHEN 'resolving' THEN 10 WHEN 'downloading' THEN 35
                                  WHEN 'processing' THEN 75 ELSE NULL END
            ELSE NULL
        END::smallint AS progress_percent,
        COALESCE((r->>'attempts')::integer, 0) AS attempt,
        actor_id, resource_type, resource_id,
        COALESCE((r->>'created_at')::timestamptz, now()) AS created_at,
        CASE WHEN r->>'state' = 'running'
             THEN COALESCE((r->>'started_at')::timestamptz, (r->>'updated_at')::timestamptz, now())
             ELSE (r->>'started_at')::timestamptz END AS started_at,
        COALESCE((r->>'updated_at')::timestamptz, now()) AS updated_at,
        CASE WHEN r->>'state' IN ('done', 'delivered', 'posted', 'failed')
             THEN COALESCE((r->>'finished_at')::timestamptz, (r->>'updated_at')::timestamptz, now())
             ELSE (r->>'finished_at')::timestamptz END AS finished_at
    FROM legacy
)
INSERT INTO job_runs (
    type, queue, source_id, state, stage, progress_percent, priority, attempt,
    actor_id, resource_type, resource_id, input_metadata, output_metadata,
    error_class, error_code, error_detail, error_retryable,
    created_at, started_at, updated_at, finished_at
)
SELECT
    type, queue, source_id, state, stage, progress_percent, 0, attempt,
    actor_id, resource_type, resource_id, '{}'::jsonb, '{}'::jsonb,
    CASE WHEN state IN ('dead_lettered', 'retry_scheduled') THEN 'execution' ELSE '' END,
    CASE WHEN state = 'dead_lettered' THEN type || '_failed'
         WHEN state = 'retry_scheduled' THEN 'retry_scheduled' ELSE '' END,
    CASE WHEN state = 'dead_lettered'
         THEN 'Execution failed; inspect correlated system logs for diagnostic detail.'
         WHEN state = 'retry_scheduled'
         THEN 'A bounded retry was scheduled after an execution failure.' ELSE '' END,
    CASE WHEN state = 'dead_lettered' THEN false
         WHEN state = 'retry_scheduled' THEN true ELSE NULL END,
    created_at, started_at, updated_at, finished_at
FROM normalized
ON CONFLICT (queue, source_id) WHERE source_id <> '' DO NOTHING;

INSERT INTO job_events (job_id, kind, state, stage, progress_percent, attempt, metadata, occurred_at)
SELECT j.id, 'snapshot', j.state, j.stage, j.progress_percent, j.attempt, '{}'::jsonb, j.updated_at
FROM job_runs j
WHERE NOT EXISTS (SELECT 1 FROM job_events e WHERE e.job_id = j.id);

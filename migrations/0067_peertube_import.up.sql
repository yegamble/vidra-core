-- 0067: PeerTube import / migration (fix_plan P18, .ralph/specs/peertube-import.md).
-- Two durable tables back the one-way "import an existing PeerTube instance into
-- Vidra" tool: a per-row IDEMPOTENCY LEDGER (so re-runs skip already-imported
-- rows and a crashed import resumes cleanly) and a RUNS table (so an admin can
-- launch/monitor an import job through the API, backed by an in-process worker).
--
-- Read-only on the SOURCE: nothing here touches the PeerTube database — these
-- tables live in Vidra's own database and record what was mapped. Source
-- credentials NEVER live in these tables (they come from server config or CLI
-- flags only); no secret is ever stored or logged here.

-- peertube_import_ledger — the durable idempotency + resume map. One row per
-- source entity: (entity_kind, source_id) -> the resulting Vidra row id, with a
-- status. source_id is the source's stable identifier (a PeerTube UUID when the
-- entity has one, otherwise its numeric id as text). The importer checks this
-- ledger before touching an entity: a 'done' row is skipped (idempotent re-run),
-- and the mapped vidra_id lets child entities resolve their already-imported
-- parents. It is GLOBAL (not scoped to one run) so a resumed or repeated import
-- stays a no-op across runs. note carries a SAFE, non-secret explanation
-- (e.g. "renamed to alice-2", "merged into existing user") — never PII or hashes.
CREATE TABLE peertube_import_ledger (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_kind TEXT        NOT NULL,
    source_id   TEXT        NOT NULL,
    vidra_id    UUID,
    status      TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'done', 'skipped', 'failed', 'unsupported')),
    note        TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- (entity_kind, source_id) is the idempotency key: one ledger row per source
    -- entity, so a re-import upserts rather than duplicates.
    UNIQUE (entity_kind, source_id)
);

-- Progress/summary counting (per-entity done/skipped/failed) and the dry-run
-- plan read the ledger by kind+status.
CREATE INDEX peertube_import_ledger_kind_status_idx
    ON peertube_import_ledger (entity_kind, status);

-- peertube_import_runs — durable admin-triggered import runs (the API contract
-- the vidra-user "Import from PeerTube" admin UI consumes + the worker's queue).
-- A run is 'dry_run' (report only, writes nothing but this row + its progress) or
-- 'run' (perform the import). The source connection is taken from SERVER CONFIG
-- when a run executes — never from the row, never from the browser. progress is a
-- JSONB snapshot the worker updates as it goes; error is a SAFE client-visible
-- summary. States: pending -> running -> done | failed.
CREATE TABLE peertube_import_runs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    mode            TEXT        NOT NULL
                        CHECK (mode IN ('dry_run', 'run')),
    state           TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (state IN ('pending', 'running', 'done', 'failed')),
    conflict_policy TEXT        NOT NULL DEFAULT 'skip'
                        CHECK (conflict_policy IN ('skip', 'rename', 'merge', 'fail')),
    -- Detected source schema version (application.migrationVersion); NULL until
    -- preflight has run.
    source_version  INTEGER,
    -- Per-entity {planned,imported,skipped,failed,unsupported} snapshot.
    progress        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- SAFE, client-visible failure/summary reason — never a DSN, credential, or
    -- internal error detail.
    error           TEXT        NOT NULL DEFAULT '',
    -- The admin who launched the run (audit trail); SET NULL if they are deleted.
    started_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);

-- The worker claims due, still-pending runs (oldest first).
CREATE INDEX peertube_import_runs_due_idx
    ON peertube_import_runs (next_attempt_at)
    WHERE state = 'pending';

-- A PeerTube import is a heavy, exclusive operation: at most ONE run may be
-- active (pending or running) at a time. The partial unique index on a constant
-- expression enforces it — a second launch while one is in flight conflicts.
CREATE UNIQUE INDEX peertube_import_runs_single_active_idx
    ON peertube_import_runs ((TRUE))
    WHERE state IN ('pending', 'running');

-- Listing runs newest-first backs the admin history view.
CREATE INDEX peertube_import_runs_created_idx
    ON peertube_import_runs (created_at DESC);

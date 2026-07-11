-- 0084: Typed, correlated security-audit envelope.
--
-- audit_log remains the low-volume security ledger. Routine/high-volume content
-- activity belongs in a separate activity stream; job execution history belongs
-- in job_events. The three streams correlate through the identifiers below but
-- deliberately have different retention and access policies.

ALTER TABLE audit_log
    ADD COLUMN schema_version  SMALLINT    NOT NULL DEFAULT 1,
    ADD COLUMN domain          TEXT        NOT NULL DEFAULT '',
    ADD COLUMN actor_kind      TEXT        NOT NULL DEFAULT 'anonymous',
    ADD COLUMN actor_role      TEXT        NOT NULL DEFAULT '',
    ADD COLUMN resource_type   TEXT        NOT NULL DEFAULT '',
    ADD COLUMN resource_id     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN correlation_id  TEXT        NOT NULL DEFAULT '',
    ADD COLUMN trace_id        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN pipeline_run_id UUID,
    ADD COLUMN job_id          UUID,
    ADD COLUMN metadata        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN changes         JSONB       NOT NULL DEFAULT '[]'::jsonb;

-- Existing rows are schema v1. Backfill the safe snapshot fields that can be
-- derived without inventing history; new typed events are written as v2.
UPDATE audit_log
SET domain = CASE
        WHEN position('.' IN action) > 1 THEN split_part(action, '.', 1)
        ELSE 'legacy'
    END,
    actor_kind = CASE WHEN actor_id IS NULL THEN 'anonymous' ELSE 'user' END;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_schema_version_check
        CHECK (schema_version BETWEEN 1 AND 32767),
    ADD CONSTRAINT audit_log_domain_length_check
        CHECK (char_length(domain) BETWEEN 1 AND 64),
    ADD CONSTRAINT audit_log_action_length_check
        CHECK (char_length(action) BETWEEN 1 AND 160),
    ADD CONSTRAINT audit_log_result_check
        CHECK (result IN ('success', 'failure')),
    ADD CONSTRAINT audit_log_actor_kind_check
        CHECK (actor_kind IN ('anonymous', 'user', 'system', 'service')),
    ADD CONSTRAINT audit_log_actor_role_check
        CHECK (actor_role IN ('', 'user', 'moderator', 'admin')),
    ADD CONSTRAINT audit_log_reason_length_check
        CHECK (octet_length(reason) <= 4096),
    ADD CONSTRAINT audit_log_request_id_length_check
        CHECK (octet_length(request_id) <= 128),
    ADD CONSTRAINT audit_log_correlation_id_length_check
        CHECK (octet_length(correlation_id) <= 128),
    ADD CONSTRAINT audit_log_trace_id_length_check
        CHECK (octet_length(trace_id) <= 64),
    ADD CONSTRAINT audit_log_resource_type_length_check
        CHECK (octet_length(resource_type) <= 64),
    ADD CONSTRAINT audit_log_resource_id_length_check
        CHECK (octet_length(resource_id) <= 512),
    ADD CONSTRAINT audit_log_metadata_shape_check
        CHECK (jsonb_typeof(metadata) = 'object'),
    ADD CONSTRAINT audit_log_metadata_size_check
        CHECK (octet_length(metadata::text) <= 8192),
    ADD CONSTRAINT audit_log_changes_shape_check
        CHECK (jsonb_typeof(changes) = 'array'),
    ADD CONSTRAINT audit_log_changes_size_check
        CHECK (octet_length(changes::text) <= 8192);

CREATE INDEX audit_log_domain_occurred_idx
    ON audit_log (domain, occurred_at DESC, id DESC);
CREATE INDEX audit_log_correlation_idx
    ON audit_log (correlation_id, occurred_at DESC, id DESC)
    WHERE correlation_id <> '';
CREATE INDEX audit_log_resource_idx
    ON audit_log (resource_type, resource_id, occurred_at DESC, id DESC)
    WHERE resource_type <> '' AND resource_id <> '';
CREATE INDEX audit_log_job_idx
    ON audit_log (job_id, occurred_at DESC, id DESC)
    WHERE job_id IS NOT NULL;
CREATE INDEX audit_log_pipeline_idx
    ON audit_log (pipeline_run_id, occurred_at DESC, id DESC)
    WHERE pipeline_run_id IS NOT NULL;

COMMENT ON TABLE audit_log IS
    'Low-volume security audit ledger. Do not store routine content activity, raw payloads, secrets, free-form content, signed URLs, or process output.';
COMMENT ON COLUMN audit_log.metadata IS
    'Allowlisted, bounded scalar metadata only (application-enforced; 8 KiB DB backstop).';
COMMENT ON COLUMN audit_log.changes IS
    'Allowlisted safe before/after scalar changes only (application-enforced; 8 KiB DB backstop).';

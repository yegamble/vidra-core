-- +goose Up

-- PeerTube integer ID → Vidra Core ID mappings for migration cutover
CREATE TABLE IF NOT EXISTS migration_id_mappings (
    job_id UUID NOT NULL REFERENCES migration_jobs(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    peertube_id INTEGER NOT NULL,
    vidra_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_migration_id_mappings_entity UNIQUE (job_id, entity_type, peertube_id)
);

CREATE INDEX idx_migration_id_mappings_job ON migration_id_mappings(job_id);
CREATE INDEX idx_migration_id_mappings_reverse ON migration_id_mappings(entity_type, vidra_id);

-- ETL checkpoint tracking for resume support
CREATE TABLE IF NOT EXISTS migration_checkpoints (
    job_id UUID NOT NULL REFERENCES migration_jobs(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, entity_type)
);

-- +goose Down

DROP TABLE IF EXISTS migration_checkpoints;
DROP TABLE IF EXISTS migration_id_mappings;

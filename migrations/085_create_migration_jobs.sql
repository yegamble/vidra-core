-- +goose Up
CREATE TABLE IF NOT EXISTS migration_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id TEXT NOT NULL,
    source_host TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    dry_run BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    stats_json JSONB NOT NULL DEFAULT '{}',
    source_db_host TEXT,
    source_db_port INTEGER DEFAULT 5432,
    source_db_name TEXT,
    source_db_user TEXT,
    source_db_password TEXT,
    source_media_path TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_migration_jobs_status ON migration_jobs(status);
CREATE INDEX idx_migration_jobs_admin_user_id ON migration_jobs(admin_user_id);
CREATE INDEX idx_migration_jobs_created_at ON migration_jobs(created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS migration_jobs;

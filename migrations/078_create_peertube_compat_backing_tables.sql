-- +goose Up
CREATE TABLE IF NOT EXISTS channel_collaborators (
    id UUID PRIMARY KEY,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'editor',
    status TEXT NOT NULL DEFAULT 'pending',
    responded_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (channel_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_collaborators_channel_id ON channel_collaborators(channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_collaborators_user_id ON channel_collaborators(user_id);

CREATE TABLE IF NOT EXISTS remote_runners (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    token TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'registered',
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    last_seen_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS remote_runner_registration_tokens (
    id UUID PRIMARY KEY,
    token TEXT NOT NULL UNIQUE,
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NULL,
    used_at TIMESTAMPTZ NULL,
    used_by_runner_id UUID NULL REFERENCES remote_runners(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_remote_runner_registration_tokens_created_at ON remote_runner_registration_tokens(created_at);

CREATE TABLE IF NOT EXISTS remote_runner_job_assignments (
    id UUID PRIMARY KEY,
    runner_id UUID NOT NULL REFERENCES remote_runners(id) ON DELETE CASCADE,
    encoding_job_id UUID NOT NULL REFERENCES encoding_jobs(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'assigned',
    progress INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    accepted_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (encoding_job_id)
);

CREATE INDEX IF NOT EXISTS idx_remote_runner_job_assignments_runner_id ON remote_runner_job_assignments(runner_id);
CREATE INDEX IF NOT EXISTS idx_remote_runner_job_assignments_state ON remote_runner_job_assignments(state);

-- +goose Down
DROP TABLE IF EXISTS remote_runner_job_assignments;
DROP TABLE IF EXISTS remote_runner_registration_tokens;
DROP TABLE IF EXISTS remote_runners;
DROP TABLE IF EXISTS channel_collaborators;

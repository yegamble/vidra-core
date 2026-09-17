-- Desired node configuration is one CAS document, not partially written setting
-- keys. Merely opening the admin page must not activate budgets on legacy nodes.
CREATE TABLE ipfs_control_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    policy_active boolean NOT NULL DEFAULT false,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Monotonic sequences and UUID idempotency survive HTTP retries, process crashes
-- and host-manager restarts. Config snapshots bind each operation to its intent.
CREATE TABLE ipfs_control_operations (
    id uuid PRIMARY KEY,
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE NOT NULL,
    config_revision bigint NOT NULL CHECK (config_revision > 0),
    action text NOT NULL CHECK (action IN ('apply', 'restart')),
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'running', 'succeeded', 'failed')),
    last_error_code text,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ipfs_control_operations_pending ON ipfs_control_operations (sequence)
    WHERE state IN ('pending', 'running');

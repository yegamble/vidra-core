-- 0057: account lifecycle (fix_plan P4 export/import + product-decisions.md §1
-- hard delete).
--
-- users.deleted_at — the §1 hard delete ANONYMISES the users row rather than
-- removing it (audit/actor integrity: audit rows, message sender identity, and
-- comment tombstones keep resolving). deleted_at stamps the irreversible
-- deletion; the anonymised row also has is_active = FALSE, a cleared password
-- hash, and placeholder username/email.
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;

-- comments.deleted_at — a deleted account's comments become TOMBSTONES: body
-- is emptied and deleted_at stamped, the row (and thus the reply thread
-- structure) is preserved, and views render "[deleted]".
ALTER TABLE comments ADD COLUMN deleted_at TIMESTAMPTZ;

-- account_exports — durable job queue for the P4 account export, mirroring
-- transcode_jobs (0039) / federation_deliveries (0038): POST /me/export
-- enqueues a pending row, an in-process worker claims due rows, writes the
-- JSON archive to blob storage (storage_key), and marks them done with a
-- 7-day expires_at — or reschedules with exponential backoff, dead-lettering
-- (state 'failed') after max attempts. A sweeper deletes expired archives.
-- States: pending → running → done | failed.
CREATE TABLE account_exports (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'running', 'done', 'failed')),
    storage_key     TEXT        NOT NULL DEFAULT '',
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending jobs cheaply (worker claim scan).
CREATE INDEX account_exports_due_idx
    ON account_exports (next_attempt_at)
    WHERE state = 'pending';

-- Single ACTIVE export per user: while one is pending/running, a re-request
-- is refused (the insert is ON CONFLICT DO NOTHING against this index).
CREATE UNIQUE INDEX account_exports_active_user_idx
    ON account_exports (user_id)
    WHERE state IN ('pending', 'running');

-- Status lookups read the user's latest export.
CREATE INDEX account_exports_user_created_idx
    ON account_exports (user_id, created_at DESC);

-- The expiry sweeper scans finished archives past their expires_at.
CREATE INDEX account_exports_expiry_idx
    ON account_exports (expires_at)
    WHERE expires_at IS NOT NULL;

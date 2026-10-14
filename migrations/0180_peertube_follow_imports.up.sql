-- 0180: peertube_follow_imports — the durable queue behind "follow the accounts
-- a PeerTube export says this user followed".
--
-- WHY A QUEUE. Each target costs a WebFinger + actor fetch to a third-party
-- host, up to 1000 per import. Done inside the upload request that would hold
-- it open for minutes and lose every unfinished target on a restart; a slow or
-- down remote must cost only its own retries.
--
-- SHAPE: one job row per import (the id the caller polls) and one target row per
-- followed handle, with the retry columns every queue here uses (attempts,
-- next_attempt_at as the lease, last_error). A target ends 'done', 'failed'
-- (attempt cap or unresolvable), or 'skipped_*' with the reason it was not
-- attempted, so the status counts add up to the total. user_id is repeated on
-- the target so a claim needs no join, and both cascade from users.
CREATE TABLE peertube_follow_imports (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX peertube_follow_imports_user_idx ON peertube_follow_imports (user_id, created_at DESC);
CREATE INDEX peertube_follow_imports_created_idx ON peertube_follow_imports (created_at);

CREATE TABLE peertube_follow_import_targets (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    import_id       UUID        NOT NULL REFERENCES peertube_follow_imports(id) ON DELETE CASCADE,
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target          TEXT        NOT NULL CHECK (char_length(target) BETWEEN 1 AND 512),
    state           TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'done', 'failed',
                                     'skipped_federation', 'skipped_invalid')),
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (import_id, target)
);
CREATE INDEX peertube_follow_import_targets_due_idx
    ON peertube_follow_import_targets (next_attempt_at) WHERE state = 'pending';

-- A re-import of the same export must not stack a second copy of work already
-- waiting: one pending row per (user, target), whichever import queued it.
CREATE UNIQUE INDEX peertube_follow_import_targets_pending_uniq
    ON peertube_follow_import_targets (user_id, target) WHERE state = 'pending';

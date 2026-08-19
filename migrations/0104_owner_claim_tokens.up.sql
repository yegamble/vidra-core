-- 0104: One-time owner-claim token (first-admin bootstrap).
--
-- Replaces the "first registered account becomes admin" rule, which let anyone
-- — including a bot — win a fresh public install by registering first. While
-- the users table is empty, boot mints a high-entropy claim token, logs it once
-- to the operator console, and stores ONLY its SHA-256 hash here (the raw token
-- is unrecoverable by design; each boot while unclaimed re-mints, replacing the
-- hash). POST /api/v1/setup/claim-owner exchanges the raw token for THE admin
-- account exactly once; every normal signup path refuses while the claim is
-- pending. Instances that already have users never mint a row (implicitly
-- claimed), so upgrades are unaffected.
CREATE TABLE owner_claim_tokens (
    -- Boolean primary key fixed TRUE: the table can only ever hold one row, so
    -- there is exactly one live claim token and a re-mint is a plain upsert.
    id          BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    token_hash  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set exactly once by the claim statement's `claimed_at IS NULL` guard —
    -- the row-level lock makes it the single-winner gate under concurrency.
    claimed_at  TIMESTAMPTZ
);

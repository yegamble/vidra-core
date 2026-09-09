-- Step-up tokens: the server-side record that a provider re-authentication
-- actually happened, for the two account-recovery capabilities a passwordless
-- account needs and could not previously reach.
--
-- An account created by ATProto or OIDC login is passwordless and carries a
-- deliberately unroutable placeholder address, so "supply your current
-- password" and "follow the link we mailed you" — the two proofs every
-- sensitive self-service action rests on — are both unavailable to it. The
-- replacement proof is a completed OAuth round trip through the provider that
-- already IS the account's credential, and this table is what makes that round
-- trip a fact the next request can check rather than a claim it has to trust.
--
-- Why a table and not a signed cookie or a JWT: the whole point is SINGLE USE.
-- A stateless token can be replayed until it expires; a row can be consumed by
-- the same statement that reads it. session_id is on the row for the same
-- reason the password change re-asks for a password: the assertion authorises
-- the browser that made it and nothing else, so a token lifted out of one
-- session's redirect is worthless in another.
CREATE TABLE IF NOT EXISTS step_up_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The session the assertion is bound to. ON DELETE CASCADE keeps a token
    -- from outliving the session it authorises; sessions are revoked rather
    -- than deleted in normal operation, and a revoked session cannot present a
    -- token anyway (the auth middleware refuses it first).
    session_id  UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    -- Which provider satisfied the challenge ("atproto" or an OIDC provider
    -- name). Recorded so the consuming action can say what it accepted.
    provider    TEXT NOT NULL,
    -- SHA-256 of the raw token. The raw value is high-entropy random, so a fast
    -- hash is the correct storage form (bcrypt is only for low-entropy
    -- passwords) — the same construction as refresh, reset and owner-claim
    -- tokens.
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The lookup key, and the guard against two rows ever sharing a token.
CREATE UNIQUE INDEX IF NOT EXISTS step_up_tokens_token_hash_idx ON step_up_tokens (token_hash);
CREATE INDEX IF NOT EXISTS step_up_tokens_user_id_idx ON step_up_tokens (user_id);
-- The purge predicate: expired rows are swept opportunistically at mint time
-- rather than by a new cron, so the table stays bounded with no extra process.
CREATE INDEX IF NOT EXISTS step_up_tokens_expires_at_idx ON step_up_tokens (expires_at);

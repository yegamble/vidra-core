-- 0129: Email-change requests (AUTH-05).
--
-- Backs the two-step self-service email change. Step one re-verifies the
-- CURRENT password and records a PENDING request for the new address; step two
-- consumes a single-use, expiring token delivered to that NEW address, which is
-- the possession proof. The account's live users.email is untouched until the
-- token is consumed, so a typo or a stolen access token cannot move an address
-- on its own.
--
-- A separate table rather than pending_* columns on users, mirroring
-- password_reset_tokens (0012) and email_verification_tokens (0013): the token
-- lifecycle (issued / used / expired / superseded) is the same lifecycle those
-- two already have, the hot users row stays untouched by a flow that may never
-- complete, and ON DELETE CASCADE disposes of pending requests with the account.
-- Only the SHA-256 hash of the token is persisted — never the raw token, which
-- exists only in the message the mailer sends.
CREATE TABLE email_change_requests (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The requested address, stored as typed. Its collision rules are enforced
    -- against users at request AND confirm time; users_email_lower_idx is the
    -- final authority, so a race between two pending requests for the same
    -- address is refused by the unique index rather than by a check.
    new_email   TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lookups are by token hash; it is unique so a hash cannot match two requests.
CREATE UNIQUE INDEX email_change_requests_token_hash_idx ON email_change_requests (token_hash);
-- The pending-state read and the supersede/cancel delete are both per-user.
CREATE INDEX email_change_requests_user_id_idx ON email_change_requests (user_id);

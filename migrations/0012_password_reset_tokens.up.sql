-- 0012: Password-reset tokens.
--
-- Backs the "forgot password" flow (PT-AUTH-PASSWORD-RESET). A request stores a
-- single-use, expiring token; only the SHA-256 hash is persisted (never the raw
-- token, which is delivered to the user out-of-band by the mailer adapter). The
-- complete step looks the token up by hash, checks it is unused and unexpired,
-- updates the password, and revokes the user's sessions.
CREATE TABLE password_reset_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lookups are by token hash; it is unique so a hash collision cannot match two
-- accounts.
CREATE UNIQUE INDEX password_reset_tokens_token_hash_idx ON password_reset_tokens (token_hash);
CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

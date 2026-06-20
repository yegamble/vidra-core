-- 0013: Email-verification tokens.
--
-- Backs the email-verification flow (PT-AUTH-EMAIL-VERIFY). A request stores a
-- single-use, expiring token; only the SHA-256 hash is persisted (never the raw
-- token, which is delivered out-of-band by the mailer adapter). The confirm step
-- looks the token up by hash, checks it is unused and unexpired, and flips
-- users.email_verified to TRUE. Mirrors password_reset_tokens (0012).
CREATE TABLE email_verification_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lookups are by token hash; it is unique so a hash cannot match two accounts.
CREATE UNIQUE INDEX email_verification_tokens_token_hash_idx ON email_verification_tokens (token_hash);
CREATE INDEX email_verification_tokens_user_id_idx ON email_verification_tokens (user_id);

-- 0056: TOTP two-factor authentication (fix_plan P4 "TOTP enrollment /
-- verification / recovery codes").
--
-- user_mfa holds at most one TOTP configuration per account. The shared secret
-- is envelope-encrypted at rest (internal/secretbox under MFA_KEY_KEK, falling
-- back to FEDERATION_KEY_KEK; raw in dev with a loud boot warning) — hence
-- totp_secret_sealed, never a plaintext column name that invites logging.
-- enabled=FALSE is a pending enrollment: the secret has been issued to the
-- user but not yet confirmed with a valid code, so login is unaffected.
CREATE TABLE user_mfa (
    user_id            UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    totp_secret_sealed TEXT NOT NULL,
    enabled            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Single-use recovery codes (10 issued when TOTP is enabled). Only the
-- SHA-256 hex of the canonicalized code is stored — the raw codes are shown
-- to the user exactly once. used_at NULL means still redeemable.
CREATE TABLE mfa_recovery_codes (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Challenge lookup: match a presented code hash to an unused row for the user.
CREATE INDEX mfa_recovery_codes_user_hash_idx ON mfa_recovery_codes (user_id, code_hash);

-- 0088: Sign-up & new users (config-parity W7).
--
-- (a) users.pending_email_verification marks accounts created WHILE the
--     registration_require_email_verification gate was active: login is
--     refused until the email is verified. The DEFAULT FALSE is the
--     grandfather clause — every pre-existing account (and any account
--     created while the gate is off) carries FALSE and is never
--     retroactively locked when the toggle flips on.
--
-- (b) users.history_enabled is the per-user watch-history preference
--     (PATCH /auth/me). DEFAULT TRUE keeps the shipped behaviour for every
--     existing account; new_user_history_enabled seeds it at account
--     creation.
--
-- (c) upload_usage_events is the rolling-24h daily-upload-quota ledger
--     (default_user_daily_quota_bytes). An append-only event per stored
--     original (upload/import/live replay) rather than a sum over
--     video_files, so deleting a video never "refunds" the daily window.
--     Rows older than the window are pruned opportunistically at record
--     time (see quota.RecordUpload).
ALTER TABLE users ADD COLUMN pending_email_verification BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN history_enabled BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE upload_usage_events (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    bytes      BIGINT      NOT NULL CHECK (bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX upload_usage_events_user_recent_idx ON upload_usage_events (user_id, created_at DESC);

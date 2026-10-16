-- Per-user rolling-24h upload quota override (PeerTube videoQuotaDaily).
-- A row is the admin's override for that account: a positive value caps the
-- account's trailing-24h uploads, 0 means unlimited for this account even when
-- the instance default (default_user_daily_quota_bytes) is set. NO row means
-- "the instance default applies" - the same tri-state storage_quota_bytes has.
-- A side table rather than a users column so the many explicit-column users
-- queries (and the sqlc User model they map to) stay untouched.
CREATE TABLE user_daily_quotas (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    daily_quota_bytes bigint NOT NULL CHECK (daily_quota_bytes >= 0),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

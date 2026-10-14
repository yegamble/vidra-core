-- Revert 0171. Per-user daily overrides are discarded; every account falls
-- back to the instance default (default_user_daily_quota_bytes).
DROP TABLE IF EXISTS user_daily_quotas;

-- name: GetSettingsVersion :one
-- The current cross-replica invalidation token (migration 0121). Every api
-- replica reads this on a short jittered ticker; when the number differs from
-- the one it last acted on, the three boot-loaded caches (instance settings,
-- instance documents, branding images) are reloaded. Aggregated rather than a
-- bare SELECT so a database whose singleton row went missing reads 0 instead of
-- raising pgx.ErrNoRows every tick — the poller's job is bounded staleness, not
-- a per-interval error log.
SELECT coalesce(max(version), 0)::BIGINT AS version
FROM settings_version
WHERE id = 1;

-- name: BumpSettingsVersion :one
-- Advance the invalidation token by one and return the new value. Called after
-- every write to an admin-editable store that is cached in memory; the returned
-- value is what makes a bump observable to a test without a second read.
--
-- Written as an upsert, not an UPDATE, so the counter self-heals if the
-- singleton row is ever missing: an UPDATE would silently affect zero rows and
-- leave the fleet stale with no error to notice.
INSERT INTO settings_version (id, version, updated_at)
VALUES (1, 1, now())
ON CONFLICT (id) DO UPDATE
SET version = settings_version.version + 1, updated_at = now()
RETURNING version;

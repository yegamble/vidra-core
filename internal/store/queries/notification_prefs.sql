-- name: ListNotificationPrefs :many
-- A user's stored notification preference rows. Types with no row default to
-- enabled; the service overlays these rows onto the known-type defaults.
SELECT type, enabled
FROM notification_prefs
WHERE user_id = $1
ORDER BY type;

-- name: UpsertNotificationPref :exec
-- Set one (user, type) preference (insert or update).
INSERT INTO notification_prefs (user_id, type, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, type) DO UPDATE
SET enabled = EXCLUDED.enabled, updated_at = now();

-- name: IsNotificationTypeEnabled :one
-- Whether the user receives notifications of this type. No stored row = the
-- default (enabled).
SELECT COALESCE(
    (SELECT enabled FROM notification_prefs WHERE user_id = $1 AND type = $2),
    TRUE
)::bool;

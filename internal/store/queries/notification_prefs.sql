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

-- name: ListNotificationEmailPrefs :many
-- A user's EXPLICIT email choices (migration 0157). A NULL email_enabled is the
-- type's default and is not returned; the service overlays these rows onto it.
SELECT type, email_enabled::bool AS email_enabled
FROM notification_prefs
WHERE user_id = $1 AND email_enabled IS NOT NULL
ORDER BY type;

-- name: UpsertNotificationEmailPref :exec
-- Set one (user, type) EMAIL choice. A new row takes the in-app default
-- (enabled = TRUE, the same answer as no row), so setting email never changes
-- whether the in-app notification is created.
INSERT INTO notification_prefs (user_id, type, email_enabled)
VALUES (sqlc.arg('user_id'), sqlc.arg('type'), sqlc.arg('email_enabled')::bool)
ON CONFLICT (user_id, type) DO UPDATE
SET email_enabled = EXCLUDED.email_enabled, updated_at = now();

-- name: GetUserContentPrefs :one
-- The caller's viewer content preferences. A miss (no row) means none saved:
-- no per-flag overrides, no language filter.
SELECT user_id, sensitive_flag_policies, preferred_languages, updated_at
FROM user_content_prefs
WHERE user_id = $1;

-- name: UpsertUserContentPrefs :one
-- Merge into the caller's content preferences in ONE statement, so two
-- concurrent PUTs touching different fields cannot overwrite each other (a
-- read-modify-write in Go could). A NULL argument leaves that field as stored
-- (or the empty default on first save); the service validates beforehand.
INSERT INTO user_content_prefs (user_id, sensitive_flag_policies, preferred_languages)
VALUES (sqlc.arg('user_id'),
        COALESCE(sqlc.narg('sensitive_flag_policies')::jsonb, '{}'),
        COALESCE(sqlc.narg('preferred_languages')::text[], '{}'))
ON CONFLICT (user_id) DO UPDATE SET
    sensitive_flag_policies = COALESCE(sqlc.narg('sensitive_flag_policies')::jsonb, user_content_prefs.sensitive_flag_policies),
    preferred_languages = COALESCE(sqlc.narg('preferred_languages')::text[], user_content_prefs.preferred_languages),
    updated_at = now()
RETURNING user_id, sensitive_flag_policies, preferred_languages, updated_at;

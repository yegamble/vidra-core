-- name: GetUserContentPrefs :one
-- The caller's viewer content preferences. A miss (no row) means none saved:
-- no per-flag overrides, no language filter.
SELECT user_id, sensitive_flag_policies, preferred_languages, updated_at
FROM user_content_prefs
WHERE user_id = $1;

-- name: UpsertUserContentPrefs :one
-- Replace the caller's content preferences (the service validates, and does the
-- read-modify-write that keeps an omitted field). Creates the row on first save.
INSERT INTO user_content_prefs (user_id, sensitive_flag_policies, preferred_languages)
VALUES (sqlc.arg('user_id'), sqlc.arg('sensitive_flag_policies'), COALESCE(sqlc.arg('preferred_languages')::text[], '{}'))
ON CONFLICT (user_id) DO UPDATE SET
    sensitive_flag_policies = EXCLUDED.sensitive_flag_policies,
    preferred_languages = EXCLUDED.preferred_languages,
    updated_at = now()
RETURNING user_id, sensitive_flag_policies, preferred_languages, updated_at;

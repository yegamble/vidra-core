-- name: GetUserContentPrefs :one
-- The caller's viewer content preferences. A miss (no row) means none saved:
-- no per-flag overrides, no language filter.
SELECT user_id, sensitive_flag_policies, preferred_languages, updated_at
FROM user_content_prefs
WHERE user_id = $1;

-- name: UpsertUserSensitiveFlagPolicies :one
-- Replace the caller's per-flag sensitive policy overrides (the service
-- validates keys and values). Creates the row on first save.
INSERT INTO user_content_prefs (user_id, sensitive_flag_policies)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET
    sensitive_flag_policies = EXCLUDED.sensitive_flag_policies,
    updated_at = now()
RETURNING user_id, sensitive_flag_policies, preferred_languages, updated_at;

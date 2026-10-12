-- name: UpsertUserBan :exec
-- Records (or replaces) why an account is banned. is_active is flipped by
-- AdminUpdateUser; this row only explains it.
INSERT INTO user_bans (user_id, reason, banned_by)
VALUES (sqlc.arg('user_id'), sqlc.arg('reason'), sqlc.narg('banned_by'))
ON CONFLICT (user_id) DO UPDATE
    SET reason = EXCLUDED.reason, banned_by = EXCLUDED.banned_by, banned_at = now();

-- name: DeleteUserBan :exec
DELETE FROM user_bans WHERE user_id = $1;

-- name: GetUserBan :one
SELECT user_id, reason, banned_at, banned_by FROM user_bans WHERE user_id = $1;

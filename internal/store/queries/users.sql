-- name: GetUserByID :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes
FROM users
WHERE id = $1;

-- name: GetUserActorByUsername :one
-- Minimal, secret-free account fields for the ActivityPub Person actor. Only
-- active accounts are federated (deactivated accounts 404).
SELECT id, username, display_name, bio, created_at
FROM users
WHERE lower(username) = lower($1) AND is_active = true;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes
FROM users
WHERE lower(email) = lower($1);

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    bio          = COALESCE(sqlc.narg('bio'), bio),
    updated_at   = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes;

-- name: DeactivateUser :exec
UPDATE users
SET is_active  = FALSE,
    updated_at = now()
WHERE id = $1;

-- name: ListUsers :many
-- Admin user list: newest first, optionally filtered by a username/email
-- substring (empty query returns all). Paginated. Carries each account's
-- current storage usage (SUM of its video_files bytes via channel ownership)
-- so the admin view can show usage next to the quota.
SELECT u.id, u.username, u.email, u.password_hash, u.role, u.email_verified, u.is_active,
       u.created_at, u.updated_at, u.display_name, u.bio, u.storage_quota_bytes,
       (SELECT COALESCE(SUM(vf.size_bytes), 0)::bigint
          FROM video_files vf
          JOIN videos v ON v.id = vf.video_id
          JOIN channels c ON c.id = v.channel_id
         WHERE c.owner_id = u.id) AS storage_used_bytes
FROM users u
WHERE (sqlc.arg('query')::text = ''
       OR u.username ILIKE '%' || sqlc.arg('query') || '%'
       OR u.email ILIKE '%' || sqlc.arg('query') || '%')
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: AdminUpdateUser :one
-- Admin edit of a user's role, active flag, email_verified flag, and/or storage
-- quota (partial: NULL role/is_active/email_verified args are unchanged). The
-- quota is tri-state — unchanged unless set_storage_quota is true, in which
-- case a NULL value resets the account to the instance default and a value
-- (0 = unlimited) overrides it.
UPDATE users
SET role       = COALESCE(sqlc.narg('role'), role),
    is_active  = COALESCE(sqlc.narg('is_active'), is_active),
    email_verified = COALESCE(sqlc.narg('email_verified'), email_verified),
    storage_quota_bytes = CASE WHEN sqlc.arg('set_storage_quota')::bool
                               THEN sqlc.narg('storage_quota_bytes')::bigint
                               ELSE storage_quota_bytes END,
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes;

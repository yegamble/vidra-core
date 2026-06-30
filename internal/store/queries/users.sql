-- name: GetUserByID :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio
FROM users
WHERE lower(email) = lower($1);

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    bio          = COALESCE(sqlc.narg('bio'), bio),
    updated_at   = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio;

-- name: DeactivateUser :exec
UPDATE users
SET is_active  = FALSE,
    updated_at = now()
WHERE id = $1;

-- name: ListUsers :many
-- Admin user list: newest first, optionally filtered by a username/email
-- substring (empty query returns all). Paginated.
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio
FROM users
WHERE (sqlc.arg('query')::text = ''
       OR username ILIKE '%' || sqlc.arg('query') || '%'
       OR email ILIKE '%' || sqlc.arg('query') || '%')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: AdminUpdateUser :one
-- Admin edit of a user's role and/or active flag (partial: NULL args unchanged).
UPDATE users
SET role       = COALESCE(sqlc.narg('role'), role),
    is_active  = COALESCE(sqlc.narg('is_active'), is_active),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio;

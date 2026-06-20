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

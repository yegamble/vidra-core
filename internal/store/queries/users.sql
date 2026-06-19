-- name: GetUserByID :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at
FROM users
WHERE lower(email) = lower($1);

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at;

-- name: CountUsers :one
SELECT count(*) FROM users;

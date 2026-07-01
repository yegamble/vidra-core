-- name: CreateWatchedWord :one
-- Add a watched word. A duplicate term (case-insensitive) raises a unique
-- violation (SQLSTATE 23505), which the service maps to "already exists".
INSERT INTO watched_words (word, created_by)
VALUES ($1, $2)
RETURNING id, word, created_by, created_at;

-- name: ListWatchedWords :many
-- All watched words, newest first, with the creator's username (NULL if that
-- account was deleted).
SELECT w.id, w.word, w.created_at, u.username AS created_by_username
FROM watched_words w
LEFT JOIN users u ON u.id = w.created_by
ORDER BY w.created_at DESC, w.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: DeleteWatchedWord :execrows
-- Remove a watched word (idempotent). Returns rows deleted (0 = no such word).
DELETE FROM watched_words WHERE id = $1;

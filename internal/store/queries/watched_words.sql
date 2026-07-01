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

-- name: MatchWatchedWords :many
-- Watched terms that occur (case-insensitive substring) in the given text.
SELECT id, word FROM watched_words
WHERE strpos(lower(sqlc.arg('text')::text), lower(word)) > 0;

-- name: RecordWatchedWordMatch :exec
-- Record that a comment matched a watched term (idempotent per word+comment).
INSERT INTO watched_word_matches (watched_word_id, comment_id)
VALUES ($1, $2)
ON CONFLICT (watched_word_id, comment_id) DO NOTHING;

-- name: ListWatchedWordMatches :many
-- Flagged comments, newest match first, with the matched term + comment context
-- (body, author, and the video it is on).
SELECT m.id, m.created_at, w.word,
       c.id AS comment_id, c.body AS comment_body, c.video_id,
       u.username AS author_username
FROM watched_word_matches m
JOIN watched_words w ON w.id = m.watched_word_id
JOIN comments c ON c.id = m.comment_id
JOIN users u ON u.id = c.user_id
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

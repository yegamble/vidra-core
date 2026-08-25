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

-- name: CountWatchedWords :one
-- How many rows ListWatchedWords would return, ignoring pagination.
SELECT count(*)::bigint FROM watched_words;

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

-- name: RecordWatchedWordVideoMatch :exec
-- Record that a video's title/description matched a watched term (idempotent
-- per word+video; §12).
INSERT INTO watched_word_matches (watched_word_id, video_id)
VALUES ($1, $2)
ON CONFLICT (watched_word_id, video_id) WHERE video_id IS NOT NULL DO NOTHING;

-- name: ListWatchedWordMatches :many
-- Flagged content (comments AND videos), newest match first, with the matched
-- term + target context: for a comment match, its body/author and the video it
-- is on; for a video match, the video's title and its owner as the author.
-- comment_id/comment_body are NULL for video matches (the review UI's type
-- badge keys off that).
SELECT m.id, m.created_at, w.word,
       m.comment_id, c.body AS comment_body,
       COALESCE(m.video_id, c.video_id)::uuid AS video_id,
       v.title AS video_title,
       COALESCE(cu.username, vu.username)::text AS author_username
FROM watched_word_matches m
JOIN watched_words w ON w.id = m.watched_word_id
LEFT JOIN comments c ON c.id = m.comment_id
LEFT JOIN users cu ON cu.id = c.user_id
LEFT JOIN videos v ON v.id = COALESCE(m.video_id, c.video_id)
LEFT JOIN channels ch ON ch.id = v.channel_id
LEFT JOIN users vu ON vu.id = ch.owner_id
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountWatchedWordMatches :one
-- How many rows ListWatchedWordMatches would return, ignoring pagination. The
-- watched_words JOIN is part of the predicate (a match whose term was deleted
-- is not listed); the LEFT JOINs only add context and cannot change the count.
SELECT count(*)::bigint
FROM watched_word_matches m
JOIN watched_words w ON w.id = m.watched_word_id;

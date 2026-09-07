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
-- Record that a comment matched a watched term (idempotent per word+comment),
-- capturing the body AS IT READ AT FLAG TIME. The excerpt used to be a live join
-- on comments.body, so an author who edited the term away left the queue quoting
-- a clean body under the flag; the snapshot is what a moderator reviews, exactly
-- as reports.message_body_snapshot (0064) is for a reported DM. ON CONFLICT DO
-- NOTHING is what makes an edit that still contains the term keep the ORIGINAL
-- snapshot: only a term not yet recorded for this comment creates a row.
INSERT INTO watched_word_matches (
    watched_word_id, comment_id, matched_text, matched_term, match_offset, match_length
) VALUES ($1, $2, sqlc.arg('matched_text'), sqlc.arg('matched_term'),
          sqlc.arg('match_offset'), sqlc.arg('match_length'))
ON CONFLICT (watched_word_id, comment_id) DO NOTHING;

-- name: RecordWatchedWordVideoMatch :exec
-- Record that a video's title/description matched a watched term (idempotent
-- per word+video; §12), with the same flag-time snapshot as the comment arm —
-- a title or description edited afterwards does not rewrite what was flagged.
INSERT INTO watched_word_matches (
    watched_word_id, video_id, matched_text, matched_term, match_offset, match_length
) VALUES ($1, $2, sqlc.arg('matched_text'), sqlc.arg('matched_term'),
          sqlc.arg('match_offset'), sqlc.arg('match_length'))
ON CONFLICT (watched_word_id, video_id) WHERE video_id IS NOT NULL DO NOTHING;

-- name: ResolveWatchedWordMatch :execrows
-- Triage one match: 'resolved' (a moderator acted) or 'dismissed' (false
-- positive), with the moderator's note. Shaped like ResolveReport — the same
-- verb, the same idempotence (re-resolving overwrites and still succeeds), and
-- the note on the DOMAIN row because audit_log's metadata allowlist rejects
-- prose. Returns rows affected; 0 = no such match, which the service maps to
-- "not found".
UPDATE watched_word_matches
SET status         = sqlc.arg('status'),
    moderator_note = sqlc.arg('moderator_note'),
    resolved_by    = sqlc.arg('resolved_by'),
    resolved_at    = now()
WHERE id = sqlc.arg('id');

-- name: ListWatchedWordMatches :many
-- The moderation review queue for flagged content (comments AND videos), newest
-- match first.
--
-- What a row shows is the SNAPSHOT (matched_text + matched_term + the rune
-- offset/length of the hit inside it), never the live body: the live join this
-- replaced let an author edit the evidence away while the flag stood. The live
-- target is still reported, as target_status:
--   'present'      the term is still in the live comment/title+description
--   'edited_away'  it is not — the snapshot and the live target now differ
-- 'deleted' is not representable: comment_id/video_id keep ON DELETE CASCADE
-- (0132 says why), so a deleted target takes its match with it.
--
-- term_active is false once the watched WORD is deleted. The match survives that
-- now (0132 relaxed the FK to ON DELETE SET NULL) and still reads back its term
-- from the snapshot, so pruning the term list no longer discards review history.
-- The word JOIN is therefore a LEFT JOIN and is NOT part of the predicate.
--
-- status is 'open' | 'resolved' | 'dismissed', or NULL for all of them.
SELECT m.id, m.created_at,
       COALESCE(NULLIF(m.matched_term, ''), w.word, '')::text AS word,
       m.matched_text, m.match_offset, m.match_length, m.snapshot_backfilled,
       m.status, m.moderator_note, m.resolved_at,
       ru.username AS resolved_by_username,
       (m.watched_word_id IS NOT NULL)::bool AS term_active,
       m.comment_id, c.body AS comment_body,
       COALESCE(m.video_id, c.video_id)::uuid AS video_id,
       v.title AS video_title,
       COALESCE(cu.username, vu.username)::text AS author_username,
       (CASE
            WHEN strpos(
                     lower(COALESCE(c.body, v.title || E'\n' || v.description, '')),
                     lower(COALESCE(NULLIF(m.matched_term, ''), w.word, ''))
                 ) > 0 THEN 'present'
            ELSE 'edited_away'
        END)::text AS target_status
FROM watched_word_matches m
LEFT JOIN watched_words w ON w.id = m.watched_word_id
LEFT JOIN comments c ON c.id = m.comment_id
LEFT JOIN users cu ON cu.id = c.user_id
LEFT JOIN videos v ON v.id = COALESCE(m.video_id, c.video_id)
LEFT JOIN channels ch ON ch.id = v.channel_id
LEFT JOIN users vu ON vu.id = ch.owner_id
LEFT JOIN users ru ON ru.id = m.resolved_by
WHERE (sqlc.narg('status')::text IS NULL OR m.status = sqlc.narg('status')::text)
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountWatchedWordMatches :one
-- How many rows ListWatchedWordMatches would return, ignoring pagination. The
-- watched_words join is no longer part of the predicate (a match outlives its
-- term now), so the only filter is the status one — and it has to be the SAME
-- filter, or the queue would page a total it cannot serve.
SELECT count(*)::bigint
FROM watched_word_matches m
WHERE (sqlc.narg('status')::text IS NULL OR m.status = sqlc.narg('status')::text);

-- name: CreateWatchedWordList :one
-- owner_id NULL = an instance list; otherwise a creator's own list. A duplicate
-- name within the same scope is a unique violation (23505).
INSERT INTO watched_word_lists (owner_id, name, action)
VALUES (sqlc.narg('owner_id'), sqlc.arg('name'), sqlc.arg('action'))
RETURNING id, owner_id, name, action, is_default, created_at, updated_at;

-- name: GetWatchedWordList :one
-- One list in a scope: owner_id NULL addresses instance lists, a user id that
-- user's own lists. A list in the OTHER scope reads as absent, which is what
-- makes the per-account endpoints owner-only.
SELECT l.id, l.owner_id, l.name, l.action, l.is_default, l.created_at, l.updated_at,
       (SELECT count(*) FROM watched_words w WHERE w.list_id = l.id)::bigint AS word_count
FROM watched_word_lists l
WHERE l.id = sqlc.arg('id') AND l.owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid;

-- name: ListWatchedWordLists :many
-- The lists in a scope, default list first, then oldest first.
SELECT l.id, l.owner_id, l.name, l.action, l.is_default, l.created_at, l.updated_at,
       (SELECT count(*) FROM watched_words w WHERE w.list_id = l.id)::bigint AS word_count
FROM watched_word_lists l
WHERE l.owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid
ORDER BY l.is_default DESC, l.created_at, l.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountWatchedWordLists :one
SELECT count(*)::bigint FROM watched_word_lists
WHERE owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid;

-- name: CountOwnedWatchedWordLists :one
-- How many lists a creator owns (the per-account cap).
SELECT count(*)::bigint FROM watched_word_lists WHERE owner_id = sqlc.arg('owner_id');

-- name: UpdateWatchedWordList :one
-- Rename / change the action of a list in a scope; no row = not in this scope.
UPDATE watched_word_lists
SET name = sqlc.arg('name'), action = sqlc.arg('action'), updated_at = now()
WHERE id = sqlc.arg('id') AND owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid
RETURNING id, owner_id, name, action, is_default, created_at, updated_at;

-- name: DeleteWatchedWordList :execrows
-- The default list is never deletable. Its terms go with the list (ON DELETE
-- CASCADE); matches keep their snapshot (list_id SET NULL, list_name kept).
DELETE FROM watched_word_lists
WHERE id = sqlc.arg('id') AND NOT is_default
  AND owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid;

-- name: ListWatchedWordListWords :many
SELECT word FROM watched_words WHERE list_id = sqlc.arg('list_id') ORDER BY lower(word);

-- name: ReplaceWatchedWordListWords :exec
-- Make a list hold exactly the given terms, atomically (one statement): terms
-- not in the set are deleted, new ones inserted, kept ones untouched.
WITH removed AS (
    DELETE FROM watched_words
    WHERE list_id = sqlc.arg('list_id') AND lower(word) <> ALL (SELECT lower(y) FROM unnest(sqlc.arg('words')::text[]) AS y)
)
INSERT INTO watched_words (list_id, word, created_by)
SELECT sqlc.arg('list_id'), t.word, sqlc.narg('created_by')
FROM (SELECT DISTINCT ON (lower(x)) x AS word FROM unnest(sqlc.arg('words')::text[]) AS x) t
ON CONFLICT (list_id, lower(word)) DO NOTHING;

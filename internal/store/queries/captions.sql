-- name: UpsertCaption :one
-- Add or replace a video's caption track for a language (re-uploading a language
-- updates its label + storage key).
INSERT INTO captions (video_id, language, label, storage_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (video_id, language) DO UPDATE
    SET label = EXCLUDED.label, storage_key = EXCLUDED.storage_key, updated_at = now()
RETURNING id, video_id, language, label, storage_key, created_at, updated_at;

-- name: ListCaptionsByVideo :many
-- A video's caption tracks, ordered by language.
SELECT id, video_id, language, label, storage_key, created_at, updated_at
FROM captions
WHERE video_id = $1
ORDER BY language;

-- name: GetCaptionByLang :one
SELECT id, video_id, language, label, storage_key, created_at, updated_at
FROM captions
WHERE video_id = $1 AND language = $2;

-- name: DeleteCaption :execrows
-- Remove a video's caption track for a language (idempotent).
DELETE FROM captions WHERE video_id = $1 AND language = $2;

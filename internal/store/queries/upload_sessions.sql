-- Resumable upload sessions + received-chunk ledger (migration 0059, fix_plan
-- P6.1). The chunk bytes live in the blob backend at uploads/<session>/<n>;
-- these rows are the resume contract + sweeper input.

-- name: CreateUploadSession :one
INSERT INTO upload_sessions (video_id, user_id, filename, total_size, chunk_size, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id = $1;

-- name: UpsertUploadChunk :exec
-- Idempotent re-PUT: a chunk index that has already landed just updates its
-- recorded size (the blob at uploads/<session>/<n> is overwritten in place).
INSERT INTO upload_chunks (upload_id, n, size_bytes)
VALUES ($1, $2, $3)
ON CONFLICT (upload_id, n) DO UPDATE
SET size_bytes = EXCLUDED.size_bytes, updated_at = now();

-- name: ListUploadChunks :many
-- The received-chunk ledger for a session, ascending by index (the resume /
-- progress contract).
SELECT n, size_bytes FROM upload_chunks WHERE upload_id = $1 ORDER BY n;

-- name: SetUploadSessionState :exec
UPDATE upload_sessions
SET state = $2, updated_at = now()
WHERE id = $1;

-- name: ListSweepableUploadSessions :many
-- Cancelled sessions and any session past its 24h expiry, for the cleanup
-- sweeper (which deletes the chunk blobs then the row).
SELECT id FROM upload_sessions
WHERE state = 'cancelled' OR expires_at <= now()
ORDER BY expires_at
LIMIT $1;

-- name: DeleteUploadSession :exec
-- Cascades to upload_chunks. The caller removes the chunk blobs first.
DELETE FROM upload_sessions WHERE id = $1;

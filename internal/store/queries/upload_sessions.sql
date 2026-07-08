-- Resumable upload sessions + received-chunk ledger (migration 0059, fix_plan
-- P6.1). The chunk bytes live in the blob backend at uploads/<session>/<n>;
-- these rows are the resume contract + sweeper input.

-- name: CreateUploadSession :one
INSERT INTO upload_sessions (video_id, user_id, filename, total_size, chunk_size, expires_at, file_fingerprint)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id = $1;

-- name: ListActiveUploadSessionsForUser :many
-- The caller's ACTIVE (not completed/cancelled, not yet expired) upload sessions
-- with the number of chunks received so far — the cross-refresh / cross-device
-- resume contract (UPLOAD-03, GET /api/v1/me/uploads). When @fingerprint is
-- supplied the list narrows to sessions with that file_fingerprint (the partial
-- index from migration 0080 serves this lookup), so a client can ask "am I
-- already uploading this exact file?". localStorage becomes a cache, not the
-- source of truth.
SELECT s.id, s.video_id, s.filename, s.total_size, s.chunk_size,
       s.file_fingerprint, s.expires_at,
       (SELECT count(*) FROM upload_chunks c WHERE c.upload_id = s.id)::bigint AS received_chunks
FROM upload_sessions s
WHERE s.user_id = sqlc.arg('user_id')
  AND s.state = 'active'
  AND s.expires_at > now()
  AND (sqlc.narg('fingerprint')::text IS NULL OR s.file_fingerprint = sqlc.narg('fingerprint')::text)
ORDER BY s.created_at DESC;

-- name: CountActiveUploadSessionsForUser :one
-- The number of the caller's ACTIVE (not completed/cancelled, not yet expired)
-- upload sessions — the batch-upload guard input (UPLOAD-10, W2.C3).
-- createUploadSession refuses to open a new session once this reaches
-- UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER, so a client's batch orchestration queues
-- rather than opening unbounded concurrent sessions. A cancel or complete frees
-- a slot; the 24h-expiry sweeper is the backstop for abandoned ones.
SELECT count(*)::bigint FROM upload_sessions
WHERE user_id = sqlc.arg('user_id')
  AND state = 'active'
  AND expires_at > now();

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

-- Resumable upload sessions + received-chunk ledger (migration 0059, fix_plan
-- P6.1). The chunk bytes live in the blob backend at uploads/<session>/<n>;
-- these rows are the resume contract + sweeper input.

-- name: CreateUploadSession :one
INSERT INTO upload_sessions (video_id, user_id, filename, total_size, chunk_size, expires_at, file_fingerprint, purpose)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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

-- name: HasActiveReplaceSessionForVideo :one
-- Whether a replace-purpose session is already open for the video (config-
-- parity W14): at most one replacement may be in flight per video, so the
-- replace-session create answers 409 replace_conflict while one exists.
--
-- 'queued'/'processing' count as in flight (migration 0120): completion is now
-- asynchronous, so between the accepted POST and the worker's swap there is a
-- window in which the session is no longer 'active' but the replacement has
-- very much not finished. Admitting a second one there would race two
-- ReplaceSource runs onto the same video.
SELECT EXISTS (
    SELECT 1 FROM upload_sessions
    WHERE video_id = sqlc.arg('video_id')
      AND purpose = 'replace'
      AND state IN ('active', 'queued', 'processing')
      AND expires_at > now()
);

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
-- Advances the session's lifecycle state, clearing any stale failure reason: a
-- session that moves on from 'failed' (only via a re-queued finalize job) must
-- not keep answering the poll with the reason it failed last time.
UPDATE upload_sessions
SET state = $2, failure_reason = '', updated_at = now()
WHERE id = $1;

-- name: MarkUploadSessionQueued :execrows
-- Accept a completion: active → queued (migration 0120).
--
-- The state guard is the whole point, and it is a CAS rather than a plain
-- UPDATE. Completion validates the session, then enqueues a finalize job, and a
-- DELETE /uploads/{id} can land in between: Cancel flips the row to 'cancelled'
-- and deletes the chunk BLOBS, but leaves the upload_chunks ledger intact — so
-- an unguarded write would flip the row back to 'queued', silently undoing the
-- user's cancel and handing the worker a job whose bytes are gone (five attempts
-- and ~15 minutes of backoff before it dead-letters).
--
-- Zero rows therefore means "the session stopped being active underneath us",
-- and the caller drops the job it just enqueued and answers 409.
UPDATE upload_sessions
SET state = 'queued', failure_reason = '', updated_at = now()
WHERE id = $1 AND state = 'active';

-- name: FailUploadSession :exec
-- Terminal failure of the asynchronous completion (migration 0120): the finalize
-- job dead-lettered. reason is a SAFE, client-visible sentence — the poller on
-- GET /api/v1/uploads/{upload_id} reads it verbatim, so it must never carry an
-- internal error, a storage key, or process output.
UPDATE upload_sessions
SET state = 'failed', failure_reason = $2, updated_at = now()
WHERE id = $1;

-- name: ListSweepableUploadSessions :many
-- Cancelled sessions and any session past its 24h expiry, for the cleanup
-- sweeper (which deletes the chunk blobs then the row).
--
-- A session with a LIVE finalize job (migration 0120) is never swept, however
-- long it has been open: its chunk blobs are the worker's input, and deleting
-- them mid-assembly would fail a completion the client has already been told was
-- accepted. Once the job reaches done/failed the ordinary rules apply again —
-- and the lease sweep guarantees a job stranded by a dead worker reaches one of
-- those states rather than pinning the row forever.
SELECT s.id FROM upload_sessions s
WHERE (s.state = 'cancelled' OR s.expires_at <= now())
  AND NOT EXISTS (
      SELECT 1 FROM upload_finalize_jobs j
      WHERE j.upload_id = s.id AND j.state IN ('pending', 'running')
  )
ORDER BY s.expires_at
LIMIT $1;

-- name: DeleteUploadSession :exec
-- Cascades to upload_chunks. The caller removes the chunk blobs first.
DELETE FROM upload_sessions WHERE id = $1;

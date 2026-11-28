-- name: LockRetainedUpload :one
SELECT s.id FROM upload_sessions s JOIN videos v ON v.id = s.video_id
WHERE s.id = $1 FOR UPDATE OF s, v NOWAIT;

-- name: MarkRetainedUploadQueued :execrows
UPDATE upload_sessions SET state='queued', failure_reason='', updated_at=now()
WHERE id=$1 AND state='failed';

-- name: GetRecordedScanRecovery :one
SELECT j.id, j.state FROM retained_upload_recoveries r JOIN upload_finalize_jobs j ON j.id = r.job_id
WHERE r.upload_id = sqlc.arg(upload_id) AND r.owner_id = sqlc.arg(owner_id)
  AND r.sha256 = sqlc.arg(sha256) AND r.privacy = sqlc.arg(privacy);

-- name: GetRetainedScanSource :one
SELECT upload_id, video_id, file_id, owner_id, channel_id, privacy, sha256, size_bytes, storage_key
FROM retained_scan_sources WHERE upload_id = sqlc.arg(upload_id) AND owner_id = sqlc.arg(owner_id)
  AND sha256 = sqlc.arg(sha256) AND privacy = sqlc.arg(privacy) AND session_state = 'failed' AND expires_at > now();

-- name: RecordRetainedScanRecovery :exec
INSERT INTO retained_upload_recoveries(job_id,upload_id,file_id,owner_id,channel_id,privacy,sha256,size_bytes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: GetCurrentRetainedScanRecovery :one
SELECT job_id, video_id, storage_key, sha256, size_bytes, file_id
FROM current_retained_scan_recoveries WHERE job_id = $1;

-- name: SetRetainedScanVideoState :one
WITH changed AS (
 UPDATE videos v SET state = sqlc.arg(new_state), updated_at = now()
WHERE v.id = sqlc.arg(video_id) AND v.state = 'failed'
 AND EXISTS (SELECT 1 FROM current_retained_scan_recoveries r WHERE r.job_id = sqlc.arg(job_id) AND r.video_id = v.id)
 RETURNING v.*
), recorded AS (
 UPDATE retained_upload_recoveries SET accepted=true FROM changed
 WHERE job_id=sqlc.arg(job_id) AND changed.state <> 'failed'
)
SELECT * FROM changed;

-- name: IsRetainedScanAccepted :one
SELECT r.accepted FROM retained_upload_recoveries r JOIN upload_finalize_jobs j ON j.id=r.job_id
WHERE r.job_id=sqlc.arg(job_id) AND j.video_id=sqlc.arg(video_id);

-- name: DetachInfectedRetainedFile :exec
-- Collection of the unreferenced bytes uses the ordinary media GC. A delete
-- by key could otherwise delete a creator's concurrent replacement.
DELETE FROM video_files f USING retained_upload_recoveries r
WHERE r.job_id = $1 AND f.id = r.file_id AND f.sha256 = r.sha256
  AND f.kind = 'original' AND f.size_bytes = r.size_bytes;

-- name: LockDeclinedRetainedScanRecovery :one
-- The original enrollment must still identify the same attached bytes and
-- creator. Preserve completed work, real scanner verdicts and subsequent
-- attempts. The caller already holds the associated session and video locks.
SELECT j.id
FROM upload_finalize_jobs j
JOIN retained_upload_recoveries r ON r.job_id = j.id
JOIN retained_scan_sources s
  ON (s.upload_id,s.file_id,s.owner_id,s.channel_id,s.privacy,s.sha256,s.size_bytes)
   = (r.upload_id,r.file_id,r.owner_id,r.channel_id,r.privacy,r.sha256,r.size_bytes)
WHERE j.id = sqlc.arg(job_id) AND j.state = 'failed' AND j.attempts = 1
  AND j.error = sqlc.arg(declined_reason) AND NOT r.accepted
  AND s.session_state = 'failed' AND s.expires_at > now()
  AND NOT EXISTS (SELECT 1 FROM audit_log a
    WHERE a.action = 'content.upload.malware_rejected' AND a.reason LIKE 'video=' || s.video_id::text || ' outcome=%'
    AND a.occurred_at >= j.created_at)
FOR UPDATE OF j,r NOWAIT;

-- name: RequeueDeclinedRetainedScanRecovery :execrows
-- Keep attempts and the same execution id. Its projection appends the retry
-- after the original terminal event, retaining both in the operator timeline.
UPDATE upload_finalize_jobs SET state='pending', next_attempt_at=now(), updated_at=now(), error=''
WHERE id = sqlc.arg(job_id) AND state='failed' AND attempts=1 AND error=sqlc.arg(declined_reason);

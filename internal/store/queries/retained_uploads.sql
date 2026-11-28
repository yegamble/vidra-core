-- name: GetRetainedScanSource :one
SELECT upload_id, video_id, file_id, owner_id, channel_id, privacy, sha256, size_bytes, storage_key
FROM retained_scan_sources WHERE upload_id = sqlc.arg(upload_id) AND owner_id = sqlc.arg(owner_id)
  AND sha256 = sqlc.arg(sha256) AND privacy = sqlc.arg(privacy) AND session_state = 'failed' AND expires_at > now();

-- name: GetCurrentRetainedScanRecovery :one
SELECT job_id, video_id, storage_key, sha256, size_bytes, file_id
FROM current_retained_scan_recoveries WHERE job_id = $1;

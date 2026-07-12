-- Rolling-24h daily-upload-quota ledger (config-parity W7,
-- default_user_daily_quota_bytes). Events are appended when an original file
-- is stored (upload, URL import, live replay) and summed over the trailing
-- window at the upload gates. An append-only ledger — not a sum over
-- video_files — so deleting a video never "refunds" the daily window.

-- name: RecordUploadUsageEvent :exec
INSERT INTO upload_usage_events (user_id, bytes)
VALUES ($1, $2);

-- name: SumUploadUsageSince :one
-- Bytes the user stored since the window start. Approximate under concurrent
-- uploads by design (race-tolerant, documented in the quota service).
SELECT COALESCE(SUM(bytes), 0)::bigint
FROM upload_usage_events
WHERE user_id = $1 AND created_at > $2;

-- name: PruneUploadUsageEvents :execrows
-- Drop this user's events older than the cutoff. Called opportunistically at
-- record time so the ledger stays bounded without a dedicated sweeper.
DELETE FROM upload_usage_events
WHERE user_id = $1 AND created_at < $2;

-- Bind a second finalize attempt to one retained original and its creator's
-- existing publication settings. The first failed job remains in the ledger.
CREATE TABLE retained_upload_recoveries (
    job_id UUID PRIMARY KEY REFERENCES upload_finalize_jobs(id) ON DELETE CASCADE,
    upload_id UUID NOT NULL UNIQUE REFERENCES upload_sessions(id) ON DELETE CASCADE,
    file_id UUID NOT NULL,
    owner_id UUID NOT NULL,
    channel_id UUID NOT NULL,
    privacy TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    accepted BOOLEAN NOT NULL DEFAULT false
);

-- The CLI and worker consult the same eligibility conditions. A moderator
-- withdrawal, owner transfer or later rejection invalidates a queued replay.
CREATE VIEW retained_scan_sources AS
SELECT s.id AS upload_id, v.id AS video_id, f.id AS file_id, u.id AS owner_id,
       v.channel_id, v.privacy, f.sha256, f.size_bytes, f.storage_key, s.state AS session_state, s.expires_at
FROM upload_sessions s
JOIN videos v ON v.id = s.video_id
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id AND u.id = s.user_id
JOIN video_files f ON f.video_id = v.id AND f.kind = 'original'
JOIN LATERAL (
    SELECT j.created_at, j.updated_at, j.state FROM upload_finalize_jobs j
    WHERE j.upload_id = s.id AND j.purpose = 'upload'
      AND NOT EXISTS (SELECT 1 FROM retained_upload_recoveries r WHERE r.job_id = j.id)
    ORDER BY j.created_at DESC, j.id DESC LIMIT 1
) original_job ON original_job.state = 'failed'
WHERE s.purpose = 'upload' AND v.state = 'failed' AND f.size_bytes = s.total_size
  AND u.is_active AND u.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM user_bans b WHERE b.user_id = u.id)
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM video_rejections r WHERE r.video_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM video_renditions r WHERE r.video_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM streaming_playlists p WHERE p.video_id = v.id AND p.state = 'ready')
  AND NOT EXISTS (SELECT 1 FROM upload_sessions other WHERE other.video_id = v.id AND other.id <> s.id
      AND (other.created_at > s.created_at OR (other.state IN ('active','queued','processing') AND other.expires_at > now())))
  AND EXISTS (SELECT 1 FROM audit_log a WHERE a.action = 'content.upload.malware_rejected'
      AND a.reason = 'video=' || v.id::text || ' outcome=scan_error policy=fail-closed'
      AND a.occurred_at BETWEEN original_job.created_at AND original_job.updated_at)
  AND NOT EXISTS (SELECT 1 FROM audit_log a WHERE a.action = 'content.upload.malware_rejected'
      AND a.reason LIKE 'video=' || v.id::text || ' outcome=infected %'
      AND a.occurred_at >= original_job.created_at);

CREATE VIEW current_retained_scan_recoveries AS
SELECT r.job_id, s.video_id, s.storage_key, r.sha256, r.size_bytes, r.file_id
FROM retained_upload_recoveries r JOIN retained_scan_sources s
ON (s.upload_id,s.file_id,s.owner_id,s.channel_id,s.privacy,s.sha256,s.size_bytes) =
   (r.upload_id,r.file_id,r.owner_id,r.channel_id,r.privacy,r.sha256,r.size_bytes)
WHERE s.session_state IN ('queued','processing') AND s.expires_at > now();

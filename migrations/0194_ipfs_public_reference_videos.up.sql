-- A reference publisher reads this view before approving a generation and again
-- before serving its bytes. A new transcode invalidates the old approval even
-- while the previous HLS tree remains ready for ordinary playback.
CREATE VIEW ipfs_public_reference_videos WITH (security_barrier = true) AS
SELECT v.id AS video_id, sp.master_key
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id
JOIN streaming_playlists sp ON sp.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND u.is_active AND NOT u.unlisted AND u.deleted_at IS NULL
  AND NOT v.is_sensitive AND cardinality(v.sensitive_flags) = 0
  AND NOT EXISTS (SELECT 1 FROM user_bans b WHERE b.user_id = u.id)
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM video_passwords p WHERE p.video_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM video_drm_keys k WHERE k.video_id = v.id)
  AND sp.state = 'ready' AND v.transcode_generation > 0
  AND sp.master_key = 'streaming-playlists/' || v.id::text || '/r' ||
                      v.transcode_generation::text || '/master.m3u8';

REVOKE ALL ON ipfs_public_reference_videos FROM PUBLIC;
COMMENT ON VIEW ipfs_public_reference_videos IS
    'Live candidates for individually approved public S3 references. The publisher separately requires operator approval for the exact video and generation and verifies the storage objects.';

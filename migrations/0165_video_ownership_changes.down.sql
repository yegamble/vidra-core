-- Revert 0165. Pending and answered ownership requests are discarded; accepted
-- ones already moved their video, and that move stays (it is just a channel_id).
-- Notifications of type 'video_ownership_request' survive as plain rows.
DROP TABLE IF EXISTS video_ownership_changes;

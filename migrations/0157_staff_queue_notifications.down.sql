-- Revert 0157. The notification rows of the three staff types survive as plain
-- notifications (minus their subject columns); without the indexes a re-fired
-- fan-out could double-notify, the pre-0157 state of the world.
DROP INDEX IF EXISTS notifications_federation_follower_request_unique_idx;
DROP INDEX IF EXISTS notifications_upload_quarantined_unique_idx;
DROP INDEX IF EXISTS notifications_new_registration_request_unique_idx;
ALTER TABLE notifications
    DROP COLUMN IF EXISTS remote_follow_id,
    DROP COLUMN IF EXISTS registration_request_id;

ALTER TABLE federation_deliveries
    DROP COLUMN IF EXISTS signing_user_id,
    DROP COLUMN IF EXISTS signing_username;
DROP TABLE IF EXISTS remote_channel_follows;

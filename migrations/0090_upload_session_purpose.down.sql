-- Down 0090: drop the upload-session purpose column (video file replacement, W14).
ALTER TABLE upload_sessions
    DROP COLUMN IF EXISTS purpose;

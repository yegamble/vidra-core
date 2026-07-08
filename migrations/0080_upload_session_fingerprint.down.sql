-- Down 0080: drop the fingerprint column + its partial resume index (W2.C2).
DROP INDEX IF EXISTS upload_sessions_user_fingerprint_idx;
ALTER TABLE upload_sessions
    DROP COLUMN IF EXISTS file_fingerprint;

-- +goose Up
-- +goose StatementBegin
ALTER TABLE upload_sessions
ADD COLUMN IF NOT EXISTS file_fingerprint TEXT;

CREATE INDEX IF NOT EXISTS idx_upload_sessions_user_fingerprint
ON upload_sessions (user_id, file_fingerprint)
WHERE file_fingerprint IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_upload_sessions_user_fingerprint;
ALTER TABLE upload_sessions DROP COLUMN IF EXISTS file_fingerprint;
-- +goose StatementEnd

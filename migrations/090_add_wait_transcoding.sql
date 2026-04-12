-- +goose Up
ALTER TABLE videos ADD COLUMN IF NOT EXISTS wait_transcoding BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE videos DROP COLUMN IF EXISTS wait_transcoding;

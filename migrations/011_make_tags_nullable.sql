-- +goose Up
-- +goose StatementBegin
-- Migration to make tags column nullable in videos table
-- This allows video creation without explicitly providing tags

ALTER TABLE videos ALTER COLUMN tags DROP NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

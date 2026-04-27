-- +goose Up
-- +goose StatementBegin
-- Phase 10 — Live chat slow-mode.
-- Adds a per-stream cooldown enforced by the chat WS hub. 0 = disabled.
-- Capped at 600 seconds (10 minutes) at the application layer; the column
-- itself is just an INT so future widenings don't require a migration.
ALTER TABLE live_streams
ADD COLUMN IF NOT EXISTS slow_mode_seconds INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE live_streams DROP COLUMN IF EXISTS slow_mode_seconds;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin
-- Add approval workflow columns to comments table.
ALTER TABLE comments ADD COLUMN IF NOT EXISTS held_for_review BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS approved BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX idx_comments_held_for_review ON comments(held_for_review) WHERE held_for_review = true;
CREATE INDEX idx_comments_approved ON comments(approved) WHERE approved = false;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS idx_comments_approved;
DROP INDEX IF EXISTS idx_comments_held_for_review;
ALTER TABLE comments DROP COLUMN IF EXISTS approved;
ALTER TABLE comments DROP COLUMN IF EXISTS held_for_review;

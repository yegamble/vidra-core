-- +goose Up
-- +goose StatementBegin
-- Add bitcoin wallet address field to users table
ALTER TABLE users ADD COLUMN bitcoin_wallet VARCHAR(62);

-- Create index for bitcoin wallet queries
CREATE INDEX idx_users_bitcoin_wallet ON users(bitcoin_wallet);
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

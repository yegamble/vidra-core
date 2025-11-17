-- Add bitcoin wallet address field to users table
ALTER TABLE users ADD COLUMN bitcoin_wallet VARCHAR(62);

-- Create index for bitcoin wallet queries
CREATE INDEX idx_users_bitcoin_wallet ON users(bitcoin_wallet);
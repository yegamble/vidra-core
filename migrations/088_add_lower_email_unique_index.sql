-- +goose Up
-- PeerTube v7.0 parity: case-insensitive email matching
-- Adds a UNIQUE functional index on LOWER(email) to prevent
-- duplicate registrations with case variants and enable efficient
-- case-insensitive lookups.

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_lower_email ON users (LOWER(email));

-- +goose Down
DROP INDEX IF EXISTS idx_users_lower_email;

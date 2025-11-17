-- +goose Up
-- +goose StatementBegin
-- Create oauth_clients table for minimal OAuth2 client storage
-- Allows password and refresh_token grants with client authentication

BEGIN;

CREATE TABLE IF NOT EXISTS oauth_clients (
    id UUID PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE,
    -- Store bcrypt-hashed secret; empty/NULL implies public client (discouraged)
    client_secret_hash TEXT,
    name TEXT NOT NULL,
    grant_types TEXT[] NOT NULL DEFAULT ARRAY['password','refresh_token'],
    scopes TEXT[] NOT NULL DEFAULT ARRAY['basic'],
    redirect_uris TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_confidential BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS idx_oauth_clients_client_id ON oauth_clients(client_id);

-- Trigger to update updated_at
CREATE OR REPLACE FUNCTION set_updated_at_oauth_clients()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_oauth_clients_updated_at ON oauth_clients;
CREATE TRIGGER trg_oauth_clients_updated_at
BEFORE UPDATE ON oauth_clients
FOR EACH ROW EXECUTE FUNCTION set_updated_at_oauth_clients();

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

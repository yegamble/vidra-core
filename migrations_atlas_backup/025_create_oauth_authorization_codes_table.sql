-- Create oauth_authorization_codes table for OAuth2 Authorization Code flow
-- Supports PKCE (code_challenge, code_challenge_method) and scopes

BEGIN;

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    client_id TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    scope TEXT,
    state TEXT,
    -- PKCE fields
    code_challenge TEXT,
    code_challenge_method TEXT,
    -- Expiry and usage
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_oauth_codes_code ON oauth_authorization_codes(code);
CREATE INDEX idx_oauth_codes_expires ON oauth_authorization_codes(expires_at);
CREATE INDEX idx_oauth_codes_user_id ON oauth_authorization_codes(user_id);

-- Table for storing access tokens (for revocation and introspection)
CREATE TABLE IF NOT EXISTS oauth_access_tokens (
    id UUID PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE, -- Store SHA256 hash of token
    client_id TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_oauth_tokens_hash ON oauth_access_tokens(token_hash);
CREATE INDEX idx_oauth_tokens_user_id ON oauth_access_tokens(user_id);
CREATE INDEX idx_oauth_tokens_expires ON oauth_access_tokens(expires_at);

-- Add scope column to oauth_clients if not exists
ALTER TABLE oauth_clients ADD COLUMN IF NOT EXISTS allowed_scopes TEXT[] NOT NULL DEFAULT ARRAY['basic'];

COMMIT;

-- Secure storage for ATProto session tokens

CREATE TABLE IF NOT EXISTS atproto_sessions (
    id INTEGER PRIMARY KEY DEFAULT 1,
    access_jwt_enc BYTEA,
    access_nonce BYTEA,
    refresh_jwt_enc BYTEA,
    refresh_nonce BYTEA,
    did TEXT,
    fetched_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE OR REPLACE FUNCTION update_atproto_sessions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_update_atproto_sessions_updated_at ON atproto_sessions;
CREATE TRIGGER trg_update_atproto_sessions_updated_at
    BEFORE UPDATE ON atproto_sessions
    FOR EACH ROW EXECUTE FUNCTION update_atproto_sessions_updated_at();

-- +goose Up
-- +goose StatementBegin
-- Add ATProto federation fields to channels and instance config

-- Channels: add optional ATProto identifiers
ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS atproto_did VARCHAR(128),
    ADD COLUMN IF NOT EXISTS atproto_pds_url VARCHAR(500);

-- Ensure DID uniqueness when present
CREATE UNIQUE INDEX IF NOT EXISTS uq_channels_atproto_did
    ON channels((LOWER(atproto_did)))
    WHERE atproto_did IS NOT NULL AND atproto_did <> '';

-- Document columns
COMMENT ON COLUMN channels.atproto_did IS 'ATProto DID for federation (e.g., did:plc:...)';
COMMENT ON COLUMN channels.atproto_pds_url IS 'Optional ATProto PDS base URL for the DID';

-- Instance configuration additions for ATProto
-- Key to serve from /.well-known/atproto-did
INSERT INTO instance_config (key, value, description, is_public)
VALUES (
    'atproto_did', '""'::jsonb, 'Instance DID served at /.well-known/atproto-did', true
)
ON CONFLICT (key) DO NOTHING;

-- Optional: configured PDS base URL for outgoing XRPC calls
INSERT INTO instance_config (key, value, description, is_public)
VALUES (
    'atproto_pds_url', '""'::jsonb, 'Default ATProto PDS base URL for this instance', false
)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety

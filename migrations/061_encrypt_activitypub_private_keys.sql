-- +goose Up
-- +goose StatementBegin
-- Migration to prepare for encrypted ActivityPub private keys
-- This migration adds metadata to track encryption status

-- Add a column to track whether keys are encrypted
-- This helps during the migration process
ALTER TABLE ap_actor_keys
ADD COLUMN IF NOT EXISTS keys_encrypted BOOLEAN DEFAULT FALSE;

-- Create an index for faster lookups during migration
CREATE INDEX IF NOT EXISTS idx_ap_actor_keys_encrypted
ON ap_actor_keys(keys_encrypted)
WHERE keys_encrypted = FALSE;

-- Add a comment to the table
COMMENT ON TABLE ap_actor_keys IS 'Stores ActivityPub actor key pairs. Private keys are encrypted at rest using AES-256-GCM.';
COMMENT ON COLUMN ap_actor_keys.private_key_pem IS 'Encrypted private key (AES-256-GCM encrypted, base64 encoded)';
COMMENT ON COLUMN ap_actor_keys.keys_encrypted IS 'Indicates whether the private key is encrypted (for migration tracking)';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Drop the encryption tracking column if needed
ALTER TABLE ap_actor_keys DROP COLUMN IF EXISTS keys_encrypted;
DROP INDEX IF EXISTS idx_ap_actor_keys_encrypted;
-- +goose StatementEnd

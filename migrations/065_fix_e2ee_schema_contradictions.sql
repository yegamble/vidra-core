-- +goose Up
-- +goose StatementBegin
-- Fix E2EE schema contradictions identified in security review (MSG-SEC-003, MSG-SEC-004)
-- and prepare user_signing_keys for true client-side E2EE.

-- -----------------------------------------------------------------------
-- MSG-SEC-003: messages.content NOT NULL contradicts E2EE requirement
-- -----------------------------------------------------------------------

-- Drop the contradictory check_encrypted_content constraint from migration 016.
-- It requires content IS NULL when is_encrypted=true, but content was NOT NULL.
ALTER TABLE messages DROP CONSTRAINT IF EXISTS check_encrypted_content;

-- Allow content to be NULL (required for encrypted messages where no plaintext exists)
ALTER TABLE messages ALTER COLUMN content DROP NOT NULL;

-- Drop old message_type constraint and add corrected version including 'secure' and 'key_exchange'
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_message_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_message_type_check
    CHECK (message_type IN ('text', 'system', 'secure', 'key_exchange'));

-- Re-add the corrected E2EE content constraint:
--   encrypted messages: encrypted_content and content_nonce must be set, content may be NULL
--   plain messages: content must be set, encrypted_content and content_nonce must be NULL
ALTER TABLE messages ADD CONSTRAINT check_encrypted_content
    CHECK (
        (is_encrypted = true  AND encrypted_content IS NOT NULL AND content_nonce IS NOT NULL) OR
        (is_encrypted = false AND content IS NOT NULL AND encrypted_content IS NULL AND content_nonce IS NULL)
    );

-- -----------------------------------------------------------------------
-- MSG-SEC-004: conversation state machine violates DB constraint
-- The old constraint required is_encrypted=true => key_exchange_complete=true,
-- which prevented the key exchange initiation step (is_encrypted=true, key_exchange_complete=false).
-- Replace with a proper state machine column.
-- -----------------------------------------------------------------------

-- Drop the contradictory constraint
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS check_encrypted_key_exchange;

-- Add encryption_status state machine column (replaces boolean pair)
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS encryption_status
    VARCHAR(20) NOT NULL DEFAULT 'none'
    CHECK (encryption_status IN ('none', 'pending', 'active'));

-- Backfill encryption_status from existing boolean columns for any existing rows
UPDATE conversations
SET encryption_status = CASE
    WHEN is_encrypted = true AND key_exchange_complete = true THEN 'active'
    WHEN is_encrypted = true AND key_exchange_complete = false THEN 'pending'
    ELSE 'none'
END;

-- Add index for efficient status filtering
CREATE INDEX IF NOT EXISTS idx_conversations_encryption_status
    ON conversations(encryption_status)
    WHERE encryption_status != 'none';

-- -----------------------------------------------------------------------
-- user_signing_keys: prepare for true client-side E2EE
-- In server-mediated model, server held encrypted private keys.
-- In true E2EE model, server only stores public keys.
-- -----------------------------------------------------------------------

-- Make encrypted_private_key nullable (server never holds private keys in true E2EE)
ALTER TABLE user_signing_keys ALTER COLUMN encrypted_private_key DROP NOT NULL;

-- Add X25519 identity public key column for ECDH key exchange
-- (separate from the Ed25519 signing public key already in public_key column)
ALTER TABLE user_signing_keys ADD COLUMN IF NOT EXISTS public_identity_key TEXT;

-- +goose StatementEnd

-- +goose Down
-- NOTE: This project uses forward-only migrations for safety.
-- Rollback is intentionally a no-op. To revert, create a compensating migration.

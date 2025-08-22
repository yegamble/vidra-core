-- Add PGP public key support to users table for secure messaging
ALTER TABLE users ADD COLUMN pgp_public_key TEXT;

-- Add secure mode to conversations
ALTER TABLE conversations ADD COLUMN is_secure_mode BOOLEAN NOT NULL DEFAULT false;

-- Add encrypted content field to messages for PGP encrypted messages
ALTER TABLE messages ADD COLUMN encrypted_content TEXT;

-- Add PGP signature field for message integrity verification
ALTER TABLE messages ADD COLUMN pgp_signature TEXT;

-- Add index for users with PGP keys
CREATE INDEX idx_users_pgp_public_key ON users(pgp_public_key) WHERE pgp_public_key IS NOT NULL;

-- Add index for secure conversations
CREATE INDEX idx_conversations_secure_mode ON conversations(is_secure_mode) WHERE is_secure_mode = true;

-- Add constraint to ensure secure messages have encrypted content
ALTER TABLE messages ADD CONSTRAINT check_secure_message_content 
    CHECK (
        (encrypted_content IS NULL AND pgp_signature IS NULL) OR 
        (encrypted_content IS NOT NULL AND pgp_signature IS NOT NULL)
    );
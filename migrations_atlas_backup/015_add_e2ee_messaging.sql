-- Add E2EE messaging support with comprehensive security
-- Uses industry-standard cryptographic patterns

-- Add encryption fields to messages table
ALTER TABLE messages ADD COLUMN encrypted_content TEXT;
ALTER TABLE messages ADD COLUMN content_nonce TEXT; -- 24-byte XChaCha20Poly1305 nonce (base64)
ALTER TABLE messages ADD COLUMN pgp_signature TEXT;
ALTER TABLE messages ADD COLUMN is_encrypted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE messages ADD COLUMN encryption_version INTEGER DEFAULT 1; -- For future algorithm upgrades

-- Add encryption support to conversations
ALTER TABLE conversations ADD COLUMN is_encrypted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE conversations ADD COLUMN key_exchange_complete BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE conversations ADD COLUMN encryption_version INTEGER DEFAULT 1;
ALTER TABLE conversations ADD COLUMN last_key_rotation TIMESTAMP WITH TIME ZONE;

-- User master keys for encrypting conversation keys
-- Uses Argon2id for password-based key derivation
CREATE TABLE user_master_keys (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    encrypted_master_key TEXT NOT NULL, -- Master key encrypted with Argon2id-derived key
    argon2_salt TEXT NOT NULL, -- 32-byte salt for Argon2id (base64)
    argon2_memory INTEGER NOT NULL DEFAULT 65536, -- 64MB memory cost
    argon2_time INTEGER NOT NULL DEFAULT 3, -- 3 iterations
    argon2_parallelism INTEGER NOT NULL DEFAULT 4, -- 4 threads
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Per-conversation encryption keys
-- Each user has their own encrypted copy of the shared secret
CREATE TABLE conversation_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- X25519 key pair for ECDH key exchange
    encrypted_private_key TEXT NOT NULL, -- Encrypted with user's master key
    public_key TEXT NOT NULL, -- X25519 public key (base64, 32 bytes)

    -- Derived shared secret (encrypted)
    encrypted_shared_secret TEXT, -- ChaCha20Poly1305 key encrypted with master key

    key_version INTEGER NOT NULL DEFAULT 1,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE, -- For key rotation

    UNIQUE(conversation_id, user_id, key_version)
);

-- Key exchange messages for secure handshake
CREATE TABLE key_exchange_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    exchange_type VARCHAR(20) NOT NULL CHECK (exchange_type IN ('offer', 'accept', 'confirm')),
    public_key TEXT NOT NULL, -- X25519 public key
    signature TEXT NOT NULL, -- Ed25519 signature for authenticity
    nonce TEXT NOT NULL, -- Unique nonce to prevent replay

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (NOW() + INTERVAL '1 hour')
);

-- User signing keys for message authenticity (Ed25519)
CREATE TABLE user_signing_keys (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    encrypted_private_key TEXT NOT NULL, -- Ed25519 private key encrypted with master key
    public_key TEXT NOT NULL, -- Ed25519 public key (base64, 32 bytes)
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Audit log for cryptographic operations
CREATE TABLE crypto_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id UUID REFERENCES conversations(id) ON DELETE CASCADE,
    operation VARCHAR(50) NOT NULL, -- 'key_generation', 'key_exchange', 'encryption', 'decryption', 'key_rotation'
    success BOOLEAN NOT NULL,
    error_message TEXT,
    client_ip INET,
    user_agent TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX idx_conversation_keys_conversation_user ON conversation_keys(conversation_id, user_id);
CREATE INDEX idx_conversation_keys_active ON conversation_keys(is_active) WHERE is_active = true;
CREATE INDEX idx_conversation_keys_expires ON conversation_keys(expires_at) WHERE expires_at IS NOT NULL;

CREATE INDEX idx_key_exchange_conversation ON key_exchange_messages(conversation_id);
CREATE INDEX idx_key_exchange_expires ON key_exchange_messages(expires_at);
CREATE INDEX idx_key_exchange_sender_recipient ON key_exchange_messages(sender_id, recipient_id);

CREATE INDEX idx_messages_encrypted ON messages(is_encrypted) WHERE is_encrypted = true;
CREATE INDEX idx_conversations_encrypted ON conversations(is_encrypted) WHERE is_encrypted = true;

CREATE INDEX idx_crypto_audit_user ON crypto_audit_log(user_id);
CREATE INDEX idx_crypto_audit_conversation ON crypto_audit_log(conversation_id);
CREATE INDEX idx_crypto_audit_created ON crypto_audit_log(created_at);

-- Update triggers for timestamp management
CREATE TRIGGER update_user_master_keys_updated_at
    BEFORE UPDATE ON user_master_keys
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Constraints for security
ALTER TABLE messages ADD CONSTRAINT check_encrypted_content
    CHECK ((is_encrypted = true AND encrypted_content IS NOT NULL AND content_nonce IS NOT NULL AND content IS NULL) OR
           (is_encrypted = false AND encrypted_content IS NULL AND content_nonce IS NULL AND content IS NOT NULL));

-- Function to clean up expired key exchange messages
CREATE OR REPLACE FUNCTION cleanup_expired_key_exchanges()
RETURNS void AS $$
BEGIN
    DELETE FROM key_exchange_messages WHERE expires_at < NOW();
END;
$$ LANGUAGE plpgsql;

-- Function to rotate conversation keys (called periodically)
CREATE OR REPLACE FUNCTION mark_keys_for_rotation()
RETURNS void AS $$
BEGIN
    UPDATE conversation_keys
    SET is_active = false
    WHERE created_at < NOW() - INTERVAL '30 days'
    AND expires_at IS NULL;
END;
$$ LANGUAGE plpgsql;

-- Security policies and additional constraints
ALTER TABLE user_master_keys ADD CONSTRAINT check_argon2_params
    CHECK (argon2_memory >= 32768 AND argon2_time >= 2 AND argon2_parallelism >= 1);

-- Ensure encrypted conversations have proper key exchange
ALTER TABLE conversations ADD CONSTRAINT check_encrypted_key_exchange
    CHECK ((is_encrypted = false) OR (is_encrypted = true AND key_exchange_complete = true));

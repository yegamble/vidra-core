-- Add Two-Factor Authentication support to users table
ALTER TABLE users
ADD COLUMN twofa_enabled BOOLEAN NOT NULL DEFAULT false,
ADD COLUMN twofa_secret TEXT,
ADD COLUMN twofa_confirmed_at TIMESTAMP WITH TIME ZONE;

-- Create index for 2FA lookups (commonly queried during login)
CREATE INDEX idx_users_twofa_enabled ON users(twofa_enabled) WHERE twofa_enabled = true;

-- Create backup codes table for 2FA recovery
CREATE TABLE twofa_backup_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for backup codes
CREATE INDEX idx_twofa_backup_codes_user_id ON twofa_backup_codes(user_id);
CREATE INDEX idx_twofa_backup_codes_unused ON twofa_backup_codes(user_id, used_at) WHERE used_at IS NULL;

-- Add comments for documentation
COMMENT ON COLUMN users.twofa_enabled IS 'Whether two-factor authentication is enabled for this user';
COMMENT ON COLUMN users.twofa_secret IS 'Encrypted TOTP secret for two-factor authentication (base32 encoded)';
COMMENT ON COLUMN users.twofa_confirmed_at IS 'Timestamp when 2FA was successfully confirmed/enabled';
COMMENT ON TABLE twofa_backup_codes IS 'One-time backup codes for 2FA recovery (hashed with bcrypt)';
COMMENT ON COLUMN twofa_backup_codes.code_hash IS 'Bcrypt hash of the backup code';
COMMENT ON COLUMN twofa_backup_codes.used_at IS 'Timestamp when this backup code was used (NULL if unused)';

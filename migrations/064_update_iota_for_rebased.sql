-- +goose Up
-- +goose StatementBegin
-- Migrate IOTA tables from Stardust protocol to IOTA Rebased (May 2025).
-- Changes:
--   iota_wallets:     rename encrypted_seed/seed_nonce to encrypted_private_key/private_key_nonce,
--                     add public_key column for Ed25519 public key storage.
--   iota_transactions: rename transaction_hash to transaction_digest (IOTA Rebased terminology),
--                      add gas_budget and gas_used for Move-based transaction tracking.
--   iota_payment_intents: no schema changes needed; payment_address VARCHAR(90) is sufficient
--                         for 66-char hex addresses (0x + 64 hex chars).

-- iota_wallets: rename seed fields to private key fields
ALTER TABLE iota_wallets
    RENAME COLUMN encrypted_seed TO encrypted_private_key;

ALTER TABLE iota_wallets
    RENAME COLUMN seed_nonce TO private_key_nonce;

-- iota_wallets: add public_key column (hex-encoded Ed25519 public key, 0x + 64 hex chars)
ALTER TABLE iota_wallets
    ADD COLUMN public_key VARCHAR(66);

-- iota_transactions: rename transaction_hash to transaction_digest
ALTER TABLE iota_transactions
    RENAME COLUMN transaction_hash TO transaction_digest;

-- iota_transactions: add gas columns for IOTA Rebased programmable transaction blocks
ALTER TABLE iota_transactions
    ADD COLUMN gas_budget BIGINT,
    ADD COLUMN gas_used BIGINT;

-- Update existing index on transaction_hash to use new column name
DROP INDEX IF EXISTS idx_iota_transactions_transaction_hash;
CREATE UNIQUE INDEX idx_iota_transactions_transaction_digest ON iota_transactions(transaction_digest);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert: restore Stardust-era schema

-- iota_transactions: remove gas columns and restore index name
DROP INDEX IF EXISTS idx_iota_transactions_transaction_digest;

ALTER TABLE iota_transactions
    DROP COLUMN IF EXISTS gas_budget,
    DROP COLUMN IF EXISTS gas_used;

ALTER TABLE iota_transactions
    RENAME COLUMN transaction_digest TO transaction_hash;

CREATE UNIQUE INDEX idx_iota_transactions_transaction_hash ON iota_transactions(transaction_hash);

-- iota_wallets: remove public_key column and restore seed field names
ALTER TABLE iota_wallets
    DROP COLUMN IF EXISTS public_key;

ALTER TABLE iota_wallets
    RENAME COLUMN private_key_nonce TO seed_nonce;

ALTER TABLE iota_wallets
    RENAME COLUMN encrypted_private_key TO encrypted_seed;

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin
-- IOTA Wallets table
CREATE TABLE IF NOT EXISTS iota_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    encrypted_seed BYTEA NOT NULL, -- AES-256-GCM encrypted seed
    seed_nonce BYTEA NOT NULL, -- Nonce for AES-GCM encryption
    address VARCHAR(90) NOT NULL UNIQUE, -- IOTA addresses can be up to 90 chars
    balance_iota BIGINT NOT NULL DEFAULT 0, -- Balance in IOTA base units (i)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_wallets_user_id ON iota_wallets(user_id);
CREATE INDEX idx_iota_wallets_address ON iota_wallets(address);

-- IOTA Payment Intents table
CREATE TABLE IF NOT EXISTS iota_payment_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id UUID REFERENCES videos(id) ON DELETE SET NULL,
    amount_iota BIGINT NOT NULL CHECK (amount_iota > 0), -- Amount in IOTA base units
    payment_address VARCHAR(90) NOT NULL, -- Address to send payment to
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'expired', 'refunded')),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    paid_at TIMESTAMP WITH TIME ZONE,
    transaction_id UUID,
    metadata JSONB, -- Additional metadata (e.g., product info, user notes)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_payment_intents_user_id ON iota_payment_intents(user_id);
CREATE INDEX idx_iota_payment_intents_video_id ON iota_payment_intents(video_id);
CREATE INDEX idx_iota_payment_intents_status ON iota_payment_intents(status);
CREATE INDEX idx_iota_payment_intents_expires_at ON iota_payment_intents(expires_at);
CREATE INDEX idx_iota_payment_intents_payment_address ON iota_payment_intents(payment_address);

-- IOTA Transactions table
CREATE TABLE IF NOT EXISTS iota_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID REFERENCES iota_wallets(id) ON DELETE SET NULL,
    transaction_hash VARCHAR(81) NOT NULL UNIQUE, -- IOTA transaction hash (64 chars hex + prefix)
    amount_iota BIGINT NOT NULL, -- Amount in IOTA base units
    tx_type VARCHAR(20) NOT NULL CHECK (tx_type IN ('deposit', 'withdrawal', 'payment')),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    confirmations INTEGER NOT NULL DEFAULT 0,
    from_address VARCHAR(90),
    to_address VARCHAR(90),
    metadata JSONB, -- Additional transaction metadata
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_transactions_wallet_id ON iota_transactions(wallet_id);
CREATE INDEX idx_iota_transactions_transaction_hash ON iota_transactions(transaction_hash);
CREATE INDEX idx_iota_transactions_status ON iota_transactions(status);
CREATE INDEX idx_iota_transactions_tx_type ON iota_transactions(tx_type);
CREATE INDEX idx_iota_transactions_created_at ON iota_transactions(created_at DESC);

-- Add foreign key from iota_payment_intents to iota_transactions
ALTER TABLE iota_payment_intents
ADD CONSTRAINT fk_iota_payment_intents_transaction
FOREIGN KEY (transaction_id) REFERENCES iota_transactions(id) ON DELETE SET NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS iota_payment_intents CASCADE;
DROP TABLE IF EXISTS iota_transactions CASCADE;
DROP TABLE IF EXISTS iota_wallets CASCADE;
-- +goose StatementEnd

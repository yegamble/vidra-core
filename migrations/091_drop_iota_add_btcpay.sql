-- +goose Up
-- +goose StatementBegin

-- Drop IOTA tables (order matters due to foreign key constraints)
ALTER TABLE iota_payment_intents DROP CONSTRAINT IF EXISTS fk_iota_payment_intents_transaction;
DROP TABLE IF EXISTS iota_transactions CASCADE;
DROP TABLE IF EXISTS iota_payment_intents CASCADE;
DROP TABLE IF EXISTS iota_wallets CASCADE;

-- BTCPay Invoices table
CREATE TABLE IF NOT EXISTS btcpay_invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    btcpay_invoice_id VARCHAR(255) NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_sats BIGINT NOT NULL CHECK (amount_sats > 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'BTC',
    status VARCHAR(20) NOT NULL DEFAULT 'New' CHECK (status IN ('New', 'Processing', 'Settled', 'Invalid', 'Expired')),
    btcpay_checkout_link TEXT NOT NULL DEFAULT '',
    bitcoin_address VARCHAR(62),
    metadata JSONB,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    settled_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_btcpay_invoices_user_id ON btcpay_invoices(user_id);
CREATE INDEX idx_btcpay_invoices_btcpay_invoice_id ON btcpay_invoices(btcpay_invoice_id);
CREATE INDEX idx_btcpay_invoices_status ON btcpay_invoices(status);
CREATE INDEX idx_btcpay_invoices_expires_at ON btcpay_invoices(expires_at);

-- BTCPay Payments table (individual payments against an invoice)
CREATE TABLE IF NOT EXISTS btcpay_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id UUID NOT NULL REFERENCES btcpay_invoices(id) ON DELETE CASCADE,
    btcpay_payment_id VARCHAR(255) NOT NULL DEFAULT '',
    amount_sats BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    transaction_id VARCHAR(255) NOT NULL DEFAULT '',
    block_height BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_btcpay_payments_invoice_id ON btcpay_payments(invoice_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop BTCPay tables
DROP TABLE IF EXISTS btcpay_payments CASCADE;
DROP TABLE IF EXISTS btcpay_invoices CASCADE;

-- Recreate IOTA tables (from migration 062)
CREATE TABLE IF NOT EXISTS iota_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    encrypted_seed BYTEA NOT NULL,
    seed_nonce BYTEA NOT NULL,
    address VARCHAR(90) NOT NULL UNIQUE,
    balance_iota BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_wallets_user_id ON iota_wallets(user_id);
CREATE INDEX idx_iota_wallets_address ON iota_wallets(address);

CREATE TABLE IF NOT EXISTS iota_payment_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id UUID REFERENCES videos(id) ON DELETE SET NULL,
    amount_iota BIGINT NOT NULL CHECK (amount_iota > 0),
    payment_address VARCHAR(90) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'expired', 'refunded')),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    paid_at TIMESTAMP WITH TIME ZONE,
    transaction_id UUID,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_payment_intents_user_id ON iota_payment_intents(user_id);
CREATE INDEX idx_iota_payment_intents_video_id ON iota_payment_intents(video_id);
CREATE INDEX idx_iota_payment_intents_status ON iota_payment_intents(status);
CREATE INDEX idx_iota_payment_intents_expires_at ON iota_payment_intents(expires_at);
CREATE INDEX idx_iota_payment_intents_payment_address ON iota_payment_intents(payment_address);

CREATE TABLE IF NOT EXISTS iota_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID REFERENCES iota_wallets(id) ON DELETE SET NULL,
    transaction_hash VARCHAR(81) NOT NULL UNIQUE,
    amount_iota BIGINT NOT NULL,
    tx_type VARCHAR(20) NOT NULL CHECK (tx_type IN ('deposit', 'withdrawal', 'payment')),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    confirmations INTEGER NOT NULL DEFAULT 0,
    from_address VARCHAR(90),
    to_address VARCHAR(90),
    metadata JSONB,
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_iota_transactions_wallet_id ON iota_transactions(wallet_id);
CREATE INDEX idx_iota_transactions_transaction_hash ON iota_transactions(transaction_hash);
CREATE INDEX idx_iota_transactions_status ON iota_transactions(status);
CREATE INDEX idx_iota_transactions_tx_type ON iota_transactions(tx_type);
CREATE INDEX idx_iota_transactions_created_at ON iota_transactions(created_at DESC);

ALTER TABLE iota_payment_intents
ADD CONSTRAINT fk_iota_payment_intents_transaction
FOREIGN KEY (transaction_id) REFERENCES iota_transactions(id) ON DELETE SET NULL;

-- +goose StatementEnd

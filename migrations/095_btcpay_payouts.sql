-- +goose Up
-- +goose StatementBegin

-- BTCPay Payouts: state machine for creator-requested withdrawals.
-- Admin approves EVERY payout (no hot-wallet automation). On-chain OR Lightning (BOLT11).
-- Valid transitions:
--   pending   -> approved  (admin)
--   pending   -> rejected  (admin, with reason)
--   pending   -> cancelled (creator, self only)
--   approved  -> completed (admin marks executed, records txid/LN payment hash)
--   approved  -> rejected  (admin changes mind before ops executes)
-- All transitions use conditional UPDATE with WHERE status=<current> — zero rows = 409 Conflict.

CREATE TYPE payout_status AS ENUM (
    'pending',
    'approved',
    'completed',
    'rejected',
    'cancelled'
);

CREATE TYPE payout_destination_type AS ENUM (
    'on_chain',
    'lightning_bolt11'
);

CREATE TABLE btcpay_payouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_sats BIGINT NOT NULL CHECK (amount_sats > 0),
    destination TEXT NOT NULL,
    destination_type payout_destination_type NOT NULL,
    status payout_status NOT NULL DEFAULT 'pending',
    auto_trigger BOOLEAN NOT NULL DEFAULT FALSE,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at TIMESTAMPTZ NULL,
    approved_by_admin_id UUID NULL REFERENCES users(id),
    executed_at TIMESTAMPTZ NULL,
    txid TEXT NULL,
    rejection_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_btcpay_payouts_requester_status ON btcpay_payouts(requester_user_id, status);
CREATE INDEX idx_btcpay_payouts_status_requested ON btcpay_payouts(status, requested_at);

-- Wire payment_ledger.payout_id to btcpay_payouts.id (table created in 094 with NULL payout_id).
ALTER TABLE payment_ledger
    ADD CONSTRAINT fk_payment_ledger_payout
    FOREIGN KEY (payout_id) REFERENCES btcpay_payouts(id) ON DELETE SET NULL;

COMMENT ON TABLE btcpay_payouts IS
    'Creator-requested withdrawals. Admin approves every payout. See payment_ledger for the financial record.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE payment_ledger DROP CONSTRAINT IF EXISTS fk_payment_ledger_payout;
DROP TABLE IF EXISTS btcpay_payouts CASCADE;
DROP TYPE IF EXISTS payout_destination_type;
DROP TYPE IF EXISTS payout_status;

-- +goose StatementEnd

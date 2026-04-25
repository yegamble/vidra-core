-- +goose Up
-- +goose StatementBegin

-- Payment Ledger: authoritative money-movement store for vidra payments.
-- Every movement (tip, payout reservation, payout compensation, subscription) writes exactly one row.
-- available_sats for a user = SUM(amount_sats) WHERE user_id = <user>.
-- Reservations are stored as negative amounts (e.g., payout_requested = -X).
-- Compensations (payout_rejected, payout_cancelled) are positive and restore balance.
-- An idempotency_key UNIQUE index prevents double-writes under webhook retry / admin double-click.

-- Entry types. NOTE: 'payout_approved' intentionally NOT here — approval is a state transition
-- on btcpay_payouts, not a money movement. See 095 for the payout state machine.
CREATE TYPE ledger_entry_type AS ENUM (
    'tip_in',
    'tip_out',
    'payout_requested',
    'payout_completed',
    'payout_rejected',
    'payout_cancelled',
    'subscription_in'
);

CREATE TABLE payment_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    counterparty_user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    channel_id UUID NULL REFERENCES channels(id) ON DELETE SET NULL,
    entry_type ledger_entry_type NOT NULL,
    amount_sats BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'BTC',
    invoice_id UUID NULL REFERENCES btcpay_invoices(id) ON DELETE SET NULL,
    payout_id UUID NULL, -- FK added by 095
    metadata JSONB,
    idempotency_key VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_payment_ledger_idempotency ON payment_ledger(idempotency_key);
CREATE INDEX idx_payment_ledger_user ON payment_ledger(user_id, created_at DESC);
CREATE INDEX idx_payment_ledger_counterparty ON payment_ledger(counterparty_user_id) WHERE counterparty_user_id IS NOT NULL;
CREATE INDEX idx_payment_ledger_type ON payment_ledger(entry_type);
CREATE INDEX idx_payment_ledger_invoice ON payment_ledger(invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX idx_payment_ledger_payout ON payment_ledger(payout_id) WHERE payout_id IS NOT NULL;

COMMENT ON TABLE payment_ledger IS
    'Authoritative money-movement ledger. available_sats per user = SUM(amount_sats) WHERE user_id = <user>. '
    'Reservations are negative, compensations positive. idempotency_key prevents double-writes.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS payment_ledger CASCADE;
DROP TYPE IF EXISTS ledger_entry_type;

-- +goose StatementEnd

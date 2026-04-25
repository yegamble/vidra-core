-- +goose Up
-- +goose StatementBegin

-- Per-user, per-notification-type cooldown store used by the balance worker
-- (phase 8 Task 15). Prevents spamming low_balance_stuck / payout_ready
-- notifications when the worker ticks faster than 24h (e.g. under the debug
-- tick endpoint in tests).

CREATE TABLE payment_notification_cooldowns (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_type VARCHAR(50) NOT NULL,
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, notification_type)
);

CREATE INDEX idx_payment_notification_cooldowns_emitted
    ON payment_notification_cooldowns(emitted_at);

COMMENT ON TABLE payment_notification_cooldowns IS
    'Idempotency store for payment-event notifications. Worker upserts emitted_at only when '
    '(NOW() - existing.emitted_at) >= 24h. See usecase/payments/balance_worker.go.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS payment_notification_cooldowns CASCADE;

-- +goose StatementEnd

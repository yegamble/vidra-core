-- +goose Up
-- +goose StatementBegin

-- ONE-SHOT backfill. Populates payment_ledger from existing settled btcpay_invoices.
-- Idempotency: unique index on idempotency_key (see 094) makes ON CONFLICT DO NOTHING safe on re-run.
-- IMPORTANT: If post-backfill corrections are needed (e.g. a channel_id is added to metadata
-- AFTER this migration runs), do NOT re-run this migration expecting it to pick up the change —
-- write explicit compensating ledger entries instead. ON CONFLICT DO NOTHING would silently skip.

-- tip_out for every settled invoice where the payer (user_id) is known.
-- amount is negative — the payer parted with sats.
INSERT INTO payment_ledger (
    user_id,
    counterparty_user_id,
    channel_id,
    entry_type,
    amount_sats,
    currency,
    invoice_id,
    metadata,
    idempotency_key,
    created_at
)
SELECT
    i.user_id,
    c.account_id AS counterparty_user_id,
    (i.metadata->>'channel_id')::uuid AS channel_id,
    'tip_out'::ledger_entry_type,
    -i.amount_sats AS amount_sats,
    i.currency,
    i.id AS invoice_id,
    i.metadata,
    'invoice-' || i.id::text || '-tip_out' AS idempotency_key,
    COALESCE(i.settled_at, i.created_at) AS created_at
FROM btcpay_invoices i
LEFT JOIN channels c ON c.id = NULLIF(i.metadata->>'channel_id', '')::uuid
WHERE i.status = 'Settled'
  AND COALESCE(i.metadata->>'type', '') = 'tip'
ON CONFLICT (idempotency_key) DO NOTHING;

-- tip_in for every settled tip-type invoice with a resolvable channel owner.
-- amount is positive — the channel owner received sats.
INSERT INTO payment_ledger (
    user_id,
    counterparty_user_id,
    channel_id,
    entry_type,
    amount_sats,
    currency,
    invoice_id,
    metadata,
    idempotency_key,
    created_at
)
SELECT
    c.account_id AS user_id,
    i.user_id AS counterparty_user_id,
    c.id AS channel_id,
    'tip_in'::ledger_entry_type,
    i.amount_sats,
    i.currency,
    i.id AS invoice_id,
    i.metadata,
    'invoice-' || i.id::text || '-tip_in' AS idempotency_key,
    COALESCE(i.settled_at, i.created_at) AS created_at
FROM btcpay_invoices i
JOIN channels c ON c.id = NULLIF(i.metadata->>'channel_id', '')::uuid
WHERE i.status = 'Settled'
  AND COALESCE(i.metadata->>'type', '') = 'tip'
ON CONFLICT (idempotency_key) DO NOTHING;

-- Report backfill counts (visible in goose output).
DO $$
DECLARE
    tip_in_count  INTEGER;
    tip_out_count INTEGER;
    settled_count INTEGER;
    settled_with_channel INTEGER;
BEGIN
    SELECT COUNT(*) INTO tip_in_count  FROM payment_ledger WHERE entry_type = 'tip_in';
    SELECT COUNT(*) INTO tip_out_count FROM payment_ledger WHERE entry_type = 'tip_out';
    SELECT COUNT(*) INTO settled_count FROM btcpay_invoices WHERE status = 'Settled' AND COALESCE(metadata->>'type','') = 'tip';
    SELECT COUNT(*) INTO settled_with_channel
        FROM btcpay_invoices i
        WHERE i.status = 'Settled'
          AND COALESCE(i.metadata->>'type','') = 'tip'
          AND NULLIF(i.metadata->>'channel_id','') IS NOT NULL
          AND EXISTS (SELECT 1 FROM channels c WHERE c.id = (i.metadata->>'channel_id')::uuid);

    RAISE NOTICE '[096 backfill] tip_out rows: %, tip_in rows: %, settled tip invoices: %, resolvable channels: %',
        tip_out_count, tip_in_count, settled_count, settled_with_channel;

    -- Invariant: every resolvable-channel settled tip becomes one tip_in.
    IF settled_with_channel <> tip_in_count THEN
        RAISE EXCEPTION
          '[096 backfill] invariant violated: resolvable-channel settled tips (%) <> tip_in rows (%)',
          settled_with_channel, tip_in_count;
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down is effectively destructive for derivative ledger data: we delete only rows
-- that originated from btcpay_invoices backfill. Post-migration ledger writes
-- (from the LedgerService webhook path) are preserved because they use a
-- different idempotency_key prefix ('invoice-<uuid>-tip_in' is indistinguishable from
-- the same-shape key written by the live service — so the most conservative Down is
-- a no-op here; a fresh backfill can rebuild from settled invoices if needed).
-- Operators should prefer dropping the ledger table via 094's Down if they truly want a reset.

-- +goose StatementEnd

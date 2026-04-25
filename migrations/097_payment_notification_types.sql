-- +goose Up
-- +goose StatementBegin

-- notifications.type is a free-form VARCHAR(50) with no CHECK constraint (see 022).
-- This migration is a documentation-only marker so the phase-8 notification types
-- are traceable in the migration ledger. The Go domain layer (Task 2) defines
-- the canonical constants; inserting unknown values is not blocked at DB level.

DO $$
BEGIN
    RAISE NOTICE
      '[097] phase-8 notification types are canonical: '
      'tip_received, payout_pending_approval, payout_approved, '
      'payout_completed, payout_rejected, payout_ready, low_balance_stuck. '
      'notifications.type is VARCHAR(50) without CHECK; values are enforced in Go (domain/notification.go).';
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- No DDL change; nothing to revert.

-- +goose StatementEnd

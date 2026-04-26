-- +goose Up
-- Phase 8B Task 11 (per F09): per-user state row tracking the timestamp at
-- which a creator's balance first entered the (0, MIN_PAYOUT_SATS) window.
-- The balance worker maintains this row each tick and emits
-- low_balance_stuck only when `since` is older than 7 days. Solves the
-- naive-MIN(created_at)-on-positive-entries bug where an old credit could
-- spuriously trigger "stuck" after a recent crossing.
CREATE TABLE IF NOT EXISTS user_low_balance_state (
  user_id           UUID NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  since             TIMESTAMPTZ NOT NULL,
  last_balance_sats BIGINT NOT NULL,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_low_balance_state_since
  ON user_low_balance_state (since);

-- +goose Down
DROP INDEX IF EXISTS idx_user_low_balance_state_since;
DROP TABLE IF EXISTS user_low_balance_state;

-- Revert 0103: drop the new_report staff fan-out's idempotency guard. The
-- notification rows themselves are plain notifications and survive; without
-- the index a re-fired fan-out could double-notify, which is the pre-0103
-- state of the world.
DROP INDEX IF EXISTS notifications_new_report_unique_idx;

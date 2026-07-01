-- Revert account reports. Drop any account-target rows first so the narrower
-- CHECK can be restored.
DROP INDEX IF EXISTS reports_reporter_account_idx;
DELETE FROM reports WHERE target_type = 'account';
ALTER TABLE reports DROP COLUMN IF EXISTS reported_user_id;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment'));

-- 0027: Account (user) abuse reports. Extend the reports table so a signed-in
-- user can also report another account, alongside the existing video/comment
-- targets. reported_user_id carries the target for target_type='account'. A user
-- reports a given account at most once (partial unique index). Self-reports are
-- rejected in the service layer.
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment', 'account'));

ALTER TABLE reports ADD COLUMN reported_user_id UUID REFERENCES users (id) ON DELETE CASCADE;

-- A user reports any given account at most once.
CREATE UNIQUE INDEX reports_reporter_account_idx
    ON reports (reporter_id, reported_user_id) WHERE reported_user_id IS NOT NULL;

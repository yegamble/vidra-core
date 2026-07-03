DROP INDEX videos_quarantined_idx;
-- 'quarantined' rows must leave the state set before the narrower CHECK returns.
UPDATE videos SET state = 'failed' WHERE state = 'quarantined';
ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'scheduled', 'published', 'failed'));
ALTER TABLE users DROP COLUMN bypass_quarantine;

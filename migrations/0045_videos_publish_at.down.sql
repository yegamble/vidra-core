DROP INDEX videos_due_publish_idx;
-- 'scheduled' rows must leave the state set before the narrower CHECK returns.
UPDATE videos SET state = 'draft' WHERE state = 'scheduled';
ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'published', 'failed'));
ALTER TABLE videos DROP COLUMN publish_at;

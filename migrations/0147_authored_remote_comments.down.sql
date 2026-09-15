-- Reverse 0147. Order matters: drop the dependents (the FK columns and the
-- widened CHECK that name the table) before the table itself.

DROP INDEX IF EXISTS watched_word_matches_authored_remote_uniq;

ALTER TABLE watched_word_matches DROP CONSTRAINT IF EXISTS watched_word_matches_one_target;
ALTER TABLE watched_word_matches DROP COLUMN IF EXISTS authored_remote_comment_id;
-- Restore the original two-target CHECK (0049).
ALTER TABLE watched_word_matches ADD CONSTRAINT watched_word_matches_one_target
    CHECK ((comment_id IS NULL) <> (video_id IS NULL));

ALTER TABLE federation_deliveries DROP COLUMN IF EXISTS authored_remote_comment_id;

DROP TABLE IF EXISTS authored_remote_comments;

-- Reverse 0147. Order matters: drop the dependents (the FK columns and the
-- widened CHECK that name the table) before the table itself.

DROP INDEX IF EXISTS watched_word_matches_authored_remote_uniq;

ALTER TABLE watched_word_matches DROP CONSTRAINT IF EXISTS watched_word_matches_one_target;

-- Delete the authored-remote-comment match rows BEFORE dropping the column and
-- restoring the two-target CHECK. Such a row has BOTH comment_id and video_id
-- NULL (its only target is authored_remote_comment_id); once the column is gone
-- it would fail the restored CHECK ((comment_id IS NULL) <> (video_id IS NULL))
-- — which evaluates FALSE when both are NULL — and the ADD CONSTRAINT below would
-- abort, leaving the table with neither the column nor the constraint. Dropping
-- the authored_remote_comments table further down would cascade these rows away
-- regardless (ON DELETE CASCADE), so removing them here loses nothing a rollback
-- does not already discard.
DELETE FROM watched_word_matches WHERE authored_remote_comment_id IS NOT NULL;

ALTER TABLE watched_word_matches DROP COLUMN IF EXISTS authored_remote_comment_id;
-- Restore the original two-target CHECK (0049).
ALTER TABLE watched_word_matches ADD CONSTRAINT watched_word_matches_one_target
    CHECK ((comment_id IS NULL) <> (video_id IS NULL));

ALTER TABLE federation_deliveries DROP COLUMN IF EXISTS authored_remote_comment_id;

DROP TABLE IF EXISTS authored_remote_comments;

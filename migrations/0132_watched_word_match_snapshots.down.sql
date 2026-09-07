-- Reverse 0132. The snapshots and triage state are dropped with the columns —
-- there is nowhere else to keep them — and the watched_word_id FK goes back to
-- ON DELETE CASCADE, which requires deleting the matches whose word is already
-- gone (they are exactly the rows the forward migration made representable).
DROP INDEX IF EXISTS watched_word_matches_status_created_idx;

ALTER TABLE watched_word_matches
    DROP CONSTRAINT IF EXISTS watched_word_matches_status_check;

DELETE FROM watched_word_matches WHERE watched_word_id IS NULL;

ALTER TABLE watched_word_matches
    DROP CONSTRAINT IF EXISTS watched_word_matches_watched_word_id_fkey;
ALTER TABLE watched_word_matches
    ALTER COLUMN watched_word_id SET NOT NULL;
ALTER TABLE watched_word_matches
    ADD CONSTRAINT watched_word_matches_watched_word_id_fkey
        FOREIGN KEY (watched_word_id) REFERENCES watched_words (id) ON DELETE CASCADE;

ALTER TABLE watched_word_matches
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS moderator_note,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS snapshot_backfilled,
    DROP COLUMN IF EXISTS match_length,
    DROP COLUMN IF EXISTS match_offset,
    DROP COLUMN IF EXISTS matched_term,
    DROP COLUMN IF EXISTS matched_text;

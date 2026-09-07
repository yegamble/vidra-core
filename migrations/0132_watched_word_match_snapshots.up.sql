-- 0132: watched-word match snapshots + triage (A16 ruling).
--
-- Two defects A16 slice 3 measured and recorded:
--
--   (1) The review queue's excerpt was a LIVE JOIN on the comment body / video
--       title+description, so an author who edited the term away left the queue
--       quoting a clean body under the flag. A moderator could not see what was
--       actually flagged, and the author could edit the evidence away while the
--       flag stood. The DM report path solved exactly this with
--       reports.message_body_snapshot (0064); this table had no equivalent.
--   (2) There was no resolve and no dismiss — every mutating verb was 404 — so
--       an instance with an active term accumulated matches forever with no
--       triage state, unlike the report queue, which has a status and a note.
--
-- The snapshot columns are written by the flagger at flag time. matched_term is
-- the term as it read then, so a match survives its word being deleted (see the
-- FK change below) and still says what was matched. match_offset/match_length
-- locate the first occurrence INSIDE the snapshot, in RUNES (code points), not
-- bytes — the review UI highlights by slicing the string, and a byte offset
-- would cut a multi-byte character in half. -1 means "not located": the
-- backfilled rows below, where the flag-time text is gone and only the live text
-- survives, so no offset can honestly be claimed.
--
-- snapshot_backfilled marks exactly those rows. A backfilled quote is the body
-- as it reads TODAY, which is the thing the defect says cannot be trusted, so it
-- must never be mistaken for a flag-time capture. New rows are false.
ALTER TABLE watched_word_matches
    ADD COLUMN matched_text        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN matched_term        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN match_offset        INTEGER     NOT NULL DEFAULT -1,
    ADD COLUMN match_length        INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN snapshot_backfilled BOOLEAN     NOT NULL DEFAULT false,
    -- Triage, shaped like the report queue (reports.status/moderator_note/
    -- resolved_by/resolved_at): 'open' until a moderator acts, then 'resolved'
    -- (action taken) or 'dismissed' (false positive / no action needed). The
    -- note is the moderator's prose and lives HERE rather than in audit_log,
    -- which validates metadata against a closed key allowlist with a 256-byte
    -- cap and rejects the whole event when prose reaches it (0130 learned this
    -- with video_rejections).
    ADD COLUMN status              TEXT        NOT NULL DEFAULT 'open',
    ADD COLUMN moderator_note      TEXT        NOT NULL DEFAULT '',
    ADD COLUMN resolved_by         UUID        REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN resolved_at         TIMESTAMPTZ;

-- Backfill the snapshot from the live target, marked as such. The video arm
-- mirrors the flagger's own concatenation (title + "\n" + description) so a
-- backfilled video snapshot has the same shape as a captured one.
UPDATE watched_word_matches m
SET matched_text = COALESCE(
        (SELECT c.body FROM comments c WHERE c.id = m.comment_id),
        (SELECT v.title || E'\n' || v.description FROM videos v WHERE v.id = m.video_id),
        ''),
    matched_term = COALESCE(
        (SELECT w.word FROM watched_words w WHERE w.id = m.watched_word_id), ''),
    snapshot_backfilled = true;

ALTER TABLE watched_word_matches
    ADD CONSTRAINT watched_word_matches_status_check
        CHECK (status IN ('open', 'resolved', 'dismissed'));

-- Deleting a watched WORD used to delete its whole review history (0030's
-- ON DELETE CASCADE): a moderator pruning the term list silently discarded the
-- record of everything it had ever caught. The match now survives, keeping its
-- snapshot and matched_term, and the queue reports the term as no longer
-- watched. The column has to become nullable for that, which is a widening;
-- release N-1 keeps working because ITS query INNER JOINs watched_words, so a
-- word-less match is simply invisible to it rather than unscannable.
--
-- comment_id/video_id deliberately KEEP their ON DELETE CASCADE. Making them
-- nullable would break one-release schema compat outright: release N-1's
-- ListWatchedWordMatches projects COALESCE(m.video_id, c.video_id)::uuid as a
-- NOT-NULL column, so the first deleted target would make its whole queue
-- endpoint fail to scan. Losing the history of a DELETED comment is the
-- narrower gap, and it is recorded rather than closed here.
ALTER TABLE watched_word_matches
    DROP CONSTRAINT watched_word_matches_watched_word_id_fkey;
ALTER TABLE watched_word_matches
    ALTER COLUMN watched_word_id DROP NOT NULL;
ALTER TABLE watched_word_matches
    ADD CONSTRAINT watched_word_matches_watched_word_id_fkey
        FOREIGN KEY (watched_word_id) REFERENCES watched_words (id) ON DELETE SET NULL;

-- The queue lists OPEN matches by default now, so the filter is part of the
-- predicate and not just a display option.
CREATE INDEX watched_word_matches_status_created_idx
    ON watched_word_matches (status, created_at DESC);

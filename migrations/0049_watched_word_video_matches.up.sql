-- 0049: watched-word video tagging (product-decisions.md §12). A video's
-- title+description are matched against the watched-words list on create AND
-- edit (same matcher as comments); matches are recorded here via a new
-- nullable video_id target. A row flags exactly one target — a comment or a
-- video (CHECK). No auto-hold for videos: the §11 quarantine pipeline is the
-- hold mechanism; flagged videos surface in the existing matches review queue
-- with a type badge.
ALTER TABLE watched_word_matches ALTER COLUMN comment_id DROP NOT NULL;
ALTER TABLE watched_word_matches ADD COLUMN video_id UUID REFERENCES videos (id) ON DELETE CASCADE;
ALTER TABLE watched_word_matches ADD CONSTRAINT watched_word_matches_one_target
    CHECK ((comment_id IS NULL) <> (video_id IS NULL));

-- A video matches a given term at most once (partial: comment rows keep the
-- original UNIQUE (watched_word_id, comment_id)).
CREATE UNIQUE INDEX watched_word_matches_video_uniq
    ON watched_word_matches (watched_word_id, video_id) WHERE video_id IS NOT NULL;

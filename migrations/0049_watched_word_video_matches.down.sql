DROP INDEX watched_word_matches_video_uniq;
-- Video-target rows cannot survive the return to a NOT NULL comment_id.
DELETE FROM watched_word_matches WHERE video_id IS NOT NULL;
ALTER TABLE watched_word_matches DROP CONSTRAINT watched_word_matches_one_target;
ALTER TABLE watched_word_matches DROP COLUMN video_id;
ALTER TABLE watched_word_matches ALTER COLUMN comment_id SET NOT NULL;

-- Reverse 0136. Dropping the counter loses only where the NEXT generation would
-- have been numbered; every already-written tree stays exactly where it is,
-- because the live one is named by streaming_playlists.master_key and the
-- rendition key prefixes, not by this column. The older code then addresses
-- output off the source key's version again, which for an unreplaced source is
-- the legacy in-place prefix — so a re-transcode after a rollback overwrites in
-- place once more, which is the behaviour that release had.
ALTER TABLE videos
    DROP COLUMN IF EXISTS transcode_generation;

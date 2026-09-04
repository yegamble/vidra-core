-- Reverse 0126: drop the short code and the function that mints it.
--
-- WHAT IS LOST: every code, irrecoverably. Unlike 0119's dates these are not
-- re-derivable from anything — they are random, and nothing else records them.
-- Re-applying 0126 mints DIFFERENT codes, so every /v/{code} link ever emitted
-- (shared, bookmarked, posted to Bluesky, embedded in an ActivityPub `url`)
-- dies permanently.
--
-- That is acceptable ONLY while the code is still invisible — i.e. before the
-- frontend starts routing /v/{code} and before core starts emitting it in RSS,
-- the sitemap, AP `url` and Bluesky posts. After that point this file is the
-- wrong tool: roll back by deploying the previous image tag, which leaves the
-- column in place and the links working. The column is additive and the older
-- binary simply ignores it.
DROP INDEX IF EXISTS videos_short_code_key;

ALTER TABLE videos DROP COLUMN short_code;

DROP FUNCTION IF EXISTS vidra_short_code();

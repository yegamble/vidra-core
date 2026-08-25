-- Reverse 0117: drop the storyboard give-up ledger.
--
-- Only the MEMORY of the failures is lost, never a storyboard: successes were
-- always recorded as video_files rows and are untouched. After a re-apply of
-- 0117 the backfill treats every still-storyboard-less video as never tried,
-- including the ones it had permanently given up on, and pays for one more full
-- round of attempts against them before booking them again.
DROP TABLE IF EXISTS video_storyboard_attempts;

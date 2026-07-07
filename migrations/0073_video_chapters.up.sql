-- 0073: video chapters (CORE-15, backport wave W1.C1;
-- .ralph/specs/backport-w1-watch-player.md).
--
-- A video's seek-bar chapters: an ordered list of (start_seconds, title) marks
-- the custom player renders as tick markers and a "current chapter" label. The
-- set is small and always written atomically as a whole (replace = clear + bulk
-- insert), mirroring the video_tags model, so there is no per-row surrogate key.
--
-- (video_id, start_seconds) is the primary key: a video cannot have two chapters
-- starting at the same second (the service also enforces STRICTLY increasing
-- starts, of which uniqueness is a consequence). ON DELETE CASCADE ties the rows
-- to the owning video's lifecycle. Bounds (start_seconds >= 0, title 1..120 chars)
-- are checked here as a backstop; the service validates the full set first and
-- returns a 400 before any write.
CREATE TABLE video_chapters (
    video_id      uuid    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    start_seconds integer NOT NULL CHECK (start_seconds >= 0),
    title         text    NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    PRIMARY KEY (video_id, start_seconds)
);

-- 0136: every transcode run gets its own generation number.
--
-- WHY. Output keys were addressed off the SOURCE key's ".rN" suffix
-- (media.HLSPrefixForSource), which answers "which upload is this?" and not
-- "which transcode is this?". A source REPLACEMENT therefore wrote a fresh
-- generation directory and was safe, while a re-transcode of an UNCHANGED
-- source kept version 0 and overwrote the same fourteen objects in place. A32/
-- A33 measured what that costs with a cache in front: the origin's bytes moved
-- (208,623 B -> 203,095 B, a different sha) and the edge kept serving the old
-- ones on a HIT, with the stale 25 fps chunk decoding in Chromium against the
-- new 24 fps init segment. No error anywhere.
--
-- transcode_generation is that missing number: a per-video counter incremented
-- once per ENQUEUE (transcode.Service.EnqueueTarget), read by the worker, and
-- used as the rN directory for both the HLS tree and the progressive
-- web-videos. Keeping it on the video rather than on the job is deliberate —
-- a RETRY of the same job must write the same prefix, and it does, because a
-- retry does not enqueue.
--
-- ONE ADDRESSING SCHEME, NOT TWO. rN keeps meaning "generation directory", the
-- shape source replacement already used and mediagc already parses
-- (media.IsHLSGenerationName); what changed is what advances N. A replacement
-- still gets a fresh directory, because it enqueues a transcode like everything
-- else.
ALTER TABLE videos
    ADD COLUMN transcode_generation INTEGER NOT NULL DEFAULT 0;

-- Existing rows start at their CURRENT source version rather than at 0, so the
-- counter never hands out a directory name a previous replacement already used.
-- Without this a video whose source was replaced twice (tree at r2, counter 0)
-- would re-transcode into r1 — safe, because the transcoder clears its prefix
-- before writing and mediagc collects what promotion supersedes, but a number
-- going backwards is the kind of thing that reads as a bug forever after.
--
-- 0 for everything else, which is exactly right: a video transcoded once from
-- its original upload has generation 0 at the legacy in-place prefix, and its
-- next transcode will be generation 1 at streaming-playlists/<id>/r1/.
UPDATE videos v
   SET transcode_generation = sub.version
  FROM (
    SELECT f.video_id,
           (substring(f.storage_key FROM '\.r([1-9][0-9]*)\.[A-Za-z0-9]+$'))::int AS version
      FROM video_files f
     WHERE f.kind = 'original'
       AND f.storage_key ~ '\.r[1-9][0-9]*\.[A-Za-z0-9]+$'
  ) AS sub
 WHERE v.id = sub.video_id;

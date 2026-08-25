-- Storyboard backfill (migration 0117). Videos whose seek preview was never
-- generated — because the box had no ffmpeg the day they published, because the
-- storyboards_enabled overlay was off, or because they came from a PeerTube
-- source too old (or media too short) for the source to have had a sheet to
-- carry — plus the give-up ledger that stops an unfixable one being re-decoded
-- forever.

-- name: ListVideosNeedingStoryboard :many
-- The backfill's scan: published videos with no storyboard sprite, that have a
-- decodable source, and that the ledger says may be tried now.
--
-- Eligibility, term by term:
--
--   * state = 'published'. A draft, a failed upload, or a quarantined video has
--     nothing anyone can scrub yet, and publishing runs its own generation pass.
--     Every row in videos is LOCAL by construction — a federated video lives in
--     remote_videos and has no media of ours to decode — so locality needs no
--     clause of its own.
--   * NOT EXISTS a kind='storyboard' file. The sprite sheet IS the record of
--     success; a video that has one is never selected again, whether it was
--     generated here, carried in by the PeerTube importer, or produced by a
--     re-transcode. (The WebVTT map is deliberately not tested: the two rows are
--     written together, and keying on one of them is enough.)
--   * a kind='original' row. generateStoryboard takes an object key and hands it
--     to ffmpeg, so an original is the whole requirement. A video that has ONLY
--     an HLS tree — no original, e.g. one whose original was swept after
--     transcoding — is therefore NOT eligible and is skipped silently rather
--     than booked as a failure: nothing is wrong with it, there is simply no
--     single object to decode. Rendering a sheet from a rendition playlist would
--     be a different code path (concatenating segments) and is not built.
--     LATERAL + LIMIT 1 mirrors GetVideoFileByKind's newest-wins tie-break so a
--     video that somehow carries two originals resolves the same way everywhere.
--   * no ledger row, or a live one that is due. A given-up row is terminal and
--     drops the video out of this scan for good.
--
-- duration_seconds is the probe's answer where video_metadata has one and 0
-- otherwise, which is exactly the "unknown, go and probe it" hint the generator
-- expects. attempts is how many times this video has ALREADY failed (0 when it
-- has no ledger row), which is what the caller sizes the next backoff from
-- without a second round-trip.
--
-- No lease and no claim: the worker driving this is leader-gated, so exactly one
-- instance is ever reading it. Ordered oldest-first so an operator watching the
-- backlog sees it drain in a predictable direction.
SELECT v.id,
       orig.storage_key,
       COALESCE(vm.duration_seconds, 0)::int AS duration_seconds,
       COALESCE(a.attempts, 0)::int          AS attempts
FROM videos v
JOIN LATERAL (
    SELECT vf.storage_key
    FROM video_files vf
    WHERE vf.video_id = v.id AND vf.kind = 'original'
    ORDER BY vf.created_at DESC
    LIMIT 1
) orig ON true
LEFT JOIN video_metadata vm ON vm.video_id = v.id
LEFT JOIN video_storyboard_attempts a ON a.video_id = v.id
WHERE v.state = 'published'
  AND NOT EXISTS (
      SELECT 1 FROM video_files sb
      WHERE sb.video_id = v.id AND sb.kind = 'storyboard'
  )
  AND (a.video_id IS NULL OR (NOT a.given_up AND a.next_attempt_at <= now()))
ORDER BY v.created_at
LIMIT $1;

-- name: RecordStoryboardAttemptFailure :one
-- Book a RETRYABLE failure: bump the attempt count, park the video behind the
-- caller's backoff, and keep the reason. When that bump reaches max_attempts the
-- row flips to given_up in the same statement, so the decision is the database's
-- and not a read-modify-write the worker could lose a race on.
--
-- The returned (attempts, given_up) is what lets the caller log the give-up ONCE,
-- at the moment it happens, instead of either never mentioning it or restating it
-- on every later tick — there is no later tick for this video.
INSERT INTO video_storyboard_attempts (video_id, attempts, next_attempt_at, last_error, given_up)
VALUES (
    sqlc.arg(video_id),
    1,
    sqlc.arg(next_attempt_at),
    sqlc.arg(last_error),
    1 >= sqlc.arg(max_attempts)::int
)
ON CONFLICT (video_id) DO UPDATE
SET attempts        = video_storyboard_attempts.attempts + 1,
    next_attempt_at = EXCLUDED.next_attempt_at,
    last_error      = EXCLUDED.last_error,
    given_up        = video_storyboard_attempts.attempts + 1 >= sqlc.arg(max_attempts)::int,
    updated_at      = now()
RETURNING attempts, given_up;

-- name: GiveUpOnStoryboard :exec
-- Book a PERMANENT failure with no retries at all. The only thing that reaches
-- here is a source with no measurable duration: the sprite layout is computed
-- from the duration and nothing else, so a retry re-probes the same object and
-- gets the same nothing. Spending the retry budget on it would be four more full
-- decodes for an answer already known.
INSERT INTO video_storyboard_attempts (video_id, attempts, next_attempt_at, last_error, given_up)
VALUES (sqlc.arg(video_id), 1, now(), sqlc.arg(last_error), true)
ON CONFLICT (video_id) DO UPDATE
SET attempts   = video_storyboard_attempts.attempts + 1,
    last_error = EXCLUDED.last_error,
    given_up   = true,
    updated_at = now();

-- name: ClearStoryboardAttempt :exec
-- Success. The video now carries a storyboard file, which is what keeps it out
-- of the scan from here on, so the ledger row has nothing left to remember —
-- dropping it keeps the table proportional to the failures rather than to the
-- catalogue. A no-op for the overwhelmingly common case of a video that
-- succeeded first time and never had a row.
DELETE FROM video_storyboard_attempts WHERE video_id = $1;

-- name: CountAbandonedStoryboards :one
-- How many videos the backfill has permanently given up on. Not read by the
-- worker; it is the one-line answer to "which part of my catalogue will never
-- have a seek preview, and why" for whatever operator surface asks next.
SELECT count(*) FROM video_storyboard_attempts WHERE given_up;

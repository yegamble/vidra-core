-- CDN purge job queue (migration 0137). The durable half of the edge
-- invalidation seam: retries with backoff, an account deletion's snapshot, and
-- the resumable downloads-revocation walk. Same state machine as
-- account_exports (0057): pending → running → done | failed.

-- name: EnqueueCDNPurgeURLs :one
-- A fixed list of media route paths to invalidate. Used for a RETRY of URLs an
-- immediate pass could not purge, and for the account-deletion snapshot taken
-- before the cascade.
INSERT INTO cdn_purge_jobs (kind, reason, urls, url_set_complete, attempts, next_attempt_at)
VALUES ('urls', $1, $2, $3, $4, $5)
RETURNING id;

-- name: EnqueueCDNPurgeDownloadsRevoked :one
-- The catalogue walk. ON CONFLICT DO NOTHING against the partial unique index:
-- a second flip while a walk is still pending or running adds nothing, because
-- the walk already covers the whole catalogue.
INSERT INTO cdn_purge_jobs (kind, reason, url_set_complete)
VALUES ('downloads_revoked', 'downloads_revoked', TRUE)
ON CONFLICT (kind) WHERE kind = 'downloads_revoked' AND state IN ('pending', 'running') DO NOTHING
RETURNING id;

-- name: ClaimDueCDNPurgeJobs :many
-- Claims due jobs (oldest first) under a lease, flipping them to 'running' and
-- pushing next_attempt_at forward by the lease interval ($2 seconds).
--
-- It admits 'running' as well as 'pending', which account_exports does not:
-- next_attempt_at IS the lease here, so a row left 'running' by a killed worker
-- becomes claimable again the moment its lease passes. That is what makes the
-- catalogue walk survive a restart without a separate recovery sweep — it
-- resumes from cursor_video_id on whichever instance claims it next.
--
-- FOR UPDATE SKIP LOCKED for the reason every other queue here uses it:
-- concurrent claimers take disjoint rows instead of racing to re-evaluate the
-- subquery.
UPDATE cdn_purge_jobs
SET state = 'running',
    next_attempt_at = now() + make_interval(secs => sqlc.arg('lease_seconds')::double precision),
    updated_at = now()
WHERE id IN (
    SELECT id FROM cdn_purge_jobs
    WHERE state IN ('pending', 'running') AND next_attempt_at <= now()
    ORDER BY next_attempt_at, id
    LIMIT sqlc.arg('claim_limit')
    FOR UPDATE SKIP LOCKED
)
RETURNING id, kind, reason, urls, cursor_video_id, url_set_complete, purged, attempts;

-- name: CompleteCDNPurgeJob :exec
-- The whole URL set landed. urls is emptied so a finished row carries no stale
-- work, and purged records how much the run invalidated in total.
UPDATE cdn_purge_jobs
SET state = 'done', urls = '[]'::jsonb, purged = $2, last_error = '', updated_at = now()
WHERE id = $1;

-- name: AdvanceCDNPurgeWalk :exec
-- One resumable batch of the catalogue walk finished: persist the cursor and
-- the running purge count, and make the row immediately claimable again so the
-- next batch runs on the next drain rather than after a lease.
UPDATE cdn_purge_jobs
SET state = 'pending', cursor_video_id = $2, purged = $3,
    url_set_complete = url_set_complete AND sqlc.arg('batch_complete')::boolean,
    next_attempt_at = now(), updated_at = now()
WHERE id = $1;

-- name: RescheduleCDNPurgeJob :exec
-- The attempt failed (or partly failed). urls is REWRITTEN to only what is
-- still unpurged, so the retry never re-issues a call the edge already
-- accepted, and next_attempt_at carries the caller's backoff.
UPDATE cdn_purge_jobs
SET state = 'pending', urls = $2, purged = $3, attempts = attempts + 1,
    next_attempt_at = $4, last_error = $5, updated_at = now()
WHERE id = $1;

-- name: FailCDNPurgeJob :exec
-- Dead-letter after the attempt cap. urls is KEPT: it is the record of exactly
-- what the edge may still be serving, which is what an operator needs in order
-- to invalidate it by hand at the provider's console.
UPDATE cdn_purge_jobs
SET state = 'failed', urls = $2, purged = $3, attempts = attempts + 1,
    last_error = $4, updated_at = now()
WHERE id = $1;

-- name: DeleteFinishedCDNPurgeJobs :execrows
-- Retention. A completed invalidation is a fact about the past with no operator
-- value once it is old, and this table would otherwise grow one row per failed
-- purge forever. Dead letters are kept far longer than successes because they
-- are the ones an operator still has to act on.
DELETE FROM cdn_purge_jobs
WHERE (state = 'done' AND updated_at < now() - make_interval(secs => sqlc.arg('done_ttl_seconds')::double precision))
   OR (state = 'failed' AND updated_at < now() - make_interval(secs => sqlc.arg('failed_ttl_seconds')::double precision));

-- name: ListPublicDownloadableVideoIDs :many
-- The catalogue walk's page: public, published videos whose OWN download flag
-- is still on, after the cursor, in id order.
--
-- The per-video flag is the fence, and the INSTANCE flag deliberately is not:
-- by the time this job runs the instance toggle is already shut, so asking
-- "is this downloadable now?" would answer no for every row and purge nothing.
-- The question the edge's contents are the answer to is "what could an
-- anonymous visitor have fetched as a download immediately before the flip",
-- and that is the per-video flag alone.
SELECT id FROM videos
WHERE privacy = 'public' AND state = 'published' AND download_enabled
  AND id > sqlc.arg('after')::uuid
ORDER BY id
LIMIT sqlc.arg('page_limit');

-- name: ListVideoDownloadFacts :one
-- Everything the download URL set of one video is derived from, in one read:
-- which download-gated file kinds it has rows for, whether it has a finalized
-- ladder (the /download/audio and /download/hls/<h> routes exist only then),
-- and the rendition heights.
SELECT
    EXISTS (SELECT 1 FROM video_files f
             WHERE f.video_id = $1 AND f.kind = 'original' AND f.storage_key <> '') AS has_original,
    EXISTS (SELECT 1 FROM video_files f
             WHERE f.video_id = $1 AND f.kind = 'webm' AND f.storage_key <> '')     AS has_webm,
    EXISTS (SELECT 1 FROM streaming_playlists p
             WHERE p.video_id = $1 AND p.master_key <> '')                          AS has_playlist,
    COALESCE((SELECT array_agg(r.height ORDER BY r.height)
                FROM video_renditions r WHERE r.video_id = $1), '{}')::int[]         AS rendition_heights;

-- HLS transcoding pipeline (migration 0039): durable transcode job queue plus
-- streaming-playlist/rendition bookkeeping. Mirrors federation_deliveries.

-- name: EnqueueTranscodeJob :exec
-- Idempotent per live job: at most one pending/running job per video (partial
-- unique index), so a re-upload while one is queued does not double the work.
INSERT INTO transcode_jobs (video_id, source_key, transcode_type)
VALUES ($1, $2, $3)
ON CONFLICT (video_id) WHERE state IN ('pending', 'running') DO NOTHING;

-- name: ClaimDueTranscodeJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', so no two workers -- in one process or across instances -- ever
-- receive the same job.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE transcode_jobs
SET next_attempt_at = now() + interval '30 minutes',
    state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM transcode_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, video_id, source_key, transcode_type, attempts;

-- name: HasLiveTranscodeJob :one
-- Whether a pending/running transcode job exists for the video. The video-file
-- replacement flow (config-parity W14) answers 409 replace_conflict while one
-- does: enqueueing is idempotent per live job, so a replacement accepted now
-- would silently never be transcoded.
SELECT EXISTS (
    SELECT 1 FROM transcode_jobs
    WHERE video_id = $1 AND state IN ('pending', 'running')
);

-- name: DeleteStreamingPlaylist :exec
-- Drops a video's playlist row (with DeleteVideoRenditions) when a replacement
-- lands while transcoding is unavailable (config-parity W14): the old HLS tree
-- must stop serving superseded content; playback falls back to the progressive
-- original until a future transcode re-promotes a ladder.
DELETE FROM streaming_playlists WHERE video_id = $1;

-- name: CompleteTranscodeJob :exec
UPDATE transcode_jobs
SET state = 'done', updated_at = now()
WHERE id = $1;

-- name: RescheduleTranscodeJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE transcode_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: DeferTranscodeJob :exec
-- Return a claimed job to the queue WITHOUT consuming an attempt. Used when the
-- job cannot run for a reason that has nothing to do with the job itself -- today
-- only "the scratch filesystem does not have room for this source".
--
-- Deliberately NOT RescheduleTranscodeJob: that increments attempts, so a video
-- that happened to arrive during five full-disk ticks would dead-letter and stay
-- untranscoded forever. A transient host condition must not consume a video's
-- retry budget.
UPDATE transcode_jobs
SET state = 'pending', next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: FailTranscodeJob :exec
-- Dead-letter: no further retries.
UPDATE transcode_jobs
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE id = $1;

-- name: UpsertStreamingPlaylist :one
-- format records the packaging shape of the tree master_key points at (0108).
-- An empty value folds to 'hls-ts' rather than violating the CHECK: a caller
-- that has no opinion is describing the only shape that existed before CMAF,
-- and the promotion of a tree must never fail on a field it did not set.
INSERT INTO streaming_playlists (video_id, master_key, state, format)
VALUES ($1, $2, $3, COALESCE(NULLIF(@format::text, ''), 'hls-ts'))
ON CONFLICT (video_id) DO UPDATE
SET master_key = EXCLUDED.master_key,
    state = EXCLUDED.state,
    -- Re-transcoding into the other format REPLACES the record: after a
    -- TRANSCODING_PACKAGER rollback the row must describe the tree that is
    -- actually stored, not the one that used to be.
    format = EXCLUDED.format,
    updated_at = now()
RETURNING *;

-- name: MarkStreamingPlaylistFailed :exec
-- Dead-letter a video's playlist: the tree it pointed at is gone or was never
-- finished, so the master key is cleared and playback falls back to the
-- progressive original.
--
-- Deliberately NOT UpsertStreamingPlaylist with an empty format. That would fold
-- to 'hls-ts' and rewrite the format of an existing CMAF row, which is only
-- harmless today because this statement clears master_key in the same breath —
-- a coupling that would rot the moment a failure path kept the old tree. A
-- failure says nothing about what format the last successful transcode wrote, so
-- it leaves the column alone.
INSERT INTO streaming_playlists (video_id, master_key, state)
VALUES ($1, '', 'failed')
ON CONFLICT (video_id) DO UPDATE
SET master_key = '', state = 'failed', updated_at = now();

-- name: GetStreamingPlaylist :one
SELECT * FROM streaming_playlists WHERE video_id = $1;

-- name: CreateVideoRendition :one
INSERT INTO video_renditions (video_id, height, width, key_prefix, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteVideoRenditions :exec
DELETE FROM video_renditions WHERE video_id = $1;

-- name: ListVideoRenditions :many
SELECT * FROM video_renditions WHERE video_id = $1 ORDER BY height DESC;

-- name: UpsertTranscodeStep :exec
INSERT INTO transcode_steps (
    transcode_job_id, video_id, format, height, width, state, stage,
    progress_percent, started_at, finished_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    CASE WHEN $6 = 'running' THEN now() ELSE NULL END,
    CASE WHEN $6 IN ('succeeded', 'failed') THEN now() ELSE NULL END
)
ON CONFLICT (transcode_job_id, format, height) DO UPDATE SET
    width = EXCLUDED.width,
    state = EXCLUDED.state,
    stage = EXCLUDED.stage,
    progress_percent = EXCLUDED.progress_percent,
    started_at = CASE
        WHEN transcode_steps.started_at IS NULL AND EXCLUDED.state = 'running' THEN now()
        ELSE transcode_steps.started_at
    END,
    finished_at = CASE
        WHEN EXCLUDED.state IN ('succeeded', 'failed') THEN now()
        ELSE NULL
    END,
    updated_at = now();

-- name: GetVideoFileSizeByStorageKey :one
-- The stored byte size of the file at a storage key. The transcode worker uses it
-- for scratch-space admission control: a job's source_key IS the original's
-- storage_key, so this answers "how big is the thing I am about to transcode"
-- without a round trip to the object store. Rows are unique enough in practice
-- (one file per stored key) but LIMIT 1 keeps the query total.
SELECT size_bytes FROM video_files WHERE storage_key = $1 LIMIT 1;

-- name: RenewTranscodeJobLease :exec
-- Push a running job's lease forward. The worker calls this on a ticker while the
-- job runs, so a job that legitimately outlives one lease is not swept out from
-- under itself. Guarded on state so a completed or failed job cannot be revived.
UPDATE transcode_jobs
SET next_attempt_at = now() + interval '30 minutes'
WHERE id = $1 AND state = 'running';

-- name: SweepExpiredTranscodeJobs :execrows
-- Return jobs whose lease elapsed while they were 'running' to the queue.
--
-- This REPLACES the boot-time blanket requeue of every running row, which was
-- safe only because the process doing it was the deployment's only worker. A
-- second instance booting would have requeued jobs the first was actively
-- running. A lease sweep needs no such assumption: it only touches rows whose
-- owner has demonstrably stopped renewing, so it is correct with any number of
-- instances and can run periodically rather than only at start-up.
--
-- attempts is incremented so a job that crashes its worker every time walks its
-- counter up and dead-letters through the normal path instead of looping forever.
UPDATE transcode_jobs
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE state = 'running' AND next_attempt_at <= now();

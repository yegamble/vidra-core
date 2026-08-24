-- PeerTube import bookkeeping (migration 0067, fix_plan P18). Two concerns:
-- the durable idempotency/resume LEDGER (peertube_import_ledger) and the
-- admin-facing RUNS queue (peertube_import_runs). No source credential is ever
-- stored here — the source connection comes from server config / CLI flags.

-- ─────────────────────────── ledger ───────────────────────────

-- name: GetImportLedgerEntry :one
-- Idempotency lookup: the mapping for one source entity, if it exists. A 'done'
-- row means "already imported, skip"; vidra_id lets children resolve parents.
SELECT * FROM peertube_import_ledger
WHERE entity_kind = $1 AND source_id = $2;

-- name: UpsertImportLedgerEntry :one
-- Record (or update) the outcome of importing one source entity. Called in the
-- SAME transaction as the entity insert so the map and the row commit atomically
-- (a crash between them cannot leave a dangling half-import). ON CONFLICT keeps
-- the re-import a no-op-that-updates rather than a duplicate.
INSERT INTO peertube_import_ledger (entity_kind, source_id, vidra_id, status, note)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (entity_kind, source_id)
DO UPDATE SET vidra_id = EXCLUDED.vidra_id,
              status   = EXCLUDED.status,
              note     = EXCLUDED.note,
              updated_at = now()
RETURNING *;

-- name: CountImportLedgerByKindStatus :many
-- Per-entity outcome tally for run progress + post-import verification.
SELECT entity_kind, status, count(*) AS n
FROM peertube_import_ledger
GROUP BY entity_kind, status
ORDER BY entity_kind, status;

-- name: ListImportLedgerConflicts :many
-- The rows the conflict policy touched (renamed/merged/skipped/failed) — the
-- dry-run "conflicts" report and the operator's post-import audit.
SELECT * FROM peertube_import_ledger
WHERE status IN ('skipped', 'failed', 'unsupported') OR note <> ''
ORDER BY entity_kind, source_id
LIMIT $1;

-- ─────────────────────────── runs ───────────────────────────

-- name: CreateImportRun :one
-- Launch a run. The single-active partial unique index makes this fail with a
-- unique violation when a run is already pending/running — the caller maps that
-- to a 409 "an import is already in progress".
INSERT INTO peertube_import_runs (mode, conflict_policy, started_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetImportRun :one
SELECT * FROM peertube_import_runs WHERE id = $1;

-- name: GetLatestImportRun :one
SELECT * FROM peertube_import_runs ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: ListImportRuns :many
SELECT * FROM peertube_import_runs
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: ClaimDueImportRuns :many
-- The worker claims due, still-pending runs (oldest first), flipping them to
-- 'running' and stamping started_at on first claim.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE peertube_import_runs
SET next_attempt_at = now() + interval '30 minutes',
    state = 'running',
    attempts = attempts + 1,
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE id IN (
    SELECT id FROM peertube_import_runs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, mode, conflict_policy, started_by;

-- name: SetImportRunVersion :exec
UPDATE peertube_import_runs
SET source_version = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateImportRunProgress :exec
UPDATE peertube_import_runs
SET progress = $2, updated_at = now()
WHERE id = $1;

-- name: CompleteImportRun :exec
UPDATE peertube_import_runs
SET state = 'done', progress = $2, error = '', finished_at = now(), updated_at = now()
WHERE id = $1;

-- name: FailImportRun :exec
UPDATE peertube_import_runs
SET state = 'failed', error = $2, finished_at = now(), updated_at = now()
WHERE id = $1;

-- ─────────────────── idempotent entity inserts ───────────────────
-- These write the mapped Vidra rows during an import. They preserve the source
-- created_at (a migration should keep original timestamps) and RETURN only the
-- new id, so they add no coupling to the shared sqlc row models. Each is called
-- in the same transaction as its ledger upsert. Conflict detection (username/
-- handle/email/slug) happens in the service BEFORE these run; ON CONFLICT guards
-- only protect the natural-key child tables against a resumed partial import.

-- name: ImportInsertUser :one
INSERT INTO users (username, email, password_hash, role, email_verified, is_active, display_name, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: ImportFindUserByEmail :one
-- Conflict lookup for the 'merge' policy: the existing user with this email.
SELECT id FROM users WHERE lower(email) = lower($1) LIMIT 1;

-- name: ImportFindUserByUsername :one
SELECT id FROM users WHERE lower(username) = lower($1) LIMIT 1;

-- name: ImportInsertChannel :one
INSERT INTO channels (owner_id, handle, display_name, description, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: ImportFindChannelByHandle :one
SELECT id FROM channels WHERE lower(handle) = lower($1) LIMIT 1;

-- name: ImportInsertVideo :one
INSERT INTO videos (channel_id, title, description, privacy, state, category, language, license, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: ImportUpsertVideoMetadata :exec
INSERT INTO video_metadata (video_id, duration_seconds, width, height)
VALUES ($1, $2, $3, $4)
ON CONFLICT (video_id) DO UPDATE SET
    duration_seconds = EXCLUDED.duration_seconds,
    width = EXCLUDED.width,
    height = EXCLUDED.height,
    updated_at = now();

-- name: ImportInsertVideoFile :one
-- sha256 is the digest the media copy already computed while streaming the
-- object across; it is empty in reference mode, where no bytes were copied and
-- the backfill worker hashes the referenced object instead.
INSERT INTO video_files (video_id, kind, storage_key, content_type, original_name, size_bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: ImportUpsertCaption :one
INSERT INTO captions (video_id, language, label, storage_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (video_id, language) DO UPDATE SET
    label = EXCLUDED.label,
    storage_key = EXCLUDED.storage_key,
    updated_at = now()
RETURNING id;

-- name: ImportInsertVideoTag :exec
INSERT INTO video_tags (video_id, tag)
VALUES ($1, $2)
ON CONFLICT (video_id, tag) DO NOTHING;

-- name: ImportInsertComment :one
INSERT INTO comments (video_id, user_id, body, parent_id, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: ImportInsertPlaylist :one
INSERT INTO playlists (owner_id, title, description, visibility, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: ImportInsertPlaylistItem :exec
INSERT INTO playlist_items (playlist_id, video_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (playlist_id, video_id) DO NOTHING;

-- name: ImportFollowChannel :exec
INSERT INTO channel_follows (follower_id, channel_id)
VALUES ($1, $2)
ON CONFLICT (follower_id, channel_id) DO NOTHING;

-- name: ImportUpsertAccountActorKey :exec
-- Federation-continuity: carry the source account's ActivityPub keypair so the
-- migrated instance keeps signing as the same actor. private_key_pem is a SECRET
-- (sealed under the KEK, or raw in dev) — never logged. ON CONFLICT keeps a
-- resumed import a no-op.
INSERT INTO account_actor_keys (user_id, public_key_pem, private_key_pem)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO NOTHING;

-- name: ImportUpsertChannelActorKey :exec
INSERT INTO channel_actor_keys (channel_id, public_key_pem, private_key_pem)
VALUES ($1, $2, $3)
ON CONFLICT (channel_id) DO NOTHING;

-- name: RenewImportRunLease :exec
-- Push a running job's lease forward. The worker calls this on a ticker while the
-- job runs, so a job that legitimately outlives one lease is not swept out from
-- under itself. Guarded on state so a completed or failed job cannot be revived.
--
-- updated_at is bumped with the lease so the operational projection can tell a
-- live job from an abandoned one — see RenewTranscodeJobLease for why that bump
-- is the whole of the `stale_running` signal.
UPDATE peertube_import_runs
SET next_attempt_at = now() + interval '30 minutes', updated_at = now()
WHERE id = $1 AND state = 'running';

-- name: SweepExpiredImportRuns :execrows
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
--
-- Bounded FOR UPDATE SKIP LOCKED for the same reason the claim uses it: without
-- it, concurrent sweepers take their row locks in table order and serialise on
-- each other; with it they take disjoint rows. LIMIT bounds one tick's work
-- (see SweepExpiredTranscodeJobs for the full rationale). At most one run is
-- ever active here, so the bound is a formality — but a queue that is swept the
-- same way as every other queue is one fewer shape to reason about.
UPDATE peertube_import_runs
SET state = 'pending', attempts = attempts + 1, started_at = NULL, updated_at = now()
WHERE id IN (
    SELECT id FROM peertube_import_runs
    WHERE state = 'running' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
);

-- ───────────── per-video data carried after the video itself ─────────────
-- Views, chapters, ratings and renditions are per-video data that hangs off an
-- already-imported video rather than being part of its insert. They are carried
-- by their own passes, keyed by their own ledger rows, so a re-run of the
-- importer BACKFILLS them onto videos an earlier release already imported —
-- a pass folded into the video insert would be skipped for every one of them.
--
-- The rule they all share: the import never overwrites a row Vidra already has.
-- ON CONFLICT DO NOTHING everywhere, so an operator-edited chapter set, a rating
-- cast on the new instance, or a rendition row a Vidra re-transcode wrote all
-- survive an import running on a schedule. Views are the one exception, and they
-- are not an overwrite either — see ImportApplyVideoViewDelta.

-- name: ImportApplyVideoViewDelta :exec
-- Apply the CHANGE in a source video's lifetime view total to Vidra's counter.
--
-- This is a delta, never an assignment, and that is the whole design. Vidra's
-- counter is live: it has been incremented by every view served since the last
-- import. Assigning the source total would erase those; adding the source total
-- again would double the history. Adding only what the source gained since the
-- last run does neither, and a run against an unchanged source passes a delta of
-- zero. The caller stores the total it applied in peertube_import_ledger
-- .source_value; the delta is (new source total - that).
--
-- GREATEST(..., 0) floors the result: a source whose total went DOWN (a purge, a
-- re-count) can walk Vidra's counter back but never below zero.
INSERT INTO video_view_counts (video_id, views, updated_at)
VALUES (sqlc.arg('video_id'), GREATEST(sqlc.arg('delta')::bigint, 0), now())
ON CONFLICT (video_id) DO UPDATE
SET views = GREATEST(video_view_counts.views + sqlc.arg('delta')::bigint, 0),
    updated_at = now();

-- name: UpsertImportLedgerCounter :exec
-- The ledger upsert for a COUNTER kind: same idempotency key as
-- UpsertImportLedgerEntry, but it also records the source total that was applied
-- so the next run can compute a delta instead of re-applying the whole number.
INSERT INTO peertube_import_ledger (entity_kind, source_id, vidra_id, status, note, source_value)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (entity_kind, source_id)
DO UPDATE SET vidra_id     = EXCLUDED.vidra_id,
              status       = EXCLUDED.status,
              note         = EXCLUDED.note,
              source_value = EXCLUDED.source_value,
              updated_at   = now();

-- name: ImportInsertVideoChapter :exec
-- One seek-bar chapter mark. (video_id, start_seconds) is the table's primary
-- key, so DO NOTHING is what makes a repeated import a no-op — and it is also
-- what stops a scheduled import from stamping on a chapter set the operator
-- edited on the new instance.
INSERT INTO video_chapters (video_id, start_seconds, title)
VALUES ($1, $2, $3)
ON CONFLICT (video_id, start_seconds) DO NOTHING;

-- name: ImportInsertVideoRating :exec
-- One user's like/dislike, with the source's timestamps preserved. DO NOTHING on
-- the (user_id, video_id) key: if this pair already has a rating it was either
-- cast on Vidra or written by an earlier import, and neither is ours to replace.
INSERT INTO video_ratings (video_id, user_id, rating, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (user_id, video_id) DO NOTHING;

-- name: ImportInsertVideoRendition :exec
-- One rung of an imported HLS ladder, so the quality selector has something to
-- render for a video whose manifest Vidra did not write. key_prefix points at
-- the source tree's directory: there are no progressive per-rung download assets
-- under it, and the download endpoint already skips a rung whose asset is
-- missing, so the row advertises the rung without promising a file.
--
-- DO NOTHING on (video_id, height): a Vidra re-transcode owns its own rendition
-- rows, and overwriting one of those with a source key_prefix would break the
-- per-rung download it does have.
INSERT INTO video_renditions (video_id, height, width, key_prefix, size_bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (video_id, height) DO NOTHING;

-- name: ImportVideoHasReadyPlaylist :one
-- Whether a video has a ready HLS tree recorded. Rendition rows are only
-- meaningful alongside one: a video whose media was copied (and will be
-- transcoded by Vidra) gets its ladder from that transcode instead.
SELECT EXISTS (
    SELECT 1 FROM streaming_playlists
    WHERE video_id = $1 AND state = 'ready' AND master_key <> ''
);

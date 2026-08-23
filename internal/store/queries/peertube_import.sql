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

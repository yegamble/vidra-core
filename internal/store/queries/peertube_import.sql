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

-- name: UpsertImportLedgerLink :exec
-- The ledger upsert for a LINK rather than a creation: --conflict-policy
-- skip/merge did not insert a row for this source entity, it mapped the entity
-- onto a row that was ALREADY on this instance so the source's children still
-- resolve. Same idempotency key as UpsertImportLedgerEntry; created_by_import
-- (0124) is FALSE because the row on the other end belongs to this instance, and
-- the source-authoritative resync must not rewrite it. Stated once, here, at
-- creation — nothing re-asserts it on a later run.
INSERT INTO peertube_import_ledger (entity_kind, source_id, vidra_id, status, note, created_by_import)
VALUES ($1, $2, $3, $4, $5, FALSE)
ON CONFLICT (entity_kind, source_id)
DO UPDATE SET vidra_id          = EXCLUDED.vidra_id,
              status            = EXCLUDED.status,
              note              = EXCLUDED.note,
              created_by_import = FALSE,
              updated_at        = now();

-- name: GetImportLedgerLastWriteForTarget :one
-- The import's most recent COMPLETED write onto one Vidra row, within one entity
-- kind — "did I put the avatar that is in this slot there, and what was it?".
--
-- The ledger is keyed by SOURCE id, which is the wrong key for that question:
-- PeerTube keeps several actorImage rows per avatar and writes a new one every
-- time the picture changes, so the row that describes the slot's current
-- occupant is a DIFFERENT source id from the one a later run is looking at.
-- Keying the lookup on vidra_id instead follows the slot rather than the file.
--
-- applied_value (0113) is the fingerprint of what was written; it is empty for
-- rows written before that memory existed, and updated_at is then the evidence
-- available — the ledger row lands AFTER the image write, so a slot whose image
-- is newer than this row was filled by somebody else.
SELECT applied_value, updated_at
FROM peertube_import_ledger
WHERE entity_kind = $1 AND vidra_id = $2 AND status = 'done'
ORDER BY updated_at DESC, source_id DESC
LIMIT 1;

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
--
-- source_authoritative (0116) is a second, orthogonal axis: conflict_policy
-- says what to do about a NAME that already exists, this says whether a re-run
-- may update rows the import already owns when the two sides have diverged.
--
-- acknowledged_schema_version (0115) is a third and is independent of both: it is
-- the launching admin's explicit, per-run sign-off on an unverified source schema,
-- and it is written ONLY from the launch request. NULL is the norm. Both live here
-- rather than in the handler because the worker that runs the preflight is a
-- different process from the one that took the request, and because started_by is
-- on this same row: the pair is the audit record of who accepted which version.
INSERT INTO peertube_import_runs (mode, conflict_policy, started_by, source_authoritative, acknowledged_schema_version)
VALUES ($1, $2, $3, $4, $5)
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
RETURNING id, mode, conflict_policy, source_authoritative, started_by, acknowledged_schema_version;

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
SET state = 'done', progress = $2, error = '', error_code = '', finished_at = now(), updated_at = now()
WHERE id = $1;

-- name: FailImportRun :exec
-- error is the SAFE prose an operator reads; error_code is the stable snake_case
-- class a CLIENT branches on (empty when the failure has no class of its own). The
-- admin UI needs the second one to tell an unverified source schema — which an
-- administrator can sign off on — apart from every other way a run can fail,
-- which they cannot.
UPDATE peertube_import_runs
SET state = 'failed', error = $2, error_code = $3, finished_at = now(), updated_at = now()
WHERE id = $1;

-- ─────────────────── idempotent entity inserts ───────────────────
-- These write the mapped Vidra rows during an import. They preserve the source
-- created_at (a migration should keep original timestamps) and RETURN only the
-- new id, so they add no coupling to the shared sqlc row models. Each is called
-- in the same transaction as its ledger upsert. Conflict detection (username/
-- handle/email/slug) happens in the service BEFORE these run; ON CONFLICT guards
-- only protect the natural-key child tables against a resumed partial import.

-- name: ImportInsertUser :one
-- is_active carries the INVERSE of the source's user.blocked (its account
-- suspension). It used to be a hardcoded true, which stood every suspended
-- account up on the new instance with the source's working bcrypt hash.
--
-- storage_quota_bytes is a LITERAL 0 (unlimited for this account). A migrated
-- creator arrives with their whole back-catalogue already stored, and usage is
-- recomputed live as SUM(video_files.size_bytes) over the very rows this import
-- writes with the source's real byte counts — so a NULL here inherits
-- INSTANCE_DEFAULT_QUOTA_BYTES (5 GiB in the shipped template) and every creator
-- with a bigger catalogue is over quota the instant the import commits: their
-- first upload after being told "we have moved" is a 422 quota_exceeded.
-- Measured-usage-rounded-up only moves the trap to the NEXT upload, and under
-- --media-mode=reference it charges bytes that were never copied here. A
-- migration is not the moment to impose a cap the operator never chose, so this
-- is stated ONCE, at creation, and ImportUpdateUser never re-asserts it.
INSERT INTO users (username, email, password_hash, role, email_verified, is_active, display_name, created_at, storage_quota_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0)
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
-- originally_published_at is the source's own originallyPublishedAt and is NULL
-- for the videos that were first published on the source itself — the absence is
-- the answer, so it is carried through as NULL rather than defaulted to
-- created_at.
-- is_sensitive carries the source's video.nsfw, the flag its own hide/warn/blur
-- policy acts on. Nothing was written for it before, so every sensitive video
-- landed unflagged — including in the search index, which bakes the flag in.
INSERT INTO videos (channel_id, title, description, privacy, state, category, language, license, created_at, originally_published_at, is_sensitive)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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

-- name: ImportSetVideoOriginallyPublishedAt :exec
-- Carry the source's originallyPublishedAt onto an ALREADY-IMPORTED video.
--
-- ImportInsertVideo writes the same value for videos this release imports, so
-- this exists for the ones it did not: a catalogue migrated before the column
-- existed has the date only on the source, and importOneVideo will never run
-- again for a video with a terminal ledger row. Hence a pass of its own, with
-- its own ledger kind, exactly like the view/chapter/rating families above.
--
-- A plain assignment rather than the DO NOTHING the rest of this section uses.
-- The ledger makes this pass write at most once per video, so a date corrected
-- here AFTER that write is safe from every later scheduled run; a date somebody
-- set on Vidra BEFORE the pass first reaches the video is overwritten by the
-- source's, which is the same direction the insert already takes. (Under
-- --source-authoritative the field travels on ImportUpdateVideo instead, where
-- the source is meant to win on every run.)
UPDATE videos SET originally_published_at = $2 WHERE id = $1;

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

-- ───────────── the instance's own category taxonomy ─────────────
-- A PeerTube instance can replace the stock 1–18 category list wholesale
-- (peertube-plugin-categories does exactly that), and the import already carries
-- each video's category id across. Carried ids with no taxonomy behind them
-- validate against nothing, so the taxonomy is carried too — into the
-- instance_custom_categories setting, which REPLACES the built-in list.
--
-- It is one SETTING, not a set of rows, and one an operator may also edit by
-- hand. So the ledger remembers the exact value the import applied (0113) and
-- the pass compares before writing: its own earlier value may be updated when
-- the source moves, anything else is a human's and is left alone.

-- name: UpsertImportLedgerApplied :exec
-- The ledger upsert for a SINGLE-VALUE kind: same idempotency key as
-- UpsertImportLedgerEntry, but it also records the value that was applied so the
-- next run can tell its own write apart from an operator's edit. A pass that
-- decided NOT to write passes the value it read back, so a skip never erases the
-- memory of an earlier write.
INSERT INTO peertube_import_ledger (entity_kind, source_id, vidra_id, status, note, applied_value)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (entity_kind, source_id)
DO UPDATE SET vidra_id      = EXCLUDED.vidra_id,
              status        = EXCLUDED.status,
              note          = EXCLUDED.note,
              applied_value = EXCLUDED.applied_value,
              updated_at    = now();

-- ═══════════ source-authoritative resync (0116) ═══════════
--
-- Everything below is read or written ONLY by a run the operator launched with
-- source_authoritative. The default import is unchanged: it fills gaps, and the
-- ON CONFLICT DO NOTHING guards above are still what it uses.
--
-- The organising rule: THE LEDGER IS THE PROVENANCE RECORD. It maps every source
-- entity to the Vidra row that stands for it, so "did the import write this
-- row?" is answerable for every family by joining peertube_import_ledger to the
-- row. A resync therefore updates exactly the rows the import owns and cannot
-- reach a video somebody uploaded here, a channel created here, or an account
-- that never came from the source — those have no ledger row and are invisible
-- to every query in this section.
--
-- A ledger row is NOT on its own proof that the import CREATED what it points
-- at: --conflict-policy skip/merge maps a source entity onto a row that was
-- already here, and merge records that mapping as 'done'. created_by_import
-- (0124) is what separates the two, and every read below carries it — the join
-- alone would let a merge + source-authoritative run rewrite rows this instance
-- made. It is TRUE for every inserted row, so nothing else changes.
--
-- The reads are DELIBERATELY BULK, one statement per family for the whole
-- instance. A no-op re-run of this importer takes ~21 seconds on a 155k-entity
-- catalogue because every entity costs one indexed ledger lookup and no source
-- round trip; a resync that asked the destination a question per entity would
-- turn that into minutes over an SSH tunnel, and this is a tool the operator
-- runs on a SCHEDULE until cutover. Each read is keyed by source_id so the
-- caller can answer "has anything changed?" from memory, in the loop it already
-- runs, with no extra query at all.
--
-- The `status = 'done'` predicate everywhere is the 0114 partial index.

-- name: ListImportLedgerDoneByKind :many
-- Every completed mapping for one entity kind. It is how a resync notices a
-- source row that is GONE: the ledger still holds it, the source no longer
-- offers it, and the difference is what has to be removed here.
SELECT source_id, vidra_id, applied_value
FROM peertube_import_ledger
WHERE entity_kind = $1 AND status = 'done';

-- name: ImportResyncUsers :many
-- Every user the import CREATED, with the fields the import maps. username and
-- email are read but never rewritten — they are the NATURAL KEYS the conflict
-- policy owns, they are uniquely indexed, and a rename carried blindly could
-- collide with an unrelated account. They come back so the caller can REPORT the
-- divergence instead of silently ignoring it.
SELECT l.source_id, u.id, u.username, u.email, u.password_hash, u.role,
       u.email_verified, u.display_name
FROM peertube_import_ledger l
JOIN users u ON u.id = l.vidra_id
WHERE l.entity_kind = 'user' AND l.status = 'done' AND l.created_by_import;

-- name: ImportResyncChannels :many
-- Every channel the import CREATED — created_by_import is what makes that
-- sentence true. A channel this import only LINKED to under --conflict-policy
-- skip/merge was made on this instance, is somebody's here, and is not the
-- resync's to reassign or rename. handle is the natural key: read, reported,
-- never rewritten.
SELECT l.source_id, c.id, c.owner_id, c.handle, c.display_name, c.description
FROM peertube_import_ledger l
JOIN channels c ON c.id = l.vidra_id
WHERE l.entity_kind = 'channel' AND l.status = 'done' AND l.created_by_import;

-- name: ImportResyncVideos :many
-- Every video the import created, with the mapped metadata AND the duration,
-- which lives on video_metadata. The LEFT JOIN matters: a video whose source
-- carried no duration has no metadata row at all, and an inner join would hide
-- it from the resync entirely.
SELECT l.source_id, v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       COALESCE(v.category, '') AS category,
       COALESCE(v.language, '') AS language,
       COALESCE(v.license, '')  AS license,
       COALESCE(m.duration_seconds, 0)::int AS duration_seconds,
       -- Deliberately NOT coalesced: NULL is a value this field carries (the
       -- video was first published here), and folding it into a zero time would
       -- make "never published elsewhere" and "published at the epoch"
       -- indistinguishable to the digest.
       v.originally_published_at
FROM peertube_import_ledger l
JOIN videos v ON v.id = l.vidra_id
LEFT JOIN video_metadata m ON m.video_id = v.id
WHERE l.entity_kind = 'video' AND l.status = 'done' AND l.created_by_import;

-- name: ImportResyncVideoTags :many
-- The tag set standing on every video the import created, ordered so the caller
-- folds it into a set digest deterministically. Rows rather than a string_agg on
-- purpose: the desired set is folded in Go, and both sides have to be folded by
-- the SAME code or a difference in escaping reads as a change that is not there.
SELECT l.source_id, t.tag
FROM peertube_import_ledger l
JOIN video_tags t ON t.video_id = l.vidra_id
WHERE l.entity_kind = 'video' AND l.status = 'done' AND l.created_by_import
ORDER BY l.source_id, t.tag;

-- name: ImportResyncChapters :many
-- The chapter set standing on every video the import created. Keyed by the
-- VIDEO's source id, not by chapter ids, because a chapter is not an entity the
-- source keeps a stable identity for as far as Vidra is concerned: the primary
-- key here is (video_id, start_seconds), so a chapter somebody MOVED is a
-- different row, and the only correct unit of comparison is the whole set.
SELECT l.source_id, c.start_seconds, c.title
FROM peertube_import_ledger l
JOIN video_chapters c ON c.video_id = l.vidra_id
WHERE l.entity_kind = 'video' AND l.status = 'done' AND l.created_by_import
ORDER BY l.source_id, c.start_seconds;

-- name: ImportResyncRatings :many
-- Every rating standing on a video the import created, keyed by the pair
-- video_ratings itself is keyed by. Whether the IMPORT wrote a given one of
-- these is a separate question, answered from the rating ledger's applied_value;
-- this read only says what is there now.
SELECT r.user_id, r.video_id, r.rating
FROM video_ratings r
JOIN peertube_import_ledger l
  ON l.entity_kind = 'video' AND l.status = 'done' AND l.created_by_import AND l.vidra_id = r.video_id;

-- name: ImportResyncPlaylists :many
SELECT l.source_id, p.id, p.owner_id, p.title, p.description, p.visibility
FROM peertube_import_ledger l
JOIN playlists p ON p.id = l.vidra_id
WHERE l.entity_kind = 'playlist' AND l.status = 'done' AND l.created_by_import;

-- name: ImportResyncPlaylistItems :many
-- The slots standing in every playlist the import created, in playback order —
-- position is part of the comparison, so a re-ordered playlist is a changed one.
SELECT l.source_id, i.video_id, i.position
FROM peertube_import_ledger l
JOIN playlist_items i ON i.playlist_id = l.vidra_id
WHERE l.entity_kind = 'playlist' AND l.status = 'done' AND l.created_by_import
ORDER BY l.source_id, i.position, i.video_id;

-- ── the resync writes ──
--
-- Every one of them is keyed on an id that came out of the ledger. NONE of them
-- inserts a parent entity: ImportInsertVideo above is a plain INSERT with no
-- ON CONFLICT and no pre-check, so a resync that re-ran it would create a
-- DUPLICATE video and the ledger upsert would then repoint vidra_id at the
-- duplicate — orphaning the original row, its children and its blobs. The same
-- hazard applies to comments and playlists. Updating by id is what makes that
-- structurally impossible.

-- name: ImportUpdateUser :exec
-- The password hash is here on purpose and it is the field this exists for: an
-- operator syncs a live PeerTube for days, somebody changes their password on
-- the source in that window, and without this they cannot log in after cutover.
--
-- is_active is NOT written, even though the INSERT above now carries the
-- source's blocked flag: a suspension is a decision either side can make, and an
-- operator who suspended an account on THIS instance would find it unsuspended
-- every night if a resync re-asserted the source's answer. The import states the
-- suspension once, at the moment the account is created, and never again.
UPDATE users
SET password_hash  = $2,
    role           = $3,
    email_verified = $4,
    display_name   = $5,
    updated_at     = now()
WHERE id = $1;

-- name: ImportUpdateChannel :exec
UPDATE channels
SET owner_id     = $2,
    display_name = $3,
    description  = $4,
    updated_at   = now()
WHERE id = $1;

-- name: ImportUpdateVideo :exec
UPDATE videos
SET channel_id  = $2,
    title       = $3,
    description = $4,
    privacy     = $5,
    state       = $6,
    category    = $7,
    language    = $8,
    license     = $9,
    originally_published_at = $10,
    updated_at  = now()
WHERE id = $1;

-- name: ImportUpdateVideoDuration :exec
-- Duration ONLY. ImportUpsertVideoMetadata above also writes width and height,
-- and the import passes neither — so re-using it on a resync would set both to
-- NULL and erase the dimensions a Vidra transcode recorded.
INSERT INTO video_metadata (video_id, duration_seconds)
VALUES ($1, $2)
ON CONFLICT (video_id) DO UPDATE SET
    duration_seconds = EXCLUDED.duration_seconds,
    updated_at = now();

-- name: ImportDeleteVideoTagsNotIn :exec
-- The delete half of a SET. An empty array deletes every tag, which is right: a
-- source that now carries no tags for this video is a source that says it has
-- none. (`tag = ANY('{}')` is false for every row, so NOT ... is true.)
DELETE FROM video_tags
WHERE video_id = $1 AND NOT (tag = ANY(@tags::text[]));

-- name: ImportDeleteVideoChapters :exec
-- Chapters are replaced as a SET, never updated in place, and this is why: the
-- primary key is (video_id, start_seconds), so a chapter the source MOVED from
-- 90s to 95s is a different row — an upsert would leave the old mark standing
-- and the video would show both. Delete-then-reinsert inside one transaction is
-- the only shape that cannot duplicate.
DELETE FROM video_chapters WHERE video_id = $1;

-- name: ImportUpsertVideoRating :exec
-- The source-authoritative form of ImportInsertVideoRating: a person who changed
-- their like to a dislike on the source has their vote carried, instead of the
-- first vote standing forever.
INSERT INTO video_ratings (video_id, user_id, rating, created_at, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, video_id) DO UPDATE SET
    rating = EXCLUDED.rating,
    updated_at = now();

-- name: ImportDeleteVideoRating :exec
-- Unrating. PeerTube expresses it two ways depending on its version — the row's
-- type becomes 'none', or the row is deleted outright — and the caller handles
-- both, but only ever removes a rating the ledger says the import wrote and that
-- still holds the value the import last wrote for it.
DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2;

-- name: ImportUpdatePlaylist :exec
UPDATE playlists
SET owner_id    = $2,
    title       = $3,
    description = $4,
    visibility  = $5,
    updated_at  = now()
WHERE id = $1;

-- name: ImportUpsertPlaylistItem :exec
-- Position is part of what a playlist IS, so unlike ImportInsertPlaylistItem
-- this carries a re-ordering rather than treating an existing slot as done.
INSERT INTO playlist_items (playlist_id, video_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (playlist_id, video_id) DO UPDATE SET position = EXCLUDED.position;

-- name: ImportDeletePlaylistItemsNotIn :exec
-- A video removed from a playlist on the source leaves the playlist here.
DELETE FROM playlist_items
WHERE playlist_id = $1 AND NOT (video_id = ANY(@video_ids::uuid[]));

-- name: ImportUnfollowChannel :exec
-- Only ever called for a (follower, channel) pair the ledger records the import
-- having created, and only when the source no longer has that subscription.
DELETE FROM channel_follows WHERE follower_id = $1 AND channel_id = $2;

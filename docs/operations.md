# vidra-core operations guide

Operator runbook for the Go backend: backups, restore, observability surfaces,
and production deployment notes. The end-to-end single-host deployment (TLS proxy,
env matrix, staging→prod promotion) lives in the meta-repo
[`deploy/README.md`](../../deploy/README.md) and
[`.ralph/specs/environments.md`](../.ralph/specs/environments.md); this file is the
backend-specific companion.

## System of record vs derived state

| Store | Role | Backup? |
|-------|------|---------|
| **PostgreSQL** | Durable system of record (users, videos, jobs, moderation, federation). | **Yes — authoritative.** |
| **Media blobs** (local disk / S3) | Uploaded + transcoded media, thumbnails, captions, export archives. The authoritative store. | **Yes — must stay consistent with the DB.** |
| **IPFS pinset** (optional mirror) | Content-addressed *copies* — already-public media on the public node, and (when the private tier is enabled) non-public media on a separate swarm.key'd node. A distribution/replication surface, never authoritative. | **No — re-derivable from the authoritative store via `POST /admin/ipfs/reconcile` (optionally `?network=`).** |
| **Redis** | Cache, rate-limit counters, idempotency keys, view-dedupe, short locks. | **No.** Ephemeral; safe to flush. It is never the source of truth for data that must survive a restart. |

Back up PostgreSQL and media on the **same cadence** so blob references in the DB
resolve after a restore.

## PostgreSQL backup & restore

Custom-format dump (compressed, supports selective/parallel restore):

```bash
# Backup (nightly). Keep e.g. 14 daily + 8 weekly.
docker exec vidra-core-postgres-1 \
  pg_dump -U vidra -Fc vidra > vidra-$(date +%F).dump

# Restore into a fresh/empty database.
docker exec -i vidra-core-postgres-1 \
  pg_restore -U vidra -d vidra --clean --if-exists < vidra-2026-07-03.dump
```

Migrations are forward-only and are applied by the `migrate` compose service (or
`make migrate-up`). They are proven to apply cleanly against **both** an empty DB
and a **populated** one (`TestMigrationsAgainstExistingFixture`,
`internal/store`), so restoring an older dump and letting migrations catch up is
safe.

## Media backup — per storage backend

- **`STORAGE_BACKEND=s3`** (production default): rely on the object store's own
  versioning/replication/lifecycle rules (AWS S3, Backblaze B2, DigitalOcean
  Spaces). Nothing bespoke to run; keep the bucket's retention aligned with the
  DB retention. Credentials (`STORAGE_S3_ACCESS_KEY`/`STORAGE_S3_SECRET_KEY`) are
  secrets — never commit or log them. **If versioning is on, read the next
  section** — on a versioned bucket nothing Vidra deletes is ever reclaimed.
- **`STORAGE_BACKEND=local`** (dev / small single-host): snapshot the media volume
  (`docker volume` or a filesystem snapshot) on the same schedule as the DB dump.

### Versioned buckets need a non-current-version expiry rule

`vidra doctor` reports this under **object retention**. It matters most on
**Backblaze B2, whose buckets are versioned by default** — so the expensive case is
the default case for the cheapest storage target most operators pick.

On a versioned bucket a delete does not free anything. It writes a delete marker
(Backblaze calls it a *hide marker*) and the previous version keeps existing and
keeps billing. That is not an edge case for Vidra; it is the normal path:

| What Vidra deletes | What it costs on a versioned bucket with no expiry rule |
|---|---|
| Resumable-upload chunks under `uploads/<session>/*`, removed once the original is assembled | Every upload is stored twice and billed twice, permanently — a 2 GB upload bills as 4 GB |
| The previous HLS generation, cleared before a re-transcode of the same source | A full extra ladder per re-transcode |
| Everything media garbage collection sweeps | Nothing at all is reclaimed; the sweep is cosmetic |

**Fix:** add a lifecycle rule that expires **non-current** versions. On the S3 API
that is `NoncurrentVersionExpiration`; in Backblaze's own console it is
`daysFromHidingToDeleting`. A few days is plenty — Vidra never reads a superseded
version, so the window only needs to cover "did I just break something and want to
roll the bucket back". Backblaze additionally warns that accumulating many versions
of one object degrades listing and delete performance and can get an account blocked
from uploads.

Example rule (S3 API, whole bucket):

```json
{
  "Rules": [
    {
      "ID": "expire-noncurrent-versions",
      "Status": "Enabled",
      "Filter": { "Prefix": "" },
      "NoncurrentVersionExpiration": { "NoncurrentDays": 7 },
      "AbortIncompleteMultipartUpload": { "DaysAfterInitiation": 1 }
    }
  ]
}
```

`AbortIncompleteMultipartUpload` is worth having alongside it: an upload interrupted
mid-multipart otherwise leaves parts that are billed and do not appear in a normal
object listing.

Turning versioning **off** is the other valid answer, and is what `vidra doctor`
reports as clean. Do that only if the object store is not part of your recovery
story — the DB dump plus the bucket is the whole backup for an S3 deployment, and an
unversioned bucket has no undo.

A rule that exists but is **disabled** does not count, and `vidra doctor` will not
credit it — that is configuration which is not running.

`STORAGE_BACKEND=ipfs` is **not** a valid backend — IPFS is a mirror sidecar, never
authoritative (it is rejected at config load). See the next section.

## Media garbage collection — the job that deletes

Once a day, one instance lists everything stored under the six media prefixes
(`web-videos/`, `thumbnails/`, `storyboards/`, `captions/`, `streaming-playlists/`,
`playlist-thumbnails/`) and **deletes every object no database row references**.
That is how a deleted video's HLS ladder and a superseded original stop billing.
It never lists any other prefix — avatars, banners, upload chunks and the
ownership marker below are owned by other lifecycles and are not enumerated.

The inference it makes is "the database does not reference it, therefore it is
garbage", and that is only true when the database and the store belong to the same
install. Five rails stand between that inference and an irreversible delete.

| Rail | What it does |
|---|---|
| `MEDIA_GC_ENABLED` (default `true`) | `false` means the daily worker is never started. The admin endpoint stays available — the flag governs the unattended delete, not an operator asking for a sweep. Boot-baked: it is not in the admin settings overlay, deliberately |
| Dry-run first | The **first sweep of every process lifetime is a dry run**, and it runs ~5 minutes after boot rather than 24 hours later. A misconfiguration that would delete a library shows up in the log and the audit trail during the deploy that introduced it. Deletion starts from the sweep after that |
| `MEDIA_GC_MAX_ORPHAN_PERCENT` (default `25`) | A destructive sweep that finds more than this share of what it scanned to be orphans deletes **nothing** and says so (`breaker_tripped`). Sweeps of 100 orphans or fewer are exempt whatever the ratio — a nearly-empty store is 100% orphans and that is not news. Every wrong reference set looks the same: a half-restored database, a bucket that is not ours, a migration in flight |
| Bucket-ownership marker | See below. S3 only |
| Storage-migration interlock | While a migration campaign is not `done`/`cancelled`, a destructive sweep is forced to dry-run with `forced_dry_run_reason: storage_migration_active`. During a move the two stores are deliberately out of step, so the reference set describes neither of them completely. An interlock check that cannot be answered counts as "a migration is running". See "Moving the media store" |

### Bucket ownership (`.vidra/owner`)

Point Vidra at a bucket that already holds objects — a colleague's, a previous
install's, the *destination* of a migration in progress — and every object in it
looks like an orphan. So the api records which install owns a store, in an object
at `.vidra/owner` holding that install's identity UUID (migration `0105`,
`instance_identity`; it survives dump-and-restore, which is the point).

At boot the api reads it and resolves one of four states, logged as
`bucket_ownership` and reported on every sweep:

| State | When | Destructive sweep |
|---|---|---|
| `owned` | the marker holds this install's identity | allowed |
| `owned` (marked now) | the api **created** the bucket, or it was **empty** — nobody else's data can be in it, so the marker is written automatically | allowed |
| `unowned` | there is no marker and the bucket is **not** empty | refused — forced dry run |
| `conflict` | the marker holds a **different** install's identity: another Vidra believes this bucket is its own | refused — forced dry run |

`STORAGE_BACKEND=local` is exempt by design (`not-applicable`): a storage root is a
directory this install was pointed at and filled itself, and there is no shared-store
hazard to guard against.

A refusal is never an error. The sweep returns the **full orphan list** with
`forced_dry_run: true` — that list is how an operator works out whose media it is.

**Adopting a bucket.** If the objects really are yours (the usual case: a store
carried over from a previous install, or one restored from a backup), claim it
once as an admin:

```bash
curl -X POST https://<host>/api/v1/admin/media/gc/adopt-bucket \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

That writes the marker and re-enables deletion from the next sweep. It is audited
(`admin.media.gc.adopt_bucket`). It **overwrites** a marker belonging to another
install, so on a genuinely shared bucket it takes ownership away from the other
one — do not adopt your way out of a `conflict` without first working out what the
other install is.

`vidra doctor` reports both halves: **media GC posture** (Section State, env file
only) says what the knobs currently mean, and **bucket ownership** (Section Reach)
says whether the marker is there, with the adopt command as its fix.

### Reading a sweep

Every sweep is audited as `admin.media.gc` with counts and states and no object
keys, and the daily one logs the same line:

```text
media gc sweep completed mode=dry-run scanned=4210 orphans=39 orphan_percent=0
  deleted=0 breaker_tripped=false bucket_ownership=owned forced_dry_run=false
```

`deleted=0` with `mode=delete` never happens: if nothing was deleted, `mode` is
`dry-run` and one of `forced_dry_run` (ownership) or `breaker_tripped` (ratio) says
why. On a **versioned bucket with no non-current-version expiry rule**, a
successful sweep still reclaims nothing — see the section above; that is a billing
problem the GC cannot solve.

## Content hashes — what `video_files.sha256` means

Every stored media file recorded in `video_files` carries the SHA-256 of its
bytes, computed **in the same pass that uploaded them** — the stream is tee'd
through the hash on its way to the backend, so a hash costs no extra read and
never buffers a video. It is what makes a storage migration verifiable: copy,
compare, then delete the source. Nothing serves it over the API; it is an
operational column.

The column has three states, and the difference matters:

| Value | Meaning |
|---|---|
| *empty* | Not computed yet. Either the row predates the hash-on-Put paths, or its write path could not produce one (a PeerTube import in **reference** mode copies no bytes). **Never read an empty value as "verified"** |
| 64 hex chars | The digest of the object as stored |
| `missing` | The backfill went to read the object and the store said it is not there. The row is a **dangling reference** — a real finding, not a hash |

A **backfill worker** closes the gap on existing libraries: once a minute, on
the leader only, it takes the 25 oldest empty rows, streams each object through
SHA-256, and records the result. It has no enable flag on purpose — it only
reads objects and writes one text column, it cannot delete or rewrite media, and
it *drains*: when everything is hashed it logs

```text
media hash backfill complete: every stored media file carries a content hash or the missing sentinel
```

once and thereafter costs one indexed query per minute that returns nothing.
While it works it logs `media hash backfill progressed scanned=… hashed=…
missing=… failed=…`. A `failed` row is a transient read error (timeout, store
5xx); it stays empty and is retried next tick. A rising `missing` count is the
one worth acting on — those are database rows whose media is gone.

**HLS segments and playlists are deliberately hash-less.** They have no
`video_files` rows at all — a ladder rung is one `video_renditions` row covering
a whole directory of segments — so there is nowhere per-object to record a
digest and nothing that would read one back. Storage migration verifies that
tree object-by-object while it copies it. Consequently a fully-hashed library
says nothing about the integrity of the HLS trees.

## Verifying media consistency — `verify-blobs`

The GC sweep enumerates the **store** and looks for objects no row references.
`verify-blobs` is the other direction: it enumerates the **database** and looks
for rows no object backs. Nothing else detects that case — the API just 404s one
video at a time, forever, and you find out from a viewer.

It runs on the api image, like `migrate`, and it only ever reads:

```bash
# The fast pass: one existence check per referenced object.
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
  --env-file env/production.env run --rm api verify-blobs

# Re-read every object that has a recorded digest and compare (reads the whole
# library — give it a real timeout).
… run --rm api verify-blobs --hash --timeout=4h

# Walk every HLS tree through the storage backend instead of trusting that a
# present master manifest implies a present ladder.
… run --rm api verify-blobs --deep

# One JSON document instead of the summary.
… run --rm api verify-blobs --json
```

It runs on the **api** service, not the `migrate` one-shot: the migrator is
given `DATABASE_URL` and nothing else, and this needs the whole `STORAGE_*` set
to know which bucket or directory to ask.

| Exit | Meaning |
|---|---|
| `0` | The database and the store agree |
| `3` | They do not — objects are missing, corrupt, hollow (`--deep`), or unreadable |
| `1` | The check could not be made: the database or the bucket was unreachable, the configuration is unusable, or the run timed out |

`3` and `1` are deliberately different. A restore has to be able to tell "I
verified and it is wrong" from "I could not verify" — and it continues in both
cases, because a restore that aborted here would leave the site down over a
media problem that not booting does not fix.

**What it checks.** Every `storage_key` the database holds: `video_files`
(originals, thumbnails, storyboards, webm alternates), captions, avatars and
banners for users and channels, the instance's own images, account-export
archives, DM attachments, the reconstructed playlist covers, and each streaming
playlist's master manifest. The key derivation is shared with the GC sweep
(`mediagc.ListReferences`) so the two can never drift into disagreeing about
what the database references.

**The three classes it reports separately**, because they have different causes:

- **MISSING** — a row references an object the store does not have, and nothing
  had recorded that before this run. The finding that matters most.
- **MISMATCHED** (`--hash` only) — the object is there and its bytes no longer
  hash to what was written. Corruption, not absence.
- **known missing** — rows whose `sha256` is the backfill's `missing` sentinel
  and whose object is indeed absent. Reported in full every run, but they do
  **not** set exit 3: they were already dangling when the dump was taken, and a
  post-restore check that can never go green is one operators stop reading.
  (A sentinel row whose object *is* there is reported as `sentinel stale` — the
  object came back and the backfill will never revisit that row.)

**When to run it.**

- **After every restore** — `deploy/restore.sh` runs the fast pass automatically
  between the migrators and `up -d`, and never blocks on it. A dump is taken at
  time T and the bucket is whatever it is at T+n; the two stop being a matched
  pair the moment they are restored separately.
- **With `--hash`, after a hardware or provider incident**, after a bucket-level
  restore, or on a schedule you are willing to pay the reads for. It is the only
  mode that detects corruption; the fast pass deliberately reads no bytes.
- **With `--deep`, after anything that touched the object store wholesale.** A
  partial restore that brought back one small text file per video and none of
  the segments passes the fast pass with a clean bill of health and plays
  nothing.
- **Not during a storage migration.** The two stores are deliberately out of
  step then; the command says so in its output and in the JSON
  (`storage_migration_active`), and `vidra doctor`'s **storage migration** check
  reports the same fact before you start.

There is no repair mode, deliberately. Every plausible repair — delete the row,
re-derive the object — destroys information, and the only party who can say
which of the two stores is the stale one is the operator who knows what
happened.

## Moving the media store

Local disk → S3, one bucket → another, one provider → another. A **storage
migration campaign** copies every object into the destination, proves each copy
by reading it back out and re-hashing it there, and only deletes the originals
after you have been live on the new store for a grace period. Viewers see
nothing: during the move the API reads from **both** stores.

**Object keys never change.** A move copies each object to the same key, so
`media_ipfs_pins` (whose primary key *is* a storage key) and every
`video_files.storage_key` keep pointing at the right bytes. **The IPFS pin
ledger is not touched and does not need migrating.**

### 1. Point at the destination

Set the target beside your existing `STORAGE_*` block — same shape, prefixed:

```bash
STORAGE_MIGRATION_TARGET_BACKEND=s3            # "" (default) = feature off
STORAGE_MIGRATION_TARGET_S3_ENDPOINT=nyc3.digitaloceanspaces.com
STORAGE_MIGRATION_TARGET_S3_REGION=nyc3
STORAGE_MIGRATION_TARGET_S3_BUCKET=example-video-media
STORAGE_MIGRATION_TARGET_S3_ACCESS_KEY=...     # SECRET
STORAGE_MIGRATION_TARGET_S3_SECRET_KEY=...     # SECRET
STORAGE_MIGRATION_TARGET_S3_USE_SSL=true
STORAGE_MIGRATION_TARGET_S3_FORCE_PATH_STYLE=false
STORAGE_MIGRATION_GRACE_HOURS=168              # a week; the undo window
```

(Or `STORAGE_MIGRATION_TARGET_BACKEND=local` +
`STORAGE_MIGRATION_TARGET_LOCAL_ROOT=/srv/new-media`.) Restart. The api refuses
to boot if the target resolves to the same store as `STORAGE_*`, and logs
`storage migration target configured` with the endpoint and bucket — never the
keys.

### 2. Start it

```bash
curl -sX POST -H "Authorization: Bearer $ADMIN" \
  https://videos.example/api/v1/admin/storage/migrations
```

At most one campaign runs at a time (409 otherwise; 503 when no target is
configured). Watch it in **Admin → Jobs**: the campaign appears as a
`storage_migrations` run whose *stage* is its phase and whose percentage is
`objects_done / objects_total`, and the per-object queue appears as
`storage_migration_objects`. Or poll:

```bash
curl -s -H "Authorization: Bearer $ADMIN" \
  https://videos.example/api/v1/admin/storage/migrations/$ID | jq
```

The copy worker runs **on every instance** (leases make that safe), so more
instances copy faster. The phases:

| State | What is happening |
|---|---|
| `enumerating` | Listing every object in the source |
| `copying` | Objects are being copied and verified |
| `synced` | Everything is verified in the destination and a delta pass found nothing new — **cut over from here** |
| `cutover` | You swapped the environment; the grace clock is running |
| `deleting_source` | Removing the old store's copies |
| `done` | Finished |

Uploads that land **during** the move are picked up by the delta pass, so
`synced` keeps meaning "the two stores match" on a live instance.

### 3. Cut over — swap BOTH environment sets

This is the step that matters. When the campaign reads `synced` and its
`objects.pending` / `objects.copying` are zero, **swap the two blocks**:
`STORAGE_*` now describes the NEW store, and `STORAGE_MIGRATION_TARGET_*`
describes the **OLD** one. Restart.

That swap is not bookkeeping. It is how the api learns the cutover happened (it
compares the identity of the backends it is holding against the ones the
campaign recorded) **and** it is what gives the delete-source step a handle on
the store it is about to empty. If you swap only `STORAGE_*`, the campaign
stalls with an explanation in `last_error` and **nothing is deleted** — which is
the correct outcome, not a bug to work around.

While the campaign is not `done`/`cancelled`, serving reads go through a
dual-read view: an object missing from the new store is fetched from the old
one. Writes only ever go to the store you are configured to serve from.

### 4. Grace, then automatic deletion

`observed_cutover_at` is stamped the first time the api sees itself serving from
the destination, and is never moved by a restart. After
`STORAGE_MIGRATION_GRACE_HOURS` the campaign deletes the source copies in
batches and finishes. **Until then, reverting is a restart** — swap the
environment back and the old store is still complete.

Deletion refuses to run unless every object is accounted for, and refuses unless
the handle it is about to delete through is the campaign's recorded source.

**`cutover` → `done` takes about three minutes, even at
`STORAGE_MIGRATION_GRACE_HOURS=0`.** The state machine advances one step per
sweep tick and the sweep ticks once a minute: `cutover` → `deleting_source` →
(delete a batch) → `done`. A campaign that has sat at `cutover` for two minutes
after a zero-grace restart is on schedule, not stalled. With a real grace window
the wait is that window plus the same three ticks, and a library big enough to
need more than one delete batch (200 objects) takes one further tick per batch.

**Completing the campaign claims the destination store for this install.** The
last thing a `done` transition does is write the `.vidra/owner` marker into the
store it just filled, with this install's identity — see "Bucket ownership"
above. That write exists because nothing else would do it: boot-time claiming
only claims a bucket the api *created* or found *empty*, and a bucket a
migration filled is neither, so without it a completed move would leave media
GC permanently and silently non-destructive on your new bucket. It **never**
overwrites a marker that is already there and says something else — that is the
shared-bucket case, and it is logged loudly and left alone. Campaigns that
finished before this behaviour existed need the manual fallback once:

```bash
curl -X POST https://videos.example/api/v1/admin/media/gc/adopt-bucket \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

`vidra doctor`'s **bucket ownership** check is how you confirm either way.

### 5. Unconfigure the target

**A finished migration is not finished until you remove the target block.** Once
the campaign reads `done`, delete (or blank) every `STORAGE_MIGRATION_TARGET_*`
line and `STORAGE_MIGRATION_GRACE_HOURS`, and restart. `STORAGE_*` — which now
names the new store — is all that should be left.

This is not tidiness. **While a migration target is configured, direct delivery
stays disabled**, whatever `delivery_presign_enabled` says: during dual-read an
object may be in either store and a URL signed against the wrong one is a 404
the API can no longer rescue, so the capability is refused at boot. The boot log
says which state you are in (`direct object delivery available` / `… disabled
while a storage migration target is configured`). Leaving a completed campaign's
target in the environment therefore costs you every byte of egress the API would
otherwise have handed straight to the object store. Configuring a target also
keeps a second backend built and its workers ticking for no reason.

### Rolling back

- **Before cutover:** `POST /api/v1/admin/storage/migrations/$ID/cancel`. Nothing
  about what you serve has changed. Objects already copied stay in the
  destination — byte-identical copies under identical keys, inert until some
  future campaign re-verifies them. Clean up the destination bucket by hand if
  you want to.
- **After cutover, inside the grace window:** swap the environment back and
  restart, then cancel.
- **After the source has been deleted:** it is a restore, not a rollback. That is
  what the grace period is for.

### Media GC is interlocked

While any campaign is not `done` or `cancelled`, destructive media garbage
collection is **forced to dry-run** — `POST /admin/media/gc` and the daily sweep
both report `forced_dry_run: true` with
`forced_dry_run_reason: storage_migration_active`. During a move the two stores
are deliberately out of step, so "no database row references this object" stops
being evidence about either of them. If the interlock check itself cannot be
answered, it is treated as "a migration is running". Finish or cancel the
campaign to release it.

### Failures

A dead-lettered object shows in `objects_failed` and in the admin jobs
failures list, with a category and never a storage key:

| `last_error` | Meaning |
|---|---|
| `source object missing` | The source listed the key and then did not have it. A dangling object; nothing to copy |
| `verification failed: …` | The bytes read back out of the destination are not the bytes that were written. The destination store is lying; do not cut over |
| `the source object does not match the sha256 recorded for this file` | The copy was faithful — the **source** no longer matches its `video_files` row. Pre-existing corruption, surfaced before you delete the only other copy |
| `copy failed repeatedly; giving up on this object` | Five transient failures. Check the destination's reachability and start a fresh campaign |

Failed objects do not block a campaign from reaching `synced` — they are a
reported fact for you to decide about. They **do** mean the destination is not a
complete copy, so read `objects_failed` before you cut over.

## Streaming format — CMAF, and how to go back

New transcodes package **CMAF/fMP4**: one set of segments under
`streaming-playlists/<video-id>[/rN]/cmaf/`, addressed by *both* manifests.

| URL | What it is |
| --- | --- |
| `/api/v1/videos/{id}/hls/master.m3u8` | HLS multivariant playlist (unchanged address) |
| `/api/v1/videos/{id}/hls/cmaf/stream.mpd` | **MPEG-DASH manifest** — the canonical DASH URL |
| `/api/v1/videos/{id}/hls/cmaf/<file>` | The shared segments and per-representation playlists both manifests point at |

The two manifests name the **same files**. DASH therefore costs no extra storage
and no extra encode — that is the entire reason for the format change. Both
manifests carry the same authorization gate as every other media route.

`TRANSCODING_PACKAGER` selects the format:

| Value | Result |
| --- | --- |
| `cmaf` *(default)* | Shared CMAF segments, HLS **and** DASH |
| `ts` | The legacy MPEG-TS segments, HLS only |

**Rolling back is config-only.** Set `TRANSCODING_PACKAGER=ts` and restart. It
changes what *new* transcodes produce and nothing else:

* every video records the format its own tree was written in
  (`streaming_playlists.format`), so already-packaged CMAF videos keep playing —
  their master playlist, segments and DASH manifest are untouched;
* nothing is re-encoded, and there is deliberately **no** job that re-packages
  the back catalogue in either direction;
* the reverse switch (`ts` → `cmaf`) behaves the same way.

A video only changes format when it is transcoded again — a replacement upload or
a manual re-transcode — at which point it is written whole in whichever format is
configured then, and the DB row is swapped to the new tree atomically.

Two things to know before switching:

* **Old players.** A CMAF video's HLS master is `#EXT-X-VERSION:7` with fMP4
  segments. Every current browser and iOS/tvOS 10+ handles it; genuinely ancient
  HLS clients that only understand MPEG-TS do not. If you support such clients,
  stay on `ts`.
* **Direct delivery and CDNs.** The new object suffixes are `.m4s` (segments),
  `.mp4` (init segments) and `.mpd` (manifest). A CDN or bucket policy that
  allow-lists suffixes needs `.m4s` and `.mpd` added.

## Extra video codecs — HEVC and AV1

By default a transcode encodes each ladder rung once, in **H.264**. That is the
compatibility floor: every browser, every phone, every set-top box can play it,
and it is also what the progressive downloads, the `/download` web videos, the
audio-only extraction and the scrubbing thumbnails are made from. It is always
emitted and there is no setting that turns it off.

Two settings add a **second and third encoding of the same rungs** to the same
CMAF tree:

| Setting | Encoder | Bitrate vs H.264 | Plays on |
| --- | --- | --- | --- |
| `TRANSCODING_HEVC_ENABLED` | `libx265` | ~65% (a 35% cut) | Safari (macOS/iOS/tvOS), Edge and Chrome on hardware that decodes it, most 2016+ smart TVs. **Not** desktop Firefox |
| `TRANSCODING_AV1_ENABLED` | `libsvtav1` | ~55% (a 45% cut) | Chrome/Edge 70+, Firefox 67+, Safari 17+ on capable hardware |

Both default to `false`. With both off, a transcode produces byte-for-byte the
H.264 ladder it always produced.

**What "the same tree" means.** There is no second encode pass and no second set
of segments to manage. The one ffmpeg invocation that already decodes the source
once and scales it once grows extra encoders; the shared segment directory grows
extra representations. In the manifests:

* the **MPD** gains one video `AdaptationSet` per codec (a set is what a player
  switches *within*, so codecs are never mixed inside one), plus the same single
  audio set — audio is still encoded and stored **once** for the whole tree;
* the **HLS master** gains a variant per codec per rung, each carrying a `SCORE`
  and an `AVERAGE-BANDWIDTH`.

Trick-play, the progressive downloads and the audio file stay H.264-only.

### How a client picks the codec

This is worth knowing before you turn anything on, because the two intuitive
answers are both wrong.

**It is not the order of the manifest.** hls.js (1.6) re-sorts every variant on
load — by height, then by its own codec preference — and partitions them into
codec sets its adaptive-bitrate logic never switches between. Manifest order
survives only as the seed for the first bandwidth estimate. Vidra still writes
H.264 first, because it is deterministic and because a hand-rolled client that
takes the first variant it understands should land on the universally playable
one, but nothing about correctness depends on it.

**It is `SCORE`, for Apple clients.** Without a `SCORE` attribute, AVFoundation's
selection is close to "the highest `BANDWIDTH` it can play" — and because an
efficient codec *declares less*, that picks H.264 and inverts the whole point of
emitting HEVC. Vidra writes a `SCORE` on every variant: the rung's rank dominates
(720p always outranks 480p, whatever the codec) and the codec breaks the tie,
preferring HEVC, then AV1, then H.264. HEVC ranks above AV1 deliberately: on
Apple hardware HEVC is hardware-decoded essentially everywhere, while AV1
hardware decode arrived only with the A17 Pro and M3, and a software AV1 decode
that outranked a hardware HEVC one would cost battery and drop frames to save
bytes the device was not short of.

**`BANDWIDTH` is a peak, `AVERAGE-BANDWIDTH` is the truth.** `BANDWIDTH` is
derived from the rung's budget, which every codec's rate control genuinely bounds;
`AVERAGE-BANDWIDTH` is computed from the bytes the tree actually stored. The gap
between them is large for AV1 and that is the point — it is rate-capped at the
budget but encodes to a quality target underneath it, so easy content costs a
fraction of the ceiling.

### What it costs

Measured end to end on a 720p/480p/360p ladder, one codec against three:

| | 1 codec (default) | 3 codecs | |
| --- | --- | --- | --- |
| ffmpeg invocations | 9 | 9 | **unchanged** |
| source decode passes | 2 | 2 | **unchanged** |
| wall-clock time | 1.0x | **3.95x** | encoding is the cost |
| segment bytes | 1.0x | **1.96x** | |
| objects stored | 31 | 55 (**1.77x**) | |
| whole stored tree | 1.0x | 1.30x | downloads dominate and stay H.264 |

The strength first: adding codecs does **not** add ffmpeg runs or source decodes.
The one ladder pass already decodes once and scales each rung once, and the extra
codecs are extra encoders reading the same scaled frames.

The costs, honestly:

* **CPU is the real bill.** Nearly 4x the wall-clock time per video, permanently.
  A backlog that used to clear overnight will not.
* **Objects roughly double.** On a versioned bucket that is ~2x the `PUT`
  requests as well as the bytes, and every future delete leaves twice as many
  markers. Check your provider's request pricing, not just its storage pricing.
* **Scratch is not automatically sized for it.** `TRANSCODING_MIN_FREE_SCRATCH_MB`
  is an *absolute floor*, not a per-job estimate — the worker refuses new jobs
  below it and otherwise assumes the job will fit. A three-codec ladder writes
  roughly twice the segment bytes of a one-codec one before anything is uploaded,
  so raise that floor when you enable this. (`TRANSCODING_STREAM_OUTPUT=true`
  avoids the question entirely by streaming segments straight to the object
  store.)
* **`transcoding_threads` is a per-encoder floor, not a cap.** The job's budget is
  divided across the encoders running concurrently, but never below one thread
  each — so three codecs across four rungs will ask for twelve threads however
  small the setting is.

**Two hard boot failures, on purpose.** Both are settings whose failure mode is
otherwise invisible — the site comes up, videos transcode, and the smaller
deliveries the operator paid CPU for silently never appear:

* **`TRANSCODING_PACKAGER=ts` with either codec on.** MPEG-TS is the frozen
  rollback path; a variant there is a rendition *directory* named for its height,
  so there is nowhere to put a second encoding of that height, and multi-codec
  MPEG-TS masters are deliberately not built. Rolling the packager back to `ts`
  therefore means turning these off too.
* **An ffmpeg without the encoder.** The shipped image has `libx265` and
  `libsvtav1`; a host binary or a slimmed image may not. The api probes
  `ffmpeg -encoders` at boot and refuses to start, naming the encoder and the
  setting. `vidra doctor` reports the same thing (the **video encoders** check)
  against the api container's ffmpeg, so a deploy can be checked before it is
  rolled out.

**Turning them off is config-only**, exactly like the packager rollback: it
changes what *new* transcodes produce, already-packaged multi-codec trees keep
serving every representation they have, and nothing is re-encoded. There is no
back-catalogue job in either direction.

## Hardware transcoding — encoding on a GPU

By default every rung is encoded by a **software** encoder (`libx264`, and
`libx265`/`libsvtav1` when the extra codecs are on). That needs no device, no
driver and no special image, works identically on every host, and is what the
ladder's bitrate budgets were tuned against. It is also the slowest option: an
encode is the only part of this pipeline that scales with CPU alone.

`TRANSCODING_HW` moves the **H.264** rungs — and the HEVC ones, when
`TRANSCODING_HEVC_ENABLED=true` — onto a hardware encoder.

| `TRANSCODING_HW` | Encoders | Where it runs | In the shipped image? |
| --- | --- | --- | --- |
| `off` *(default)* | `libx264` / `libx265` | anywhere | yes |
| `vaapi` | `h264_vaapi`, `hevc_vaapi` | Linux, via a DRM render node | **yes** |
| `qsv` | `h264_qsv`, `hevc_qsv` | Linux, Intel GPU + oneVPL runtime | no — needs a rebuilt ffmpeg |
| `nvenc` | `h264_nvenc`, `hevc_nvenc` | Linux, NVIDIA GPU + container runtime | no — needs a rebuilt ffmpeg |
| `videotoolbox` | `h264_videotoolbox`, `hevc_videotoolbox` | macOS only | n/a — unreachable from a Linux container |

The "in the shipped image" column is measured, not assumed: the image's ffmpeg
(Alpine 3.24, ffmpeg 8.1.2) carries `h264_vaapi`, `hevc_vaapi`, `av1_vaapi`, the
Vulkan encoders and the v4l2m2m wrappers, and carries **no** `*_qsv` and **no**
`*_nvenc`. On a stock deployment, `vaapi` is the only backend that can succeed.

### There is no `auto`, and that is the design

Whether a backend works is a property of the **host**, not of the build. The same
image runs on a droplet with no GPU, on a server whose `/dev/dri` was never mapped
into the container, and on a GPU instance. A pipeline that selected an encoder by
looking around would re-tune a whole deployment's picture quality the first time a
device node appeared, moved or vanished — on a kernel upgrade, a hypervisor
migration, or a compose edit made for an unrelated reason.

So hardware is opt-in **by name**, and the tooling makes the opt-in easy instead:

* `vidra doctor` has a **hardware transcode** check. It reports which backends
  look usable against the *api container's* ffmpeg and this host's devices, and it
  never fails — a deployment with no GPU is not misconfigured.
* `vidra setup` prints the same offer as an informational line when it can be sure
  (it can only ask the *host's* ffmpeg, so on a fresh install it usually stays
  quiet and points at `doctor`).

### Giving the container the GPU

The environment variable is not enough. `vaapi` and `qsv` read a DRM render node,
and a container only has one if it is mapped in. `docker-compose.yml` carries
commented-out exemplars on the `api` service; uncomment the one you need.

```yaml
# vaapi / qsv
devices:
  - "/dev/dri:/dev/dri"
group_add:
  - "44"    # video  — check with `getent group video` on the host
  - "104"   # render — check with `getent group render` on the host
```

```yaml
# nvenc — needs nvidia-container-toolkit on the host
deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia
          count: 1
          capabilities: ["gpu", "video"]
```

`TRANSCODING_HW_DEVICE` overrides the render node (default
`/dev/dri/renderD128`). Set it only on a host with more than one GPU — an iGPU
beside a discrete card gets `renderD129` too, and "the first one" is not a
preference the pipeline can hold on your behalf. It must stay **empty** for
`videotoolbox` and `nvenc`, which name no device; the api refuses to boot rather
than ignore a path you wrote.

> **Security.** Mapping `/dev/dri` (or reserving an NVIDIA device) gives the
> container direct access to the GPU and its driver — a real kernel attack
> surface, and one shared with anything else using that card. Do it on hosts you
> control. It is not appropriate on a shared or multi-tenant machine, and it is
> the reason these lines ship commented out rather than merely unused.

### What does not change

A hardware backend may change **how** a representation is produced and never
**what it is**. An H.264 rung encoded by `h264_vaapi` is still `avc1.*`, in the
same adaptation set, at the same bitrate, with the same segment layout — so the
manifests, the serving routes, the progressive downloads and the stored rows are
identical to a software install's. That is what makes turning it back off
config-only.

Three things stay on the CPU whatever the setting says:

* **AV1**, always. Hardware AV1 *encode* exists on Arc, Ada and RDNA3 and nowhere
  else, and on other devices it either is not built or produces something these
  bitrate budgets were never tuned for. `TRANSCODING_AV1_ENABLED` means "spend
  CPU for ~45% fewer bytes"; silently turning that into something else would be a
  different setting wearing the same name.
* **Trick-play**, the dense one-frame-per-second scrubbing rendition. It is its
  own pass, a hardware encoder buys nothing on it, and its rate control is not
  tuned for that shape.
* **The standalone web-video ladder** (`libx264`). On CMAF the `/download` videos
  are *derived* from the ladder rather than re-encoded, so they inherit whatever
  encoded the rungs — and are still H.264 either way.

### When it goes wrong

**At boot.** The api probes `ffmpeg -encoders` and refuses to start if the chosen
backend's encoder is absent, naming the combination. `TRANSCODING_HEVC_ENABLED`
with a backend that has no HEVC encoder in this build is called out specifically,
because the fix is usually `TRANSCODING_HW=off` — which keeps HEVC, on
`libx265` — and not `TRANSCODING_HEVC_ENABLED=false`.

**At job time.** The boot probe proves the *encoder* is in the build. It cannot
prove the *device* is reachable, and that is the failure a real deployment hits:
the image has `h264_vaapi`, so the api boots and looks healthy, and nobody mapped
`/dev/dri` in, so every upload dies. The job error names the backend, the device
path and this setting. A **missing device node is permanent** — the retry finds
the same empty `/dev`, so it dead-letters immediately instead of costing a quarter
of an hour of backoff to say what it already knew. Everything else (a busy GPU, a
wedged driver, an encoder session limit) stays retryable, because those recover.

**There is no automatic per-job fallback to the CPU, on purpose.** A deployment
that quietly fell back would have no way to tell "my GPU is working" from "my GPU
has been broken for a month and every video costs several times what I budgeted";
the failure mode is a performance cliff nobody sees until the queue backs up. The
job fails, the error says which knob to turn, and you decide.

**Turning it off is config-only.** `TRANSCODING_HW=off` and a restart. Already-
encoded trees are ordinary H.264 and keep serving; nothing is re-encoded and there
is no back-catalogue job in either direction.

## Direct delivery — presigned redirects instead of proxying every byte

By default every media byte a viewer receives is read out of the store by the Go
API and streamed through it. That is the **authoritative** path, it re-checks
authorization on every request, and it never goes away. Direct delivery is an
optional shortcut: for requests that are already servable to an anonymous
visitor, the API answers `307` with a short-lived signed object-store URL and the
viewer fetches the bytes from the store instead.

**Turning it on.** One admin setting — `delivery_presign_enabled`, on the
Advanced page under *Delivery*. Default **off**. Flipping it takes effect on the
next request; there is no restart and no env variable. It is inert unless the
store can sign URLs, so on a local-filesystem install it does nothing.

**It is deliberately unavailable in two situations**, both decided at boot:

- `STORAGE_BACKEND=local` — the filesystem has no HTTP surface to redirect to.
- a storage migration is configured (`STORAGE_MIGRATION_TARGET_*`) — during
  dual-read an object may be in either store, and a URL signed against the wrong
  one is a 404 the API can no longer rescue. Finish and unconfigure the
  migration, then enable delivery.

The boot log says which applies (`direct object delivery available` /
`… disabled while a storage migration target is configured`).

**What never gets a signed URL**, whatever the setting says:

| Never redirected | Why |
|---|---|
| Anything not `public` + `published` | Private, unlisted-by-password, draft, scheduled, quarantined and blocked media stay behind per-request authorization. A signed URL is transferable; an authorization decision is not |
| Password-protected media, even with a valid `?pt=` token | The token is scoped to one viewer's unlock; a signed URL would outlive and outrank it |
| Any request carrying `?pt=` or an `Authorization` header | Trading one credential for a longer-lived one, and keeping `?pt=` out of intermediary logs |
| Official downloads while `downloads_enabled` is off, or with the video's own download flag off | Moderators keep their gate bypass for the **bytes**; they do not get a shareable URL |
| HLS playlists (`.m3u8`) | This origin rewrites their relative URIs (playback token, generation version). A copy served from the store points players at URIs that do not resolve |
| `storyboard.vtt` | Its cues reference `storyboard.jpg` relatively; it only works served next to that route |
| The IPFS gateway's own redirect | Unchanged — a pinned public asset still prefers the immutable CID |

Everything else — originals, the VP9 alternate, downloads, extracted audio, HLS
**segments** (canonical and imported-PeerTube shapes), thumbnails, storyboard
sprites, avatars, banners, public playlist covers — is eligible.

**Signature lifetime is one hour** and is not configurable. The redirect itself
is cached for five minutes (`private, max-age=300, must-revalidate`), well inside
that, so a browser never replays an expired signature. The signed URL also pins
the response's `Content-Type`, `Content-Disposition` and a *private* cache policy,
so a redirected download still saves under the creator's filename and a redirected
video still plays inline — those headers are inside the signature and cannot be
edited by the viewer.

**Verifying it.** With the setting on and an S3-backed install. Note these are
**GET requests with the body discarded** (`-o /dev/null -D -`), not `curl -I`:
the media routes are GET-only, so a HEAD answers `405` and tells you nothing
about delivery.

```bash
# A public video's original should answer 307 with a signed Location.
curl -s -o /dev/null -D - https://example.org/api/v1/videos/<id>/original
#   HTTP/2 307
#   location: https://<bucket>.<endpoint>/web-videos/<id>.mp4?X-Amz-Signature=…
#   cache-control: private, max-age=300, must-revalidate

# The same request with a credential must NOT redirect.
curl -s -o /dev/null -D - -H "Authorization: Bearer <token>" \
  https://example.org/api/v1/videos/<id>/original
#   HTTP/2 200
#   cache-control: private, no-store

# A private video's original must NOT redirect, for anyone.
curl -s -o /dev/null -D - -H "Authorization: Bearer <owner-token>" \
  https://example.org/api/v1/videos/<private-id>/original
#   HTTP/2 200

# Follow the signed URL: same bytes, same headers the API would have sent. -L
# really does fetch the whole object, and prints BOTH header blocks — the API's
# 307 and then the store's 200.
curl -s -o /dev/null -D - -L https://example.org/api/v1/videos/<id>/download/original \
  | grep -i 'HTTP/\|content-disposition\|content-type'
```

**Rolling back** is the setting, not a migration: turn `delivery_presign_enabled`
off and the next request is served by the API again. Already-issued signatures
stay valid until they expire (up to an hour) — if you are turning it off because
media leaked, also make the object non-public (delete it, or change its privacy;
the API refuses to sign it again immediately) and rotate the store credentials if
the leak was of the signing key itself.

**Cache headers.** Every media response is `private`. Nothing Vidra serves as
bytes is shared-cacheable, because a shared cache entry can outlive the
authorization decision that produced it. `delivery.Resolver.Purge` now has a
real implementation behind it (see the CDN section below), but nothing calls it
automatically yet, and header promotion is gated on a purge path that has been
*exercised* rather than merely built — so the promotion is still deliberately
unmade. Current policy: whole-file media (originals, downloads, webm, audio)
`private, max-age=3600, must-revalidate`; per-video assets (thumbnails,
storyboards, captions) and identity images `private, max-age=300,
must-revalidate`; HLS `private, max-age=31536000, immutable` on a
generation-versioned URL and `private, max-age=0, must-revalidate` otherwise; and
`private, no-store` for anything requested with a playback token or an
Authorization header. Captions are the one eligible-looking asset that is never
redirected today — they are read as a stream that does not expose a storage key —
so they take the cache policy only.

## CDN delivery — an edge in front of your object store

The same machinery as direct delivery above, with a different destination: for a
request that is already servable to an anonymous visitor, the API answers `307`
with a **CDN edge URL** instead of proxying the bytes or signing an object-store
URL. Vidra contains no CDN-vendor code and names no provider; a CDN is described
entirely by configuration.

**Point the CDN at your object store, not at Vidra.** This is the one thing that
has to be right and that nothing here can check for you. The delivery layer works
in storage object keys, so the edge URL is `DELIVERY_CDN_BASE_URL` + `/` + the
object key:

```text
DELIVERY_CDN_BASE_URL=https://cdn.example.com
  → https://cdn.example.com/streaming-playlists/<uuid>/240p/seg_00000.ts
  → https://cdn.example.com/web-videos/<uuid>.mp4
```

So the CDN's **origin** must serve those same keys: the bucket, or a static
server rooted at the media directory. A CDN pointed at the Vidra API origin 404s
every request — the API addresses media by *route* (`/api/v1/videos/<id>/hls/…`),
not by key — and a 404 from a third party is indistinguishable from a cold cache,
so you will only see it in the browser.

**Turning it on is two steps, deliberately.** `DELIVERY_CDN_BASE_URL` (env,
needs a restart) makes the CDN *exist*; the `delivery_cdn_enabled` admin setting
on the Advanced page under *Delivery*, default **off**, makes it be *used*.
Setting the base URL alone changes nothing. That split is what lets you wire and
restart on a calm afternoon and then flip delivery on — and back off, in seconds,
with no restart — during an incident. The boot log confirms what was accepted
(`cdn delivery available`).

**Ordering.** When more than one optional source can serve an object, the order
is IPFS gateway → CDN → presigned → API proxy. The CDN beats a presigned URL
because a signed URL is a per-viewer bearer credential that expires, is
uncacheable at every layer, and meters object-store egress per viewer. The IPFS
mirror keeps its place ahead of both — it shipped first and has its own master
switch, so if you want the edge to take thumbnails, turn the mirror off.

**What never goes to the edge** is exactly the direct-delivery table above:
non-public or unpublished media, anything carrying `?pt=` or an `Authorization`
header, HLS playlists, and `storyboard.vtt`. A CDN can therefore front only
public, published, uncredentialed media. **CDN-fronted private playback is not
this feature** — it needs signed-URLs-at-the-edge, which is a different
mechanism and a later decision.

**Purge.**

```bash
DELIVERY_CDN_PURGE_URL={url}            # Varnish / nginx purge module
DELIVERY_CDN_PURGE_METHOD=PURGE         # the default; may be omitted

DELIVERY_CDN_PURGE_URL=https://api.example.com/purge?url={url_encoded}
DELIVERY_CDN_PURGE_METHOD=POST
DELIVERY_CDN_PURGE_TOKEN='Bearer <token>'   # default header: Authorization
```

Placeholders are `{url}` (the edge URL), `{url_encoded}` (the same, encoded for
a query value) and `{key}` (the object key alone). `DELIVERY_CDN_PURGE_TOKEN` is
a **secret**: sent header-only, never logged, and never echoed in an error — the
request URL is stripped out of transport errors too, because some purge APIs want
the credential in the query string.

Leaving `DELIVERY_CDN_PURGE_URL` empty is legal and means the edge cannot be
invalidated from here: a deletion, a privacy flip or a takedown will not reach
it. The API warns about that once at boot rather than at the moment you need it.

**Nothing calls purge automatically yet**, and that is on purpose. Automatic
invalidation only becomes load-bearing once a media response is promoted from
`private` to shared caching, and that promotion is a separate, riskier change
gated on a purge path that has actually been fired in anger. Until then every
byte route stays `private`, exactly as documented above, and the edge caches only
what a viewer's own browser would have.

**Verifying it.** As with direct delivery these are GET requests with the body
discarded — the media routes are GET-only and a `curl -I` answers `405`.

```bash
# A public video's original should answer 307 to the edge.
curl -s -o /dev/null -D - https://example.org/api/v1/videos/<id>/original
#   HTTP/2 307
#   location: https://cdn.example.com/web-videos/<id>.mp4
#   cache-control: private, max-age=300, must-revalidate

# Follow it: the edge must serve the same bytes. A 404 here means the CDN's
# origin is not key-addressed — see the top of this section.
curl -s -o /dev/null -D - -L https://example.org/api/v1/videos/<id>/original

# A credentialed request must NOT redirect.
curl -s -o /dev/null -D - -H "Authorization: Bearer <token>" \
  https://example.org/api/v1/videos/<id>/original
#   HTTP/2 200
#   cache-control: private, no-store
```

**Rolling back** is the setting, not a migration: turn `delivery_cdn_enabled`
off and the next request is served by the API again. Turning delivery off does
**not** evict what the edge already holds — if you are turning it off because
something leaked, purge the object (or make it non-public, which stops Vidra
handing out its edge URL) as well.

## Playback quality (QoE) — measuring whether delivery is actually better

Enabling a CDN raises an obvious question the section above cannot answer: is it
faster for *your* viewers? `GET /api/v1/admin/qoe/playback-health` answers it.
With no parameters it reports time-to-first-frame and rebuffer percentiles per
delivery source for the last 24 hours, so `cdn` and `api-proxy` sit next to each
other in the same table.

Nothing is sent anywhere. There is no analytics provider, no external service and
no egress; the measurements are stored in this instance's own database and aged
out on a fixed schedule.

**The switch** is the `qoe_collection_enabled` instance setting (Advanced →
delivery, beside the two delivery toggles). Unlike those two it defaults **on**,
because it costs nothing and an instance that measures nothing cannot answer the
question above. Turning it off stops collection on the very next beacon — no
restart — and leaves what is already stored alone.

**What is stored about a viewer.** No IP address and no account id. Each event
carries a keyed digest derived from the instance's `JWT_SECRET`, scoped to a
single UTC day: within one day two events from the same viewer are recognisable
as the same viewer (which is how you tell one bad connection from a thousand),
and across days nothing links. Rotating `JWT_SECRET` re-derives the key, so
digests written before and after a rotation never correlate — that is intended,
and it changes no count, percentile or rollup.

**Retention** is fixed and enforced by a leader-elected worker, so exactly one
instance prunes no matter how many are running:

| Table         | Kept     | What it is                                    |
| ------------- | -------- | --------------------------------------------- |
| `qoe_events`  | 7 days   | Individual measurements — the incident detail. |
| `qoe_rollups` | 90 days  | Hourly counts and percentiles — what the admin view reads. |

A second leader-elected worker turns closed hours into rollups every 10 minutes.
If both api and worker roles are running (see *Splitting the api and the
workers*), the workers run in the **worker** role only.

**Two numbers that mean less than they look.** `verified_session_count` is how
many events carried a playback session id the server could check against a
signed token — which today happens only for password-protected videos, so on a
normal public catalogue it is 0 and the session ids are client-asserted. And
`rendition_reporting_supported` is `false` for the `native-hls` engine (Safari,
iOS) permanently: the browser picks the variant itself and exposes no hook, so
zero bitrate switches there is a capability gap, not flawless adaptive streaming.

## IPFS mirror (pinset) — a distribution surface, not a backup

When `IPFS_ENABLED=true` (`IPFS_API_URL` + `IPFS_GATEWAY_URL` required), the mirror
add+pins **only already-public media** — public+published videos and their
derivatives (HLS tree, thumbnail, storyboard, captions, VP9), non-unlisted
user/channel avatars+banners, and public playlist covers — to a Kubo node, records
each CID in the `media_ipfs_pins` ledger, and exposes the CID / gateway URL
additively in API responses. Nothing private/unlisted/quarantined/DM/export is ever
pinned (the eligibility gate is a hard privacy fence). Local/S3 stays the only
authoritative copy.

For bandwidth offload, the existing stable asset endpoints return a short-cache
`307 Temporary Redirect` to `{IPFS_GATEWAY_URL}/ipfs/{cid}` when the ledger row has
the expected media class, is `pinned` on the **public** network, and contains a valid
CID. This applies to public video thumbnails, storyboard JPEG sprite sheets,
user/channel avatars and banners, and public playlist covers. The storyboard VTT
remains on the application endpoint so its relative `storyboard.jpg` cue stays
correct. If a pin is absent, pending, failed, private-network, invalid, or the lookup
fails, the same request transparently serves the authoritative local/S3 object.
Private-swarm CIDs are never used in a redirect or exposed to clients.

### Local-only vs live public mode

The compose `ipfs` service is local-only unless the operator explicitly sets
`IPFS_PUBLIC_NETWORK=true`. Its init hook blocks all swarm addresses, removes
bootstrap peers, and disables routing, providing, and relay/NAT traversal in local mode. Live mode restores the
public bootstrap/DHT provider configuration and enables relay reservations plus
hole punching so a NAT'd node can actually serve an independently operated public
gateway. Merely having outbound swarm peers is not proof of public retrievability.

From the meta-repository, `make ipfs-live` starts both tiers with the intended
split: `IPFS_ENABLED=true`, public networking on, the client gateway defaulting to
`https://ipfs.io`, and `IPFS_MIRROR_PRIVATE=true` pointed at the separate keyed
node. To exercise the backend node directly:

```bash
IPFS_ENABLED=true \
IPFS_PUBLIC_NETWORK=true \
IPFS_GATEWAY_URL=https://ipfs.io \
docker compose --profile core --profile ipfs up -d --build
```

The Kubo RPC (`:5001`), private RPC (`:5002`), local gateway (`:9090`), and private
Cluster REST (`:9094`) bind to host loopback in the reference compose file; never
publish an RPC port to an untrusted network. Only the public libp2p swarm port
(`:4001` TCP+UDP) is host-public. The live integration workflow adds a fresh real
MP4, retrieves its unique CID through an independent public gateway, and compares
the bytes. `make test-ipfs-integration` keeps this proof opt-in locally via
`IPFS_TEST_PUBLIC_GATEWAY_URL` + `IPFS_TEST_VIDEO_PATH`.

**The pinset is a distribution surface, never a backup.** Do **not** back up the
Kubo datastore for durability — it holds only re-derivable copies of already-public
bytes:

- **Backup source of truth stays PostgreSQL + the authoritative blob store.** The
  DB dump + media snapshot (above) is a complete, restorable backup on their own.
- **The pinset is re-derivable.** After a node loss, a fresh node, or a restore,
  run `POST /api/v1/admin/ipfs/reconcile` (admin, audited). It re-arms any
  dead-lettered pins and seeds a pin intent for every eligible public object that
  has no ledger row; the mirror worker then re-adds+pins them from the authoritative
  store. It is idempotent — a second run enqueues zero. Watch progress and node
  health with `GET /api/v1/ipfs/status` (counts per state/class + `node_reachable`).
- **Node GC.** Unpinning (on delete, or when a video goes private / a user goes
  unlisted) only marks a CID's blocks collectable — actual disk is reclaimed by the
  node's own garbage collector, `ipfs repo gc` (run it on a schedule or when the
  datastore grows). A CID is unpinned only after a reference check confirms no other
  live pin shares it (content-address dedupe).
- **⚠️ Unpin ≠ erasure on a public network.** Once a CID is public it may have been
  fetched, cached, or re-pinned by other nodes and the DHT; unpinning removes *our*
  obligation to serve it but cannot guarantee erasure elsewhere. Treat every public
  pin as a **permanent public disclosure** — which is exactly why only
  already-public media is ever eligible. If an object must be provably unrecoverable,
  it must never have been public in the first place. Replicating **non-public** media
  is a separate tier with its own node and its own rules — see the next section.

## Private IPFS mirror tier (P19.P) — replication, not distribution

`IPFS_MIRROR_PRIVATE=true` opts the instance into replicating **non-public** media
(private + unlisted videos and all their derivatives, non-public playlist covers,
unlisted/deactivated-owner avatars & banners) to a **second, fully separate
swarm.key'd Kubo node** — never the public-mirror node. Quarantined content is
mirrored nowhere; DM attachments are never mirrored on any network. This tier is a
**durability/DR-replication surface (the STOR-05 story for non-public media), not a
playback path** — the failure mode is always "private content unreachable", never
"private content public".

**Config (supersedes the old guard).** The shipped v1 used
`IPFS_MIRROR_PRIVATE ⇒ IPFS_CLUSTER_API_URL` as a proxy for "private infra exists";
that guard is **replaced** by the correct, stricter shape:

| Env | Meaning |
|-----|---------|
| `IPFS_MIRROR_PRIVATE=true` | master opt-in; **requires** `IPFS_PRIVATE_API_URL`. |
| `IPFS_PRIVATE_API_URL` | RPC of the **dedicated** private-swarm Kubo. `== IPFS_API_URL` is a hard boot error (`refusing to dual-home`). Allowed with `IPFS_ENABLED=false` (private-only tier). |
| `IPFS_PRIVATE_CLUSTER_API_URL` / `IPFS_PRIVATE_CLUSTER_TOKEN` | optional IPFS Cluster REST + auth token (secret — redacted, never logged/committed) for multi-node replication on the private swarm. |
| `IPFS_PRIVATE_ADD_TIMEOUT` / `IPFS_PRIVATE_PIN_CONCURRENCY` | per-network worker tuning (inherit the public defaults). |

There is **deliberately no `IPFS_PRIVATE_GATEWAY_URL` knob** — not having it is the
guarantee that a private CID is never emitted with a gateway URL. Private CIDs never
appear in any API response (`ipfs_pinned`/`ipfs{}` stay public-network-only signals);
viewer serving of private/unlisted media is unchanged, straight from the authoritative
local/S3 store through the authenticated app API.

**Node configuration (the private Kubo).** Run it fail-closed and quiet — the
`ipfs-private` compose profile does this for dev; production keys are operator-managed:

- `LIBP2P_FORCE_PNET=1` — the daemon **refuses to boot** without a `swarm.key`
  (fail-closed: keyless never means "fell back to the public network").
- Default bootstrap **cleared** (`ipfs bootstrap rm --all`) — you self-manage peers;
  `Routing.Type=none` (explicit peering only, no DHT) + `Provide.Enabled=false`
  (no content announcements) keep even the private swarm from advertising CIDs.
- `Gateway.NoFetch=true`, gateway **bound internally / never host-published** — no
  public gateway route exists for this node. If DR tooling ever needs gateway reads,
  that is an infra decision (reverse-proxy auth) outside the app contract; the app
  never links to it.

The dedicated private integration workflow adds a fresh real MP4 to one keyed
node, replicates and compares it byte-for-byte on the second keyed node, and then
proves a keyless public node cannot connect to the PNet or retrieve its CID.

**swarm.key custody — the operational realities (accept these before enabling):**

- **Generation** (dev convenience only; prod keys are operator-made): a 32-byte
  pre-shared key under the PNet header, e.g.
  ```bash
  printf '/key/swarm/psk/1.0.0/\n/base16/\n' > swarm.key
  od -A none -t x1 -v /dev/urandom | head -c 64 | tr -d ' \n' >> swarm.key
  printf '\n' >> swarm.key
  ```
  Place it at `$IPFS_PATH/swarm.key` on the node.
- **Distribution is manual.** The **same** key must be copied to **every** node in the
  private swarm (and every cluster peer). Peers holding different keys cannot talk.
- **Possession = full membership.** There is **no per-node revocation** and no online
  rotation — holding the key is complete network access, full stop. Treat it like a
  root credential.
- **Rotation = new key + coordinated full restart.** To rotate you generate a new key,
  distribute it to every node, and restart them together; nodes straddling old/new keys
  are partitioned until they all match. Plan a maintenance window. (These are
  community-acknowledged gaps in LibP2P PNet, not a Vidra limitation —
  [kubo experimental-features](https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md),
  [identity/revocation discussion](https://discuss.ipfs.tech/t/discussion-identity-revocation-and-node-roles-in-private-ipfs-networks/20247).)
- **Secrets hygiene.** `swarm.key` and `IPFS_PRIVATE_CLUSTER_TOKEN` are secrets:
  redacted in logs, **never committed** (the compose profile references a gitignored
  key-file path; `deploy/ipfs-private/*.key`, `swarm.key`, `cluster-secret`, `*.peerid`
  are `.gitignore`d), and distributed out-of-band.

**What breaks by design (this is correct, not a bug):**

- Public gateways (`ipfs.io`, `dweb.link`, …) can **never** resolve a private-swarm CID.
- External pinning services **cannot join** — they aren't keyed into the swarm.
- Cross-instance replication is limited to peers you explicitly key in; there is no
  federation-wide private fetch. NAT traversal/AutoRelay behave differently on PNet —
  irrelevant for a LAN/VPC cluster, a consideration if peers span NATed sites.
- **Unpin behaves better than on the public network.** Inside a closed, un-announced
  swarm there is no public DHT to have leaked the CID and no outside node that could
  have cached it, so unpin + `ipfs repo gc` on the keyed nodes is effective removal
  (contrast the public "unpin ≠ erasure" caveat above).

**DR-restore from the private pinset.** The private pinset is re-derivable exactly like
the public one — PostgreSQL + the authoritative blob store remain the only backup. After
a private-node loss / fresh node / restore, re-seed and re-pin the non-public tier:

```bash
# Re-arm dead-lettered pins + seed a pin intent for every private-eligible object
# that has no ledger row, scoped to the private swarm. Idempotent (a second run
# enqueues zero). Admin-only, audited (admin.ipfs.reconcile).
POST /api/v1/admin/ipfs/reconcile?network=private
```

Watch progress and node/cluster health at `GET /api/v1/ipfs/status` — the additive
`networks.private` block reports `{enabled, node_reachable, cluster_enabled,
cluster_reachable, pins{by state}, by_class[]}` independently of the public block.
Omit `?network=` to reconcile both swarms; pass `network=public` for the public tier
only.

## Restore drill

Quarterly: restore the latest DB dump + media snapshot into a throwaway stack,
run `make migrate-up`, boot the api, and verify `/readyz` is 200 and a known
video plays. A backup you have never restored is not a backup.

## Observability surfaces (P17)

All opt-in and zero-cost when disabled.

- **Structured logs**: `LOG_LEVEL` (`debug|info|warn|error`) + `LOG_FORMAT`
  (`json` prod / `text` dev). One `slog` line per request with `request_id`,
  `correlation_id`, and (with tracing on) `trace_id`/`span_id`. Request logs use
  the Echo route template (or URL path for an unmatched route), never the raw
  query string: OAuth codes/state, playback tokens, searches, signed URLs, and
  future secret parameters therefore cannot leak through the URI field. The
  `TestNoSensitiveLogKeys` / `TestNoForbiddenLogging` guards in `make ci` prevent
  unsafe logging primitives and known sensitive structured keys; they cannot
  prove that arbitrary error/message values are safe.
- **Metrics** (`METRICS_ENABLED=true`): Prometheus RED metrics at **`GET /metrics`**
  (`vidra_http_requests_total`, `vidra_http_request_duration_seconds` labelled by
  method / route template / status class — bounded cardinality, no ids or raw
  URLs) plus a `vidra_queue_depth{queue,state}` gauge and the connection-pool
  series `vidra_db_pool_total_conns` / `_idle_conns` / `_acquired_conns` /
  `_max_conns` with `vidra_db_pool_empty_acquires_total` and
  `vidra_db_pool_acquire_wait_seconds_total`. The endpoint is
  unauthenticated and lives at the root like the health probes — **network-scope
  it** (internal Prometheus scrape only), do not expose it publicly. It is
  intentionally omitted from the public OpenAPI contract (an ops surface).

  The pool series is the one to watch before adding a replica. `_acquired_conns`
  pinned at `_max_conns` with a rising `rate(vidra_db_pool_empty_acquires_total)`
  means requests are queueing for a connection rather than for the database —
  that is `DB_MAX_CONNS` being too small for this process, not PostgreSQL being
  slow, and the two have opposite fixes. Each process exports its own; the sum
  across processes is what PostgreSQL's `max_connections` has to cover.
- **Health probes**: `GET /healthz` is dependency-free liveness (is the process
  up). `GET /readyz` is the load balancer's question — *should I send this
  instance traffic* — and answers it deliberately narrowly:

  | Condition | HTTP | `status` |
  |---|---|---|
  | everything reachable | 200 | `ok` |
  | Redis down, PostgreSQL up | 200 | `degraded` |
  | PostgreSQL down | 503 | `unavailable` |
  | SIGTERM received, listener still open | 503 | `draining` |

  **Only PostgreSQL takes a replica out of rotation.** It is the system of
  record; without it nearly every route is a 500. Redis does not, because every
  rate limiter fails *open* on a Redis error and the rest of its users are
  caches — and because all replicas share one Redis, 503ing on it would empty
  the whole fleet from the load balancer at the same instant, turning a partial
  loss of rate limiting into a total outage. The body still names the down
  component either way; `vidra status` renders it as a ⚠.

  The result is cached for ~2s, so polling `/readyz` from a dozen balancers and
  uptime checks costs the same pooled DB connection as polling it from one.
- **Tracing** (`OTEL_ENABLED=true`): OTLP spans for HTTP requests, PostgreSQL
  queries, Redis commands, and outbound HTTP (federation/import/whisper/atproto),
  with inbound + outbound W3C `traceparent` propagation. Run a local backend with
  `docker compose --profile core --profile otel up` and set
  `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317`; view traces at
  http://localhost:16686 (Jaeger).
- **Admin dashboards**: `GET /api/v1/admin/system` (build/uptime/dependency
  health) and `GET /api/v1/admin/jobs` (per-queue depth + oldest-pending age +
  recent failures) back the admin ops pages. A rising `oldest_pending_age_seconds`
  or growing `pending` is the stuck-worker signal; dead-lettered jobs appear as
  `failed` + in `recent_failures`.

There is intentionally no administrator-facing System Logs API yet. Stdout is
the canonical deployment log stream. Before records can be copied into a
searchable multi-instance sink, Vidra needs a central recursive value sanitizer
and record-size bound, a non-blocking bounded writer with a drop metric and no
recursive self-logging, and independent retention/pruning. Never expose current
raw `slog` values through a database, file reader, or process-local ring buffer.
See `docs/operational-observability-phase1.md` for the staged design.

## Production deployment notes

The reference deployment is one host per environment behind a TLS proxy — see
[`deploy/README.md`](../../deploy/README.md). Backend-specific fail-secure rules
enforced at boot in `VIDRA_ENV=production`:

- Refuses the dev `JWT_SECRET`; requires a strong secret.
- Refuses `DEV_MAIL_CAPTURE_ENABLED`.
- Requires `FEDERATION_KEY_KEK` when `FEDERATION_ENABLED=true` (actor private keys
  are envelope-encrypted at rest); likewise set `MFA_KEY_KEK` for TOTP secrets.
- Marks auth cookies `Secure`; `PUBLIC_BASE_URL` / `CORS_ALLOWED_ORIGINS` must
  match the public domains.
- Keep `HTTP_IMPORT_ALLOW_PRIVATE_URLS=false` (the SSRF guard) — it is a dev/e2e
  escape hatch only.

Health/readiness for orchestrators: `GET /healthz` (liveness) and `GET /readyz`
(503 until PostgreSQL + Redis are reachable). See the top-level
[`README.md`](../README.md) for the full env-var and endpoint reference.

## Running more than one api instance

Several `vidra-core` api instances can share one PostgreSQL. What makes that safe
is two mechanisms, and it is worth knowing which does what:

**Queue work is leased.** Every durable queue claims rows with
`FOR UPDATE SKIP LOCKED` and pushes the row's due time forward by a lease
(30 minutes), which the owning worker renews every 5 minutes while it works. Two
instances therefore never claim the same job, and an instance that dies stops
renewing so its rows return to the queue by themselves — no boot-time requeue and
no assumption about which instances are alive. More instances mean more transcode,
import, caption and delivery throughput.

**Singleton sweeps are leader-elected.** The workers that sweep rather than claim
(media garbage collection, the content-hash backfill, scheduled publish, the
transcode-hold sweep, the upload sweeper, the live watchdog, the search and IPFS
reconcilers, operational-job retention, the E2EE sweep) each walk a table or a
bucket and act on what they find, so running them everywhere duplicates work —
and media GC deletes. They are
gated on a PostgreSQL advisory lock held on a dedicated connection: exactly one
instance runs them, and the lock is released by the server itself when that
instance dies. Leadership moves within ~15 seconds.

Media GC additionally carries its own rails — an enable flag, a dry-run-first
first sweep, an orphan-ratio breaker and the bucket-ownership marker — because
leadership only decides *how many* instances sweep, not whether the sweep is
right. See [Media garbage collection](#media-garbage-collection--the-job-that-deletes).
Each instance spends its own first-sweep dry run, so a leadership move re-arms
that rail on the new leader.

### Connection budget — the thing that breaks first

Every process opens its own pgx pool of up to `DB_MAX_CONNS` (default 10)
connections. PostgreSQL's `max_connections` is server-wide. **The number that has
to fit is `DB_MAX_CONNS` × processes**, and processes means api replicas *plus*
worker replicas — the `worker` service merges the same `x-api-env` anchor, so it
gets the same pool size.

At the defaults, one all-in-one process needs 10 of a stock server's 100. An
api + worker split needs 20. Three api replicas and three workers need 60, and a
managed plan capped at 25 or 40 connections is exceeded well before that. The
failure is not gradual: it is `FATAL: sorry, too many clients already` on
whichever process connects *last*, which during a rolling deploy is the new one —
so the deployment that fails is the one you just started, and the one still
running looks fine.

Lower `DB_MAX_CONNS` per process rather than chasing `max_connections`, which on
a managed plan you usually cannot move. Two rails exist to keep that honest:

- `DB_MAX_CONNS` must be **at least 2**, and the api refuses to boot below it. On
  any role that runs workers the singleton-cron elector checks one connection out
  of this pool and holds it for as long as that instance is the leader (that is
  what makes the advisory lock release itself on death), so a pool of 1 would
  leave nothing to run queries on — and the symptom is every request hanging on
  connection acquire, with no error anywhere.
- `vidra doctor` reads your server's actual `max_connections` and prints the
  arithmetic, warning when the pool is tight for more than one process. It never
  fails a run: a small pool is a legitimate configuration, and doctor cannot see
  how many processes you are running.

`vidra_db_pool_empty_acquires_total` on `/metrics` is how you tell whether a
smaller pool is actually costing anything: it counts the acquires that had to
wait because every connection was checked out.

`DB_MIN_CONNS`, `DB_CONN_MAX_LIFETIME` and `DB_CONN_MAX_IDLE_TIME` come with the
same defaults the pool always had (1, 1h, 30m). The lifetime is what lets a
failover, a rotated credential or a connection-pooling proxy take effect without
restarting the process.

### Rolling restarts — `/readyz` and the drain delay

`/readyz` returns 503 as soon as SIGTERM is received, **while the listener stays
open**. That gap is what `HTTP_DRAIN_DELAY` (default `0`) is for.

A load balancer finds out a replica is going away by *polling* readiness. With no
delay the sequence is: last health check passes → listener closes → the requests
already in flight toward this replica are refused. With one, readiness goes red
first, the balancer takes the instance out of rotation, and only then does the
socket close on traffic nobody is routing any more.

Set it to at least **twice your health-check interval** — more if your balancer
requires several consecutive failures before it acts. It is spent *before* the
`HTTP_SHUTDOWN_TIMEOUT` drain of in-flight requests, not out of it, so the two add
up; under Docker both have to fit inside the container's `stop_grace_period`
(10s by default) or SIGKILL ends the argument. A second SIGTERM ends the wait
early, so an operator in a hurry does not have to escalate to SIGKILL.

Zero is the right value for a single-node install: nothing is making routing
decisions about it, so the wait would only slow the restart down. Nothing changes
for an install that does not set it.

### What was actually tested

Two api replicas against one PostgreSQL, verified end to end rather than by
inspection:

| Property | How it was checked |
|---|---|
| Both replicas serve | `GET /healthz` 200 on each replica's port |
| Exactly one leader | one `elected leader` log line; `pg_locks` shows exactly one advisory holder |
| Failover on crash | `SIGKILL` the leader → advisory holders drop to 0, a new leader is elected ~11s later |
| No duplicate work under load | 400 outbox events drained concurrently by both replicas → 406 unique events, 406 deliveries, **0 duplicates** |

The last row is the one that matters, and it was checked against a counterfactual:
with the lease and `SKIP LOCKED` removed and the image rebuilt, the same run
produced **423 deliveries for 406 events — 17 duplicates**. A "no duplicates"
result from a harness that cannot detect duplicates would prove nothing.

Reproduce with `deploy/docker-compose.soak.yml`:

```sh
docker compose -f docker-compose.yml -f deploy/docker-compose.soak.yml \
  --profile core up -d --scale api=2
```

### Caveats, stated plainly

- **This is not a scaling recommendation.** It is evidence that the concurrency
  primitives hold under a real two-process topology. Capacity planning, session
  affinity, rolling deploys and load balancing are not covered here.
- **A partitioned leader still holds its lock** until PostgreSQL notices the TCP
  session is gone (`tcp_keepalives_*`, minutes by default). During that window the
  singleton sweeps do not run anywhere. That is the correct failure to have —
  pausing periodic maintenance is safe in a way that running media GC from two
  instances is not.
- **Local media storage does not scale out.** `STORAGE_BACKEND=local` writes to a
  volume only one host can see; multi-instance requires `STORAGE_BACKEND=s3`.
- **Live does not scale out at all**, on any storage backend. See below.
- **The soak ran without an object store or a real search service.** Transcode
  throughput across instances has not been measured under load.

### The live plane is single-host

Everything above is about the api replicas. **Live streaming is not covered by any
of it, and it is single-host by construction.** This is a supported-topology
statement, not a defect list — read it before planning a multi-node deployment
that includes live.

**Live segments never enter the media store.** While a broadcast is running, the
RTMP media server (nginx-rtmp) writes the HLS playlist and its MPEG-TS segments
straight onto a **local Docker volume** (`LIVE_HLS_ROOT`, the `live_hls` volume in
`docker-compose.yml`), and the api serves them by opening the file. They are never
uploaded to `STORAGE_BACKEND`, they have no storage key, and they exist for a
window of about twelve seconds before being deleted. The media server also
**reuses segment names** — the segment called `<id>-0.ts` in this broadcast is a
different segment from the `<id>-0.ts` of the last one.

Three consequences follow, and none of them are configurable:

- **A second api instance cannot serve live.** An api replica on another host sees
  an empty `LIVE_HLS_ROOT`, so every live request it receives is a 404 — and a
  404 that is *indistinguishable* from "no such stream" or "not currently live",
  because live 404s deliberately do not leak which. If you run more than one api
  instance, either keep all of them on the host holding the live volume, or route
  `/api/v1/live/{id}/hls/*` to that host. There is no error message that will tell
  you that you got this wrong; you will see a live stream that never starts.
- **There is no CDN, presigned or IPFS path for live.** The whole delivery
  abstraction (`Direct delivery`, the IPFS mirror, and any CDN configured later)
  operates on stored objects addressed by a storage key. Live bytes have no key,
  no immutable version and no durable copy to point a cache at, so they are always
  served by the api itself, from the origin. A CDN in front of Vidra must not be
  configured to cache `/api/v1/live/…` — the playlist changes every couple of
  seconds and the segment names repeat across broadcasts, which is the exact shape
  of a cache that serves the wrong video. The responses are marked
  `private, max-age=0, must-revalidate` (or `private, no-store` when they carry a
  credential) to say so.
- **Recordings are different.** None of this applies to a replay: when
  `replay_enabled` is set, the finished broadcast is transcoded into an ordinary
  video, and from that point on it is a stored object like any other, with every
  delivery path available to it.

Fixing this properly means replacing the media server's HLS muxer with a
repackager inside Vidra that writes segments through the storage backend. That is
a rewrite of the live plane, it is not planned, and no amount of delivery or
storage configuration substitutes for it.

## Splitting the api and the workers

A default install runs one process that does everything: it serves HTTP *and* runs
every background worker. That is `VIDRA_ROLE=all`, and nothing below is required to
run Vidra.

The reason to split is **ffmpeg**. Transcoding runs inside the api process, so in
the single-container topology a busy transcode queue competes with request handling
for the same CPU and memory, and the only way to give the transcoder more headroom
is to give the HTTP server more too. `VIDRA_ROLE` separates the two halves of the
same binary — same image, same configuration — so each can be sized and scaled for
what it actually does:

| `VIDRA_ROLE` | HTTP listener | Background workers |
|---|---|---|
| `all` (default) | yes | yes |
| `api` | yes | **no** |
| `worker` | **no** | yes |

An unrecognised value refuses to boot. The failure it prevents is the quiet one: an
install where nothing transcodes, imports, mirrors or sweeps, whose only symptom is
a queue depth that grows forever.

### Doing it with Compose

```sh
# Add workers beside a full api (the api still runs workers too).
docker compose --profile core --profile worker up -d --scale worker=3

# The real split: the api serves only, the workers do all the background work.
API_ROLE=api docker compose --profile core --profile worker up -d --scale worker=3
```

`worker` layers onto `core`; it is not a stack of its own. The worker service is the
api service with one variable changed — it merges the same `x-api-env` anchor, so a
new configuration key reaches both halves without anyone remembering to copy it.

**If you set `API_ROLE=api`, you must run at least one worker.** There is no
interlock for this and there deliberately is not one: a process cannot tell whether
some other container is draining the queues.

### Local storage stops working the moment the split crosses machines

`STORAGE_BACKEND=local` writes media to a filesystem. On the stock stack that is
fine and it is what you get by default: the `api` and `worker` containers are
different processes on the *same host* sharing the `media_data` volume, so a
rendition the worker writes is a file the api can open.

Put the worker on a **different machine** and the same configuration is silently
wrong. The transcode succeeds, the rendition lands on a disk no api instance can
read, and every playback request for it is a 404 — with nothing in any log to say
why, because from each process's point of view nothing failed. **A multi-machine
fleet requires `STORAGE_BACKEND=s3`**, which is the same requirement multiple api
instances have (see [the multi-instance caveats](#caveats-stated-plainly)).

A worker booting with `VIDRA_ROLE=worker` and `STORAGE_BACKEND=local` logs one
loud warning saying so. It is a **warning and not a refusal** on purpose: refusing
would break the default single-host install, which is a legitimate use of exactly
this configuration, and no process can tell from the inside whether the filesystem
it is writing to is the same one another container is reading.

### Sizing

With the split on, the api container's envelope can shrink — it is serving JSON and
streaming bytes, not encoding video. In the production overlay that is `API_CPUS` /
`API_MEM_LIMIT`; give the freed capacity to the workers (`WORKER_CPUS` /
`WORKER_MEM_LIMIT`) and budget roughly **4× `UPLOAD_MAX_SIZE` of scratch per
concurrent transcode** on the worker's `TMPDIR` volume.

Scaling workers is safe for the same two reasons running several api instances is
(see [Running more than one api instance](#running-more-than-one-api-instance)):
queue work is leased and claimed with `FOR UPDATE SKIP LOCKED`, and the sweep-only
crons are behind a single advisory lock. `--scale worker=3` is three times the
transcode throughput, not three times the same job.

### The leader-election rule

The singleton-cron elector runs **only in roles that run workers**. An api-only
process does not stand for election at all.

This is not an optimisation. If an api-only process could win the advisory lock, it
would hold it while running zero leader-gated sweeps — media GC, the content-hash
backfill, operational-job retention, the transcode-hold sweep and the rest would
simply stop — and the worker that *can* run them would sit as a follower waiting for
a leader that never yields. The symptom would be nothing at all in the logs.

### Health checks

**A worker has no HTTP listener, so it has no health endpoint.** The image bakes an
HTTP `HEALTHCHECK`, which a worker can only ever fail; the Compose `worker` service
disables it (`healthcheck: {disable: true}`), and any hand-rolled deployment must do
the same or every worker container will be marked unhealthy and anything waiting on
`service_healthy` will hang. Liveness for a worker is process liveness — let the
supervisor restart it if it exits. `GET /readyz` on the api still reports PostgreSQL
and Redis — though only PostgreSQL 503s it, see
[Observability surfaces](#observability-surfaces-p17) — and queue depth and
stalled-queue age are on `/metrics` (`METRICS_ENABLED=true`), which is where a
worker outage is actually visible.

### Shutdown

A worker exits on `SIGTERM` without draining in-flight jobs, by design. Every claim
is held under a renewed lease, so a job interrupted mid-flight is handed back by the
lease-expiry recovery sweep — running on whichever instance is still up — within one
lease. Nothing has to know which workers were alive.

### Rolling it back

Config-only, no data change: drop `API_ROLE` (or set it to `all`), stop the `worker`
profile, restart. The api is running every worker again on the next boot.

## Migrating from PeerTube (`cmd/peertube-import`)

Findings from a real migration attempt against a live PeerTube instance,
2026-08-23. Read this before planning one.

### The binary ships nowhere

`Dockerfile` builds only `./cmd/api`, and the release assets carry only the
`vidra` CLI, so there is no supported way to obtain the importer. Build it:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o peertube-import ./cmd/peertube-import
```

### It needs the source DATABASE, not the source host

The importer reads the source PeerTube **PostgreSQL directly** (`--source-dsn`);
it is not an API client. Media is separate and can come straight from object
storage with `--source-storage=s3`, which matters more than it sounds: on an
instance whose media already lives in S3/B2, **no media ever transits the source
host**, and the only thing needing reach into it is the database. PeerTube binds
Postgres to `127.0.0.1`, so that reach is an SSH tunnel — and a tunnel is all the
access the migration requires.

The target is read from the ordinary server config (`config.Load()` —
`DATABASE_URL`, `STORAGE_*`, `FEDERATION_KEY_KEK`), so the importer runs
wherever the Vidra config lives, usually the new host.

### `--source-dsn` has no secret indirection

Every other secret-bearing entry point in this project accepts `@path`, `-` for
stdin, or an environment variable — `vidra setup` documents exactly that, and the
importer's own `--source-s3-access-key` / `--source-s3-secret-key` honour
`PEERTUBE_SOURCE_S3_*`. `--source-dsn` does not, so a production database
password lands in `argv` where any local user can read it from `ps`. Treat that
as a gap to close, and in the meantime run the import on a host where that
exposure is acceptable.

### The version gate is real, and `--force` is not an agent's decision

`ClassifyVersion` accepts source `migrationVersion` in **[700, 1000]** and
refuses anything outside it:

```
source schema version 1040 is newer than the verified range [700, 1000]
— pass --force only after verifying compatibility
```

The flag's help says *"HUMAN operators only; agents MUST NOT set this"*, and that
is the right boundary: the refusal is a statement about what has been **verified**,
not about what happens to work.

What "verifying compatibility" can reasonably mean, given the importer only reads:

1. **Check every column it references still exists.** Extract the quoted
   identifiers from the importer's SQL and diff them against
   `information_schema.columns` on the source. A removed or renamed column is the
   failure that would break the run loudly. On a 1040 source this came back
   **clean** — no referenced identifier was missing.
2. **Check what the newer schema added that the importer does not read.** This is
   the quieter risk and the one worth the time: the import does not fail, it
   silently carries less than the operator assumes.

Neither check proves semantic equivalence — a column that still exists but now
means something else will pass both.

### What it does not carry across

Deliberate, and documented in the tool: moderation state (video blacklist,
account/server blocklists, abuse reports) and user notification settings and
watch history.

Not deliberate, just unread — measured against a real instance, with that
instance's row counts as an illustration of the scale involved:

| Source table | Rows there | What is lost | Status |
|---|---:|---|---|
| `video.views` | 3,170,672 views | per-video view totals | **carried** (see below) |
| `videoChapter` | 20,500 | chapter markers | **carried** |
| `accountVideoRate` | 18,895 | likes / dislikes | **carried** |
| HLS `videoFile` rows | ~3 per video | the quality ladder's rungs | **carried** in reference mode |
| `actorImage` | 12,883 | account + channel avatars and banners | still unread |
| `storyboard` | 1,564 | regenerated by Vidra, so no real loss | n/a |
| `videoSource` | 623 | original-file provenance records | still unread |

The four now carried run as **passes of their own**, after the videos, keyed by
their own ledger rows — not as extra work inside the video import. That is what
lets a re-run **backfill them onto videos an earlier release already imported**.
An instance whose catalogue is already in Vidra gets them by re-running the
importer; nothing has to be re-imported from scratch.

View totals are the one counter here, and they are applied as a **delta**: the
ledger remembers the source total it last applied, and a run applies only the
difference. So a re-run against an unchanged source adds nothing, views Vidra
served between runs are not erased, and a source total of zero is read as "no
data" rather than "withdraw everything". The **per-day** rollup
(`video_view_days`) is deliberately left empty — the source has one lifetime
number and no daily history, and inventing buckets for it would fabricate a
shape of data that was never measured. See `docs/peertube-migration.md` §1.1.

Run `--dry-run` first: it reports the plan and the conflicts and writes nothing.
Its `entities` map now carries `view_count`, `chapter`, `rating` and `rendition`
alongside the older kinds. `view_count` counts **videos** whose total would be
carried, never views.

### Reference mode works

Verified end to end against a real instance: `--media-mode=reference` records the
source's object keys, Vidra resolves them against its configured store, and the
bytes stream correctly — master playlist, variant playlist and segments all
served, including `206 Partial Content` for Range requests, which is what a
player needs to seek.

One thing to know before choosing it:

- **The store has to BE the source bucket.** Reference mode records
  bucket-relative keys, so they only resolve if Vidra points at the bucket the
  objects are in. The imported instance is therefore *not* independent of the
  source. Give it a **read-only** credential so it structurally cannot modify
  the origin instance's media, and keep `MEDIA_GC_ENABLED=false` — GC's ownership
  marker will also refuse to sweep an unowned bucket on its own, so the two
  together are defence in depth. **Do not adopt the bucket**; adopting stamps
  ownership and re-enables destructive sweeps against media Vidra has no rows for.
Rendition rows used to be the gap here: the API reported `renditions: []` even
though the imported master carried variants (240p/360p/480p in the case
measured), so the quality menu rendered empty while playback and ABR worked fine
(hls.js reads levels from the manifest, not from the API). They are now carried
in reference mode — one row per rung of the referenced tree, read from the
source's HLS `videoFile` rows. Two notes on what those rows say:

- `key_prefix` points at the source tree's **directory**, which is where the rung's
  variant playlist and segments actually live. PeerTube keeps every rung of a
  ladder side by side in one directory rather than a directory per rung, so all
  of a video's imported rungs share it. There are no progressive per-rung
  download assets under it; the download endpoint already skips a rung whose
  asset is missing, so nothing promises a file that is not there.
- Rung **heights** come from the source. **Widths** come from the source when it
  records them (`videoFile.width`, or `video.aspectRatio` × height); failing
  both, they are derived 16:9, because `video_renditions` CHECKs `width > 0` and
  there is no "unknown" to store. The guess is confined to the width — the
  height, which is what a quality menu labels a rung with, is never guessed.

Note also that **copy mode is not a copy**: it regenerates HLS through Vidra's
transcoding pipeline. On a catalogue of any size that is the dominant cost — 2,039
hours of source on 4 vCPU is months of wall clock, not hours. If independence from
the source bucket is the goal, a server-side object-store copy of the referenced
keys is the cheaper route: it duplicates the media without re-encoding a frame.

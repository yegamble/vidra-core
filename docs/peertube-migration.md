# Migrating a PeerTube instance into Vidra

This guide covers the one-way import of an existing **PeerTube** instance — its
PostgreSQL database and media storage — into a Vidra instance (fix_plan P18,
design in `.ralph/specs/peertube-import.md`). The import is **read-only on the
source**, **idempotent**, **resumable**, **dry-runnable**, and **audited**.

It is distinct from live federation (following a remote PeerTube instance): this
tool *moves content in*, once.

---

## 1. What is imported vs. deferred

**Imported** (local content only — remote/federated actors are never migrated):

- Users/accounts (bcrypt password hashes carried, so passwords keep working) and
  their ActivityPub actor keypairs (for federation continuity).
- Channels.
- Videos + their metadata (title, description, privacy, category/licence/
  language, duration), the primary web video file, thumbnail, captions, and
  existing PeerTube HLS playlists when `--media-mode=reference` is used.
- Threaded comments (locally authored).
- Regular playlists + their items.
- Tags.
- Subscriptions (local user → local channel follows).
- **View totals** — each video's lifetime count (see §1.1 for what that does and
  does not mean).
- **Chapters** — seek-bar marks. `has_chapters` on the video detail is derived
  from the rows, so it flips on by itself.
- **Ratings** — likes/dislikes cast by local accounts on local videos.
- **HLS ladder rungs** in reference mode — one `video_renditions` row per rung of
  the referenced tree, so the quality selector has something to render.
- **The instance's category taxonomy**, when the source replaces the stock one
  (see §1.2). Without it the category ids the videos carry would mean nothing
  here.
- **Account and channel avatars and banners** (`actorImage`) — see §1.3, because
  these are the one family fetched over HTTP rather than copied from storage.

**Deferred / mode-dependent** (reconcile or regenerate afterwards):

- **HLS streaming playlists in copy mode** — reference mode reuses PeerTube's
  existing HLS objects; copy mode still relies on Vidra's own transcoding
  pipeline (`TRANSCODING_ENABLED`) after import. Ladder rungs follow the tree:
  in copy mode Vidra's own transcode writes them.
- **Per-day view history** — see §1.1.
- **Moderation state** (video blacklist, account/server blocklists, abuse reports).
- **User notification settings and watch history.**
- **Original-file provenance records** (`videoSource`).
- Live sessions, plugins, themes, runners, redundancy config, any payment data.
  (The categories plugin's *taxonomy* is read — see §1.2 — but no plugin is
  installed, enabled or otherwise carried.)
- **The instance's own name, description and terms.** They are not in the source
  database at all: `application.configPart` holds only object-storage config, and
  the identity lives in `config/local-production.json` on the source **host**,
  which `--source-dsn` cannot reach. Copy them into Vidra's admin settings by
  hand after the import.

### 1.1 What happens to view counts

PeerTube stores **one lifetime number per video** and no history behind it.
Vidra stores both a lifetime total (`video_view_counts`) and a per-UTC-day
rollup (`video_view_days`) that feeds the creator statistics chart.

The total is carried. **The day rollup is left empty** — not one backfilled
bucket, not a spread across the video's lifetime. Both of those would invent a
shape of data the source never had: a single bucket claims a whole catalogue's
views happened on one calendar day, and a spread claims a daily history nobody
measured. An absent day row already means zero (migration 0046 never backfilled
either), so an imported video's chart reads as *no daily data before the import,
real daily data after it* — which is the truth.

The write is a **delta, never an assignment**, which is what makes the scheduled
re-run safe:

- an unchanged source contributes nothing, so re-running does not double;
- views Vidra served between runs survive, because the source total is never
  assigned over them;
- a source that gained views contributes only the gain.

A source total of **zero is read as "no data", not "withdraw everything"** — a
source that stopped carrying the column would otherwise wipe the instance's view
history on one run.

### 1.2 What happens to the category taxonomy

Vidra ships PeerTube's stock 1–18 category ids on purpose: that is what lets an
imported video's `category` come across unchanged. But an instance running
**`peertube-plugin-categories`** has replaced that taxonomy — the plugin deletes
the stock entries and adds the instance's own at higher ids — and importing one
without its taxonomy leaves every video pointing at an id that validates against
nothing.

So the taxonomy is carried into the **`instance_custom_categories`** setting,
which replaces the built-in list when set. It is read from the source's `plugin`
row (settings key `json-categories-as-text`, whose value is a JSON string
containing JSON), and `add` and `delete` are both applied, so what Vidra offers
is what the source offers. An `add` on a surviving stock id is a rename and is
carried as one.

The import is run on a schedule until cutover, so two rules make the re-run safe:

- **No plugin, no override.** Absent, disabled, uninstalled, carrying no
  taxonomy, or describing exactly the built-in list: nothing is written and
  Vidra's built-in taxonomy stands. Most PeerTube instances are this case.
- **An operator edit is never overwritten.** The ledger records the exact value
  the import applied (migration 0113). A stored value that still equals it is the
  import's own and is updated when the source's taxonomy moves; anything else is
  a human's and is left alone, with a note in the report's `conflicts` so the
  divergence is visible. Clearing the setting is also a decision: the import does
  not refill it. And nothing is ever removed — a source that drops its plugin
  leaves the last carried taxonomy in place, because the imported videos still
  carry its ids.

One operational wrinkle: a running server caches the settings overlay. An import
run through the **admin API** reloads it as part of the run; an import run from
the **CLI** against a database a server is already serving writes the row, but
that server keeps serving the old taxonomy until it restarts (or an admin saves
any instance setting).
### 1.3 What happens to avatars and banners

Every other media family is read out of the source's object store. Actor images
are not there to read. PeerTube's S3/B2 configuration covers streaming
playlists, web videos, captions and originals — **never avatars**, which stay on
the source host's local filesystem however the rest of the instance is set up.
So `--source-storage=s3` cannot see them, and `--source-local-root` cannot
either unless the import happens to run on the source machine.

They *are* served publicly by the source instance, so that is where they are
fetched from: `GET <origin>/lazy-static/avatars/<filename>`.

**The origin is derived, not configured.** The importer reads the canonical URLs
the source's own local actors carry (`https://host/accounts/alice`) and takes
the majority origin. There is no `--source-origin` flag: an operator who has
already given this tool a database and a media root should not have to know that
one more family needs one more input, and every local actor on an instance
carries the same origin by construction. A source whose actors carry no absolute
URL reports the family as deferred and the rest of the import proceeds.

Consequences worth knowing before you run it:

- **Avatars are fetched even under `--media-mode=reference`.** Reference mode
  works for video because Vidra can point at object keys the source already has
  in a shared bucket; there is no such key for an avatar, so referencing is not
  a thing that can be done. The choice is between fetching them and an instance
  whose accounts have no faces. `--media-mode=none` *is* respected — it says
  "import no media", and this is media.
- **The source instance must still be reachable over HTTP** when the import
  runs. If it is not, each image is recorded as a failure and a later run picks
  them up; nothing else in the import is affected.
- **Only JPEG, PNG and WebP are stored**, because that is what Vidra's own
  avatar upload accepts. Anything else is recorded as `unsupported`.
- **What the bytes are decides how they are stored** — not the source filename,
  not the response's declared type. `/static/avatars/<name>` (as opposed to
  `/lazy-static/…`) answers `200` with the web app's HTML shell rather than a
  404, and a naive fetch would happily store 62 KB of HTML as somebody's face.
- **The largest variant wins.** PeerTube generates several resolutions from one
  avatar upload and keeps a row per size. They all name the same slot here, so
  the import takes the biggest one per account/channel per slot and ignores the
  rest. (Before it did, all of them were carried concurrently and raced for the
  same object key: on a real migration that left 137 of 229 user avatars as
  sub-5 KB thumbnails while 2.1 MB originals sat in the source.) A source that
  records no pixel sizes still gets one row per slot — the newest.
- **An avatar somebody uploaded here is never written over.** The import fills
  gaps and updates images *it* wrote; anything else it leaves exactly as it is
  and lists under `conflicts` in the report, so the divergence is visible rather
  than silent. The ledger remembers what each import write produced, which is
  how the two are told apart.
- **An image the import wrote does follow the source.** If an account changes
  its avatar on the source between runs — or the earlier run picked the wrong
  variant — the next run replaces what it put there. An unchanged source costs
  nothing: no fetch, no upload.
- **An oversize image is recorded `unsupported`, not `failed`.** How big a file
  the source holds is a fact about the source, so it is ruled out once instead
  of being re-downloaded and re-rejected on every run.
- The source is a live production instance during a migration, so the fetches
  are deliberately unhurried: four connections, a 20-second ceiling per image,
  an 8 MiB cap, and one host contacted for the whole run.

Re-runs are cheap here: a slot that already holds what the source offers is
settled from the database, without contacting the source at all.

See the full entity mapping table in `.ralph/specs/peertube-import.md`.

---

## 2. Prerequisites

1. **A read-only copy of the source PeerTube PostgreSQL database.** Either:
   - restore a `pg_dump` into a scratch database, or
   - point at a read-only replica.

   Create a **least-privilege read-only role** for the connection. The importer
   also pins every session to `default_transaction_read_only = on` as defence in
   depth, but you should still not hand it a writable superuser.

2. **A media strategy.**
   - `--media-mode=copy` needs the source instance's media storage mounted
     read-only, either as a local filesystem tree or an S3-compatible bucket.
   - `--media-mode=reference` does not copy or read source media during import.
     Instead, Vidra stores PeerTube's existing object keys and the running Vidra
     server must have `STORAGE_*` pointed at that same object store.

   The importer expects PeerTube's default object layout: `web-videos/`,
   `thumbnails/`, `captions/`, and `streaming-playlists/hls/`.

3. **A running Vidra instance** (this destination) with its `DATABASE_URL`,
   storage backend (`STORAGE_*`), and — if you want actor keys sealed at rest —
   `FEDERATION_KEY_KEK` configured, exactly as the server uses them.

4. **A supported PeerTube schema version.** Preflight reads
   `application.migrationVersion` and refuses versions outside the verified range
   (**700–1000**, ≈ PeerTube 5.x–8.x). See `.ralph/specs/peertube-reference.md`.
   The refusal is a hard stop that only a **human who has verified
   compatibility** may lift — automated agents must never lift it. Two ways to,
   and no third:

   - On the CLI, `--force`. It is blanket: it also covers a source whose version
     cannot be read at all.
   - Through the admin UI or API, by acknowledging the **exact version** the
     refused run reported (§5). Narrower on purpose — it opens the gate for that
     one number, expires by itself if the source moves, and is recorded.

5. **Destination storage credentials that can WRITE.** Preflight proves it: it
   stores a tiny object under `.vidra/write-probe/` and deletes it again, before
   the run touches anything. A read-only key passes every other check there is —
   the bucket answers a `HeadBucket`, the ownership marker reads back — and then
   fails **every** upload. One real migration ran three minutes and failed 1,321
   avatar uploads with `s3: put "avatars/users/…": not entitled`, which is
   Backblaze B2 for *this key does not have the `writeFiles` capability*. On
   AWS/MinIO/Spaces the action is `s3:PutObject`. `--media-mode=none` skips the
   probe; `--media-mode=reference` does **not**, because actor images are stored
   in the destination whatever the media mode says.

   Delete access (`deleteFiles` / `s3:DeleteObject`) is not required for an
   import, and preflight will not stop for its absence — but without it media
   garbage collection frees nothing later. `vidra doctor --write-probe` reports
   the same two facts about a running deployment.

> A source DSN that will not connect is diagnosed rather than passed through:
> `dial unix /tmp/.s.PGSQL.15432` means the connection string carried no usable
> host and the driver fell back to a **local** socket, and a plain
> `connection refused` from another machine is usually a PeerTube PostgreSQL
> still bound to `127.0.0.1` — for which an SSH tunnel
> (`ssh -L 15432:127.0.0.1:5432 <source host>`) is the answer that changes
> nothing on the source.

> Password hashes: PeerTube and Vidra both use bcrypt, and a bcrypt hash encodes
> its own cost, so carried hashes verify directly — users keep their passwords.
> If you ever import from a system with an incompatible scheme, import with
> credentials disabled and require a password reset instead.

---

## 3. Dry run first (writes nothing)

Always dry-run first. It reports the counts, the mapping plan, conflicts, and the
deferred families, and writes **nothing**: no ledger rows, no entities, no media.
(Preflight still runs its write probe first, and a rehearsal is exactly where you
want to find out the destination credentials are read-only — the probe object is
removed again, so the dry run still leaves no trace.)

```bash
DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
peertube-import \
  --source-dsn 'postgres://readonly:pw@oldhost:5432/peertube_prod?sslmode=disable' \
  --source-storage local --source-local-root /mnt/peertube-media \
  --conflict-policy skip \
  --dry-run
```

The JSON report shows `entities` (per-kind `planned` counts), `conflicts`
(collisions the policy would resolve), and `deferred`.

### Conflict policy

When a source username / channel handle / email collides with an existing Vidra
row, `--conflict-policy` decides:

- `skip` (default, safest, non-destructive) — leave the existing Vidra row, map
  the source entity to it.
- `rename` — import under a de-duplicated identifier (`alice` → `alice-2`).
- `merge` — attach the source entity's children to the existing Vidra row.
- `fail` — abort the whole import on the first collision.

---

## 4. Run it

Drop `--dry-run` to perform the import:

```bash
DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
peertube-import \
  --source-dsn 'postgres://readonly:pw@oldhost:5432/peertube_prod?sslmode=disable' \
  --source-storage local --source-local-root /mnt/peertube-media \
  --conflict-policy skip
```

For S3-backed source media in copy mode:

```bash
peertube-import \
  --source-dsn '...' \
  --source-storage s3 \
  --source-s3-endpoint s3.example.com --source-s3-bucket peertube \
  --source-s3-region us-east-1
# credentials via env so they are not in the process args:
#   PEERTUBE_SOURCE_S3_ACCESS_KEY=... PEERTUBE_SOURCE_S3_SECRET_KEY=...
```

### Reusing an existing Backblaze/S3 bucket without copying video

Use `--media-mode=reference` when the new Vidra server should serve objects
directly from the current PeerTube bucket. This is the fastest path for a test
replacement server: the import writes DB rows only, leaving video, thumbnail,
caption, and HLS bytes in place.

Configure the Vidra runtime storage (`STORAGE_*`) to the current bucket first:

```bash
export STORAGE_BACKEND=s3
export STORAGE_S3_ENDPOINT=s3.us-east-005.backblazeb2.com
export STORAGE_S3_BUCKET=exampletube
export STORAGE_S3_REGION=us-east-005
export STORAGE_S3_USE_SSL=true
export STORAGE_S3_FORCE_PATH_STYLE=true
export STORAGE_S3_ACCESS_KEY=...
export STORAGE_S3_SECRET_KEY=...
```

Then run the import without any `--source-storage` flags:

```bash
DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
peertube-import \
  --source-dsn 'postgres://readonly:pw@localhost:5432/peertube_source?sslmode=disable' \
  --media-mode reference \
  --conflict-policy skip \
  --dry-run
```

Drop `--dry-run` to commit. Referenced media rows keep PeerTube keys like
`web-videos/<file>.mp4`, `thumbnails/<file>.jpg`, `captions/<file>.vtt`, and
`streaming-playlists/hls/<source-video-uuid>/<playlist>.m3u8`.

`--media-mode none` (or the older `--no-media`) imports metadata only and writes
no media rows.

### Resuming

The import records every mapped row in a durable **ledger** in the Vidra database
(`peertube_import_ledger`). If it is interrupted, **just run the same command
again**: already-imported rows are skipped and it continues where it left off.
Re-running a completed import is a safe no-op. `--resume` is accepted for clarity
but changes nothing — idempotency is always on.

---

## 5. Via the admin API (optional)

An admin can also launch/monitor an import from the server (e.g. the vidra-user
"Import from PeerTube" admin UI) instead of the CLI. This requires the source to
be configured in **server config** — the browser never sends a DSN or credential:

```bash
# server .env
PEERTUBE_IMPORT_ENABLED=true
PEERTUBE_SOURCE_DATABASE_URL=postgres://readonly:pw@oldhost:5432/peertube?sslmode=disable
PEERTUBE_SOURCE_STORAGE_BACKEND=local
PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=/mnt/peertube-media
PEERTUBE_IMPORT_CONFLICT_POLICY=skip
PEERTUBE_IMPORT_MEDIA_MODE=copy
```

For Backblaze/S3 reference mode, set `PEERTUBE_IMPORT_MEDIA_MODE=reference` and
point the normal Vidra `STORAGE_*` settings at the existing PeerTube bucket. The
admin worker does not need `PEERTUBE_SOURCE_STORAGE_*` in reference mode.

Then (admin bearer token):

- `POST /api/v1/admin/peertube-import` `{"mode":"dry_run"}` or `{"mode":"run"}` →
  `202` with the run; only one run may be active at a time (`409` otherwise);
  `503` if not configured.
- `GET /api/v1/admin/peertube-import` → recent runs + live progress.
- `GET /api/v1/admin/peertube-import/{id}` → poll one run's progress report.

An in-process worker executes the run; start/finish are emitted as audit events
(no secrets). The admin path never self-passes `--force`.

### Unverified source schema (the 8.x case)

A source outside the verified range fails the run rather than importing from a
schema nobody has checked. The failed run carries the machine-readable reason and
the number:

```json
{ "state": "failed", "error_code": "unverified_schema", "source_version": 1040 }
```

A **dry run** reaches the version check before it touches anything, so the cheap
way to find out is to preview first — it writes nothing either way.

An administrator who accepts the risk re-launches naming that exact version:

```json
{ "mode": "run", "conflict_policy": "skip", "acknowledged_schema_version": 1040 }
```

- It is per-run. It is not remembered, not a setting, not a default, and every
  launch has to state it again.
- It must **equal** the version preflight detects, so it cannot be sent by
  something that never read the refusal, and it stops applying if the source is
  upgraded between the dry run and the import.
- It widens the schema-version gate and nothing else.
- It is recorded on the run (`acknowledged_schema_version`, beside `started_by`)
  and in the audit log, which names the version accepted.
- What is being accepted is real: an unverified schema may have renamed or
  removed columns the importer reads, so the run can fail partway or carry
  incorrect data. Dry-run, read the report, and check the counts afterwards (§6).

`error_code: undetectable_schema` — no version could be read from the source at
all — cannot be acknowledged this way. There is no version to name; that one
needs the CLI and a human who has looked at the source.

The admin page at `/admin/import-peertube` does all of this for you: after a
refused run it shows the detected version and a confirmation naming it, which has
to be ticked again before every launch.

---

## 6. Post-import verification

1. **Counts.** Compare the run's `report.entities[*].imported` against your
   source (`SELECT count(*) FROM "user"`, `video`, …). `skipped`/`failed` explain
   any difference; `conflicts` lists resolved collisions.
2. **Sign-in.** A migrated user can log in with their existing password.
3. **Playback.** Open an imported public video. In reference mode, both the
   original and PeerTube's existing HLS playlist should play through Vidra's
   authenticated proxy. In copy mode, the original plays immediately; enable
   `TRANSCODING_ENABLED` if you want Vidra to generate a fresh adaptive ladder.
4. **Channels/playlists/comments/follows** appear as expected.
5. **Categories.** If the source ran a custom taxonomy, an imported video's
   category should read as its own name, not as blank or as a stock label. The
   effective list is `GET /api/v1/videos/config`; the stored override is
   `instance_custom_categories` in the admin instance settings. A blank category
   on a video whose source category was *deleted there too* is correct — that is
   what the source showed.
6. **Federation continuity** (if enabled): imported actors keep their keypairs,
   so remote followers continue to resolve them. If you changed domains, plan an
   ActivityPub `Move`/`alsoKnownAs` redirect (see `.ralph/specs/federation.md`).
7. **Audit trail.** `GET /api/v1/admin/audit-log` (or the logs) show the
   `admin.peertube_import.start`/`finish` events — no secrets.

---

## 7. Security notes

- **Read-only source.** The importer never writes to the PeerTube database or
  storage; sessions are pinned read-only.
- **Secrets.** The source DSN and S3 keys are secrets — pass them via env or a
  restricted shell, never commit them, and they are never logged. Password
  hashes and actor private keys are never logged either.
- **Path traversal.** Media reads go through the same key-validated storage
  backend as the rest of Vidra; a source filename cannot escape its bucket.
- **File type/size.** Copied files are extension-allowlisted and size-capped.
- **No browser-supplied credentials.** The admin API triggers imports using the
  server-configured source only.

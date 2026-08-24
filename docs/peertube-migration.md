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

**Deferred / mode-dependent** (reconcile or regenerate afterwards):

- **HLS streaming playlists in copy mode** — reference mode reuses PeerTube's
  existing HLS objects; copy mode still relies on Vidra's own transcoding
  pipeline (`TRANSCODING_ENABLED`) after import. Ladder rungs follow the tree:
  in copy mode Vidra's own transcode writes them.
- **Per-day view history** — see §1.1.
- **Moderation state** (video blacklist, account/server blocklists, abuse reports).
- **User notification settings and watch history.**
- **Account and channel avatars/banners** (`actorImage`) and original-file
  provenance (`videoSource`).
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
   `--force` overrides the refusal, but is for a **human who has verified
   compatibility** — automated agents must never pass it.

> Password hashes: PeerTube and Vidra both use bcrypt, and a bcrypt hash encodes
> its own cost, so carried hashes verify directly — users keep their passwords.
> If you ever import from a system with an incompatible scheme, import with
> credentials disabled and require a password reset instead.

---

## 3. Dry run first (writes nothing)

Always dry-run first. It reports the counts, the mapping plan, conflicts, and the
deferred families, and writes **nothing**.

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
(no secrets). The admin path never self-passes `--force`: an unverified version
fails the run with a clear message for a human to act on.

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

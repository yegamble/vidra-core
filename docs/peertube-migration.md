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

**Deferred / mode-dependent** (reconcile or regenerate afterwards):

- **HLS streaming playlists in copy mode** — reference mode reuses PeerTube's
  existing HLS objects; copy mode still relies on Vidra's own transcoding
  pipeline (`TRANSCODING_ENABLED`) after import.
- **Moderation state** (video blacklist, account/server blocklists, abuse reports).
- **User notification settings and watch history.**
- Live sessions, plugins, themes, runners, redundancy config, any payment data.

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
export STORAGE_S3_BUCKET=sizetube
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
5. **Federation continuity** (if enabled): imported actors keep their keypairs,
   so remote followers continue to resolve them. If you changed domains, plan an
   ActivityPub `Move`/`alsoKnownAs` redirect (see `.ralph/specs/federation.md`).
6. **Audit trail.** `GET /api/v1/admin/audit-log` (or the logs) show the
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

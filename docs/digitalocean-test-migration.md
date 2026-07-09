# DigitalOcean test replacement for example.com

This runbook clones the PeerTube `example.com` database into Vidra while
serving existing media directly from the current Backblaze B2 S3 bucket. It does
not migrate video bytes.

## Shape

- `vidra-core` runs on the DigitalOcean server.
- `vidra-user` is built/deployed against that API.
- PostgreSQL stores the new Vidra database plus a restored read-only PeerTube
  source database.
- `STORAGE_BACKEND=s3` points at the existing Backblaze bucket.
- `peertube-import --media-mode reference` writes Vidra DB rows that reference
  existing object keys such as `web-videos/...` and
  `streaming-playlists/hls/...`.

## DigitalOcean baseline

Use a normal CPU Droplet for the test server because video bytes stay on
Backblaze. Size it for Postgres plus the Go API and Next.js UI, not for local
video storage. Use a larger CPU plan only if you turn on Vidra transcoding.

Minimum network setup:

- SSH from your admin IP.
- HTTP/HTTPS from the internet.
- No public Postgres port.
- DNS first on a test name such as `test.example.com`; only move
  `example.com` after playback and login checks pass.

Lower the DNS TTL before cutover, for example to 300 seconds, so rollback is
fast.

## Restore the source PeerTube dump

The supplied dump is gzip-wrapped PostgreSQL custom format from PostgreSQL 18,
so use `pg_restore` 18 or newer.

```bash
createdb peertube_source
gunzip -c ~/peertube_db_20260609_000002.sql.gz > /tmp/peertube_source.dump
pg_restore --no-owner --no-privileges --dbname peertube_source /tmp/peertube_source.dump
```

Create a read-only role for the importer:

```sql
CREATE ROLE peertube_readonly LOGIN PASSWORD 'replace-me';
GRANT CONNECT ON DATABASE peertube_source TO peertube_readonly;
GRANT USAGE ON SCHEMA public TO peertube_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO peertube_readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO peertube_readonly;
```

## Vidra env

Use the existing Backblaze object store as Vidra's authoritative storage for
this test. The current PeerTube config uses these relevant object prefixes:

- `web-videos/`
- `thumbnails/`
- `captions/`
- `streaming-playlists/` (with HLS under `streaming-playlists/hls/`)

Example core env:

```bash
DATABASE_URL=postgres://vidra:replace-me@127.0.0.1:5432/vidra?sslmode=disable

STORAGE_BACKEND=s3
STORAGE_S3_ENDPOINT=s3.us-east-005.backblazeb2.com
STORAGE_S3_BUCKET=exampletube
STORAGE_S3_REGION=us-east-005
STORAGE_S3_USE_SSL=true
STORAGE_S3_FORCE_PATH_STYLE=true
STORAGE_S3_ACCESS_KEY=replace-me
STORAGE_S3_SECRET_KEY=replace-me

PEERTUBE_IMPORT_ENABLED=true
PEERTUBE_SOURCE_DATABASE_URL=postgres://peertube_readonly:replace-me@127.0.0.1:5432/peertube_source?sslmode=disable
PEERTUBE_IMPORT_CONFLICT_POLICY=skip
PEERTUBE_IMPORT_MEDIA_MODE=reference

TRANSCODING_ENABLED=false
```

If this is a read-only clone, use a Backblaze key that can read the bucket and
leave upload/transcoding features off. If you want new Vidra uploads to write
into the same bucket, use a write-capable key and understand that new objects
will be added beside the existing PeerTube objects.

## Import

Run migrations for the Vidra destination database first, then dry-run:

```bash
make migrate-up

peertube-import \
  --source-dsn 'postgres://peertube_readonly:replace-me@127.0.0.1:5432/peertube_source?sslmode=disable' \
  --media-mode reference \
  --conflict-policy skip \
  --dry-run
```

Review the JSON report. For the exampletube dump, expect a PeerTube schema version
around `1000`, thousands of users/channels, about 14k videos, and
`hls_playlist.planned` for videos with existing HLS.

Run it for real:

```bash
peertube-import \
  --source-dsn 'postgres://peertube_readonly:replace-me@127.0.0.1:5432/peertube_source?sslmode=disable' \
  --media-mode reference \
  --conflict-policy skip
```

## Verify before DNS

Check the DB points at existing PeerTube keys:

```sql
SELECT storage_key FROM video_files WHERE kind = 'original' LIMIT 5;
SELECT master_key FROM streaming_playlists WHERE state = 'ready' LIMIT 5;
```

Expected shapes:

- `web-videos/<file>.mp4`
- `streaming-playlists/hls/<source-video-uuid>/<master>.m3u8`

Then verify through the app:

- Login with a migrated user.
- Open several public videos.
- Open at least one video that had HLS in PeerTube and confirm adaptive playback.
- Confirm thumbnails and captions load.
- Confirm private videos are still hidden from anonymous users.

For a direct HLS smoke test, use a Vidra video id:

```bash
curl -i https://test.example.com/api/v1/videos/<vidra-video-id>/hls/master.m3u8
```

The served master playlist should contain `peertube/<playlist-file>.m3u8`
entries; Vidra rewrites those so the flat PeerTube HLS object tree works through
Vidra's authenticated proxy.

## DNS cutover

1. Start with `test.example.com` pointed at the Droplet.
2. Verify API health, UI load, login, thumbnails, originals, and HLS playback.
3. Lower the `example.com` A/AAAA record TTL.
4. Point `example.com` at the Droplet IP.
5. Keep the old PeerTube server available for rollback until caches expire and
   the Vidra test is accepted.

Do not run the old PeerTube server and Vidra as concurrent writers to the same
bucket unless that is intentional. Reference mode is safest when PeerTube is
frozen or the test is treated as a snapshot from the restored dump.

# PeerTube import / migration spec — vidra-core

> Authoritative design for importing an **existing PeerTube instance** — its
> PostgreSQL database and media storage — into a Vidra instance. This is a
> one-way migration tool, distinct from live federation (P10) and from the
> clean-room parity work in the `peertube-*` ledgers. Ralph builds this under
> fix_plan **P18 — PeerTube Import and Migration**. Treat PeerTube as a data
> source to read, never code to copy.

## Goal

Let an operator move a running (or backed-up) PeerTube instance into Vidra with
its content and identity intact: accounts, channels, videos and their files /
streaming playlists, thumbnails, captions, playlists, comments, tags/categories,
subscriptions/follows, and — where possible — ActivityPub actor identity so
remote followers keep working. The migration must be safe, idempotent,
resumable, and auditable.

## Import modes

1. **Database + storage import (primary).** Read from a PeerTube PostgreSQL
   source (a dump restored to a scratch DB, or a read-only replica) and from its
   media storage (local filesystem or S3-compatible). Map PeerTube's schema to
   Vidra's and copy/re-probe media. This is the complete, offline migration path
   and the default this phase targets.
2. **API / ActivityPub import (secondary, later).** For instances where only
   public access is available, pull public data via PeerTube's REST API /
   ActivityPub. Cannot recover private data, password hashes, or non-public
   content; document it as partial.

## Hard principles

- **Read-only on the source.** Never write to, migrate, or "fix" the PeerTube
  database or storage. Connect with a least-privilege read-only role.
- **Pin supported versions.** Detect the PeerTube schema/version on preflight and
  refuse on unverified versions. Record the supported version range in this spec
  and the `peertube-reference.md` ledger. A `--force` override may exist for a
  human operator, but **Ralph must never self-pass `--force`** — an unverified
  version is a `BLOCKED` safety rail requiring user sign-off, not an autonomous decision.
- **Dry-run first.** A `--dry-run` mode reports counts, the mapping plan,
  conflicts, and unsupported/partial entities, and writes nothing.
- **Idempotent + resumable.** Maintain a durable **import ledger** mapping each
  source row's stable identifier (PeerTube UUID / id) → Vidra id, with status.
  Re-running skips already-imported rows; a crashed import resumes cleanly.
- **Explicit conflict policy.** Username/handle/email/channel-slug collisions are
  resolved by a configured policy (`skip` | `rename` | `merge` | `fail`),
  defaulting to the safest non-destructive option.
- **Admin-only + audited.** The import is an admin operation; emit audit events
  (start/finish/per-entity summary, never secrets) per
  `.ralph/specs/observability.md`. No password hashes, tokens, or keys in logs.
- **Security.** Apply SSRF and path-traversal protections when reading remote/S3
  storage and when following media URLs; validate file types; bound sizes.

## Entity mapping ledger (must be filled in before/with implementation)

Maintain a table: **PeerTube entity → Vidra model/table → status
(`supported` | `partial` | `unsupported` | `deferred`) → notes**. At minimum:

- `user` / `account` / `actor` → Vidra users + accounts + actors (identity).
- `videoChannel` → Vidra channels.
- `video`, `videoFile`, `videoStreamingPlaylist` (HLS) → Vidra video + renditions.
- `thumbnail`, `videoCaption` → Vidra thumbnails + captions.
- `videoComment` (threaded) → Vidra comments.
- `videoPlaylist`, `videoPlaylistElement` → Vidra playlists.
- `tag`, `videoCategory`/`videoLicence`/`videoLanguage` → Vidra tags/metadata.
- `actorFollow` / `accountFollow` → Vidra subscriptions/follows.
- `userNotificationSetting`, `userVideoHistory` → Vidra prefs/history (or defer).
- Moderation: `videoBlacklist`, `accountBlocklist`/`serverBlocklist`, `abuse` →
  Vidra moderation (where in scope; otherwise `deferred`).
- Out of scope / deferred: plugins, themes, runners/jobs, redundancy/mirroring
  config, live sessions in progress, any premium/payment data.

## Identity & credentials

- **Passwords**: PeerTube stores bcrypt hashes. If Vidra uses a compatible bcrypt
  scheme, import the hashes so users keep their passwords; otherwise import
  accounts with credentials disabled and require a password-reset/verification
  flow. Never log or export hashes.
- **Federation identity**: import ActivityPub actor handles and keypairs so the
  migrated instance can keep serving the same actors (or, if changing domains,
  document the ActivityPub `Move`/`Alsoknownas` redirect path). Ties into P10.

## Media migration

- Support source storage = local filesystem and S3-compatible object stores.
- Copy with streaming + checksums (resumable); re-probe with FFmpeg; preserve
  existing HLS/renditions/thumbnails/captions where valid, regenerate when not.
- Map storage paths/URLs into Vidra's storage layout and update DB references.

## Surface & operability

- Primary surface: a CLI command (e.g. `cmd/peertube-import`) taking source DSN,
  source storage config, conflict policy, and `--dry-run` / `--resume`.
- Optional admin API endpoint to launch/monitor an import job — if added, it MUST
  be documented in `api/openapi.yaml` (the route↔spec drift guard enforces this)
  and is the backend contract the `vidra-user` admin "Import from PeerTube" UI
  consumes.
- Config keys (add to `internal/config` + `.env.example`, all optional/off by
  default): source DB DSN (read-only), source storage settings, conflict policy.
  Source credentials are secrets — never commit them, never log them.

## Testing

- Seed a scratch PostgreSQL with a known-version PeerTube schema + fixture rows;
  run the importer; assert the mapping ledger, idempotency (re-run is a no-op),
  dry-run correctness, conflict handling, and that no secret is logged.
- Tiny media fixtures only; never real user data.

## Deliverable docs

- An operator **migration guide** (README or `docs/`) covering prerequisites,
  read-only source setup, dry-run, running/resuming, conflict policy, what is
  imported vs. deferred, and post-import verification.

See `peertube-reference.md` (pinned version), `peertube-feature-ledger.md`
(feature parity), `security.md`, and `observability.md` (audit + no-secrets).

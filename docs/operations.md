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
install. Four rails stand between that inference and an irreversible delete.

| Rail | What it does |
|---|---|
| `MEDIA_GC_ENABLED` (default `true`) | `false` means the daily worker is never started. The admin endpoint stays available — the flag governs the unattended delete, not an operator asking for a sweep. Boot-baked: it is not in the admin settings overlay, deliberately |
| Dry-run first | The **first sweep of every process lifetime is a dry run**, and it runs ~5 minutes after boot rather than 24 hours later. A misconfiguration that would delete a library shows up in the log and the audit trail during the deploy that introduced it. Deletion starts from the sweep after that |
| `MEDIA_GC_MAX_ORPHAN_PERCENT` (default `25`) | A destructive sweep that finds more than this share of what it scanned to be orphans deletes **nothing** and says so (`breaker_tripped`). Sweeps of 100 orphans or fewer are exempt whatever the ratio — a nearly-empty store is 100% orphans and that is not news. Every wrong reference set looks the same: a half-restored database, a bucket that is not ours, a migration in flight |
| Bucket-ownership marker | See below. S3 only |

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
  URLs) plus a `vidra_queue_depth{queue,state}` gauge. The endpoint is
  unauthenticated and lives at the root like the health probes — **network-scope
  it** (internal Prometheus scrape only), do not expose it publicly. It is
  intentionally omitted from the public OpenAPI contract (an ops surface).
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
- **The soak ran without an object store or a real search service.** Transcode
  throughput across instances has not been measured under load.

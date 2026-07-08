# Spec: Hybrid IPFS Media Mirroring (`vidra-core`)

> Drop-in target: `vidra-core/.ralph/specs/ipfs-media.md`.
> Feature IDs: **P2P-01…04, STOR-04/05** (`.ralph/specs/backport/FEATURE_VISION.md`).
> Conventions: Go / Echo / sqlc / PostgreSQL. Contract-first. `make ci` stays green.
>
> Supersedes the "IPFS as a third `STORAGE_BACKEND`" framing in
> `product-decisions.md §5` / `fix_plan.md:332`. That framing is wrong for the ask
> (see §2). This spec keeps `STORAGE_BACKEND=ipfs` **rejected** and adds IPFS as an
> orthogonal **mirror**.

---

## 1. Goal & non-goals

**Goal.** When `IPFS_ENABLED=true` and a healthy IPFS node is reachable, every
**eligible** media object the platform stores is added to IPFS and pinned, its
CID is persisted, and the CID / gateway URL is exposed in API responses so clients
(and the IPFS badge) are truthful. IPFS is a **complete mirror of eligible media**,
not just video files. Local/S3 remains the **authoritative** store; IPFS never
becomes a single point of failure for uploads or playback.

**Non-goals (v1).**
- IPFS is **not** an authoritative backend. `STORAGE_BACKEND` stays `local|s3`;
  `ipfs` stays rejected (`config.go:613`). Authority ≠ distribution.
- No WebTorrent (P2P-02 is out; the intentional-difference note in
  `fix_plan.md:363` stands).
- No `ipfs://` client scheme rewriting beyond exposing an HTTPS gateway URL
  (P2P-04 handled by returning a resolved gateway URL, not a raw scheme).
- No mirroring of **private/unlisted/DM/export/quarantined/live-edge/remote-cache**
  media in v1 (see §7 Privacy — this is the gating decision).

---

## 2. Architecture: mirror sidecar, not a backend

```
          upload / re-transcode / avatar change / delete
                              │
                 authoritative write (unchanged)
                 internal/storage.Backend (local | s3)   ◀── serving reads here (unchanged)
                              │
                     enqueue mirror intent
                              ▼
        media_ipfs_pins (durable queue + CID ledger)      ◀── admin counts read here
                              │
        ipfsmirror worker (claims due rows, add+pin/unpin)
                              ▼
             internal/ipfs.Client  ── Kubo RPC /api/v0/*  ── kubo node
                              │
             (optional) IPFS Cluster /pins/<cid>  for replication
                              │
          API responses add cid + gateway_url  (additive, never replaces auth serving)
```

**Why a sidecar, not a `storage.Backend`:**
1. The ask is *"IPFS should contain ALL media"* — a mirror of the authoritative
   store, present **in addition** to local/S3. A replacement backend makes IPFS
   the only copy and makes an IPFS outage an upload/playback outage.
2. Every media class already has an opaque `storage_key`. The mirror observes the
   authoritative write and pins the same bytes — **zero change** to how any media
   class is written or served today.
3. Graceful degradation is structural: the mirror is an async queue; if the node
   is down the row stays `pending` and the reconciliation worker backfills. The
   write path never blocks on IPFS.

**Package layout (new):**
- `internal/ipfs/` — the Kubo RPC client (`client.go`), CID validation
  (`cid_validation.go`), optional cluster auth (`cluster_auth.go`). Ports the
  archive's contracts (see AUDIT §5) but as a mirror, not a `Backend`.
- `internal/ipfsmirror/` — the mirror service: intent enqueue helpers, the worker,
  the reconciliation/backfill scan, admin stats. Mirrors the shape of
  `internal/mediagc` and the `transcode_jobs` worker so it's idiomatic.

---

## 3. Per-media-class eligibility matrix

Eligibility = (is this class safe for a world-readable network?) AND (is this
specific object public?). "Mirror?" below is the **v1 default** under the
recommended privacy model (§7a). "Gate" is the runtime predicate the enqueue
helper evaluates.

| # | Media class | v1 Mirror? | Gate (all must hold) |
|---|---|---|---|
| 1 | Video original | ✅ | `videos.privacy='public'` AND `state='published'` |
| 2 | HLS tree (master+renditions+segments) | ✅ (directory add) | same as #1 |
| 3 | VP9/WebM alt | ✅ | same as #1 |
| 4 | Thumbnail | ✅ | parent video public+published |
| 5 | Storyboard sprite | ✅ | parent video public+published |
| 6 | Storyboard VTT | ✅ | parent video public+published |
| 7 | Captions | ✅ | parent video public+published |
| 8 | User avatar / banner | ✅ | owner `users.unlisted=false` AND account active |
| 9 | Channel avatar / banner | ✅ | owner account not unlisted |
| 10 | Playlist cover | ✅ | `playlists.visibility='public'` |
| 11 | **DM attachment** | ❌ | **never in v1** — E2EE / private (see §7) |
| 12 | Account export | ❌ | never — private, transient, PII |
| 13 | Upload chunks (in flight) | ❌ | never — transient, assembled then dropped |
| 14 | Live HLS edge | ❌ | never — mutable stream; only the finalized replay (→ #1-6) |
| 15 | Live replay VOD | ✅ (as #1-6) | after finalization, once privacy=public |
| 16 | Remote video thumbnail cache | ❌ | never — not our content to redistribute |
| — | Private/unlisted/quarantined variants of #1-7 | ❌ | never in v1 (fails the public gate) |

**Re-evaluation triggers** (a class can flip eligible↔ineligible):
- `videos.privacy` change (public→private ⇒ enqueue **unpin**; private→public ⇒
  enqueue **pin** of every derivative).
- `videos.state` → `published` (enqueue pin), → `failed`/deleted (unpin).
- `users.unlisted` toggle (unpin/pin avatars+banners+that owner's public videos).
- account deactivation / deletion (unpin all).

The enqueue helper is called from the **same transactions** that already mutate
these fields (publish transition, privacy update, avatar upload, delete), so
eligibility is always derived from committed state.

---

## 4. CID storage schema — one polymorphic table (RECOMMENDED)

**Recommendation: a single polymorphic `media_ipfs_pins` table keyed by the
storage object key**, not per-table `*_ipfs_cid` columns.

**Why polymorphic wins here:**
- Every media class already resolves to an opaque `storage_key` (or a
  deterministically derived key). The key is the natural universal join.
- New media classes (W1 storyboard extensions, Messaging v2 attachments, future
  live-replay) need **zero migrations** — they just start enqueuing intents.
- The reconciliation worker, admin counts, and GC scan have **one** place to read.
- Per-table columns (the archive's approach — `user_avatars.webp_ipfs_cid`) scatter
  the ledger, force a migration per class, and give no single pin-state view. The
  archive never achieved full coverage partly because of this.

Trade-off accepted: a mirror CID lookup is a join on `object_key` rather than a
column on the owning row. Cheap (indexed PK) and only needed on serving surfaces
that opt into exposing the CID.

### Migration sketch — `migrations/00NN_media_ipfs_pins.up.sql`
```sql
-- IPFS mirror ledger + durable pin queue. One row per storage object the mirror
-- has (or intends to) pin. object_key is the authoritative storage.Backend key
-- (opaque, forward-slash) — the universal handle across every media class, so no
-- new column is needed when a class is added. cid is CIDv1; car_root is the wrap
-- directory CID for multi-file adds (HLS trees). state drives the worker.
CREATE TABLE media_ipfs_pins (
    object_key   TEXT        PRIMARY KEY,          -- storage.Backend key (e.g. web-videos/<id>.mp4, streaming-playlists/<id>/)
    media_class  TEXT        NOT NULL              -- 'video_original','hls','thumbnail','storyboard','storyboard_vtt','caption','webm','user_avatar','user_banner','channel_avatar','channel_banner','playlist_cover'
                     CHECK (media_class <> ''),
    cid          TEXT        NOT NULL DEFAULT '',   -- CIDv1; '' until pinned
    car_root     TEXT        NOT NULL DEFAULT '',   -- directory/wrap root CID for multi-file (HLS); '' for single-file
    byte_size    BIGINT      NOT NULL DEFAULT 0,
    state        TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending','pinned','failed','unpinning','unpinned')),
    attempts     INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error   TEXT        NOT NULL DEFAULT '',   -- SAFE, client-invisible; never the raw node error verbatim
    -- Provenance for admin views + privacy re-evaluation. Nullable; a class that
    -- has no owning video leaves video_id NULL, etc.
    video_id     UUID        REFERENCES videos (id)   ON DELETE SET NULL,
    owner_user_id UUID       REFERENCES users (id)    ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Worker claim scan: due, still-actionable rows (mirrors transcode_jobs_due_idx).
CREATE INDEX media_ipfs_pins_due_idx
    ON media_ipfs_pins (next_attempt_at)
    WHERE state IN ('pending','unpinning');

-- Admin counts per class/state, and the reconciliation backfill scan.
CREATE INDEX media_ipfs_pins_state_class_idx ON media_ipfs_pins (state, media_class);

-- Privacy re-evaluation & cascade cleanup by video.
CREATE INDEX media_ipfs_pins_video_idx ON media_ipfs_pins (video_id) WHERE video_id IS NOT NULL;
```
`.down.sql`: `DROP TABLE media_ipfs_pins;`

**Optional read-optimization (defer unless a hot path needs it):** a nullable
`ipfs_cid TEXT` denormalized onto `streaming_playlists` / `video_files` for the
watch-page hot path, populated by the worker. Start without it — the join is fine.

sqlc: add `internal/store/queries/media_ipfs_pins.sql` (Upsert intent, ClaimDue,
MarkPinned, MarkFailed, MarkUnpinned, CountByStateClass, GetByObjectKey,
ListByVideo). Run `make sqlc`; `make sqlc-verify` must pass.

---

## 5. Pin lifecycle

All state transitions go through the ledger; the worker does the network I/O.

### 5.1 Create / publish (add + pin)
1. Authoritative write completes (existing code, unchanged).
2. In the **same transaction**, if `IPFS_ENABLED` and the eligibility gate (§3)
   holds, `Upsert` a `media_ipfs_pins` row `state='pending'`. This is a cheap DB
   write — **it never calls the node** and never blocks the request.
3. The `ipfsmirror` worker claims due `pending` rows (`FOR UPDATE SKIP LOCKED`,
   like `transcode_jobs`), streams the bytes from the authoritative
   `storage.Backend`, calls `client.Add(pin=true, cid-version=1, raw-leaves=true)`,
   writes `cid` + `state='pinned'`. On error: `attempts++`, exponential backoff
   into `next_attempt_at`, `state='failed'` after max attempts (dead-letter,
   visible to admin, retried by reconciliation).
4. (Optional) if `IPFS_CLUSTER_API_URL` set, also `ClusterPin(cid)` for
   replication factor.

### 5.2 Delete (unpin + GC) — **reference-checked**
1. On media/video/avatar delete, set the ledger row `state='unpinning'` in the
   delete transaction.
2. Worker claims it, but **before unpinning** re-checks that no *other* live
   ledger row (or DB reference) shares the CID (content-addressing means identical
   bytes ⇒ identical CID; two videos could dedupe to one CID). Only unpin when the
   CID's reference count hits zero. (This is the archive's guardrail: *"Never
   unpin without reference checking."*)
3. `client.Unpin(cid)`; on success `state='unpinned'` (or delete the row). Kubo GC
   is best-effort and **not** triggered synchronously — unpinning is the contract;
   actual block reclamation is the node's `repo gc`. Document that unpin ≠
   guaranteed erasure on a public network (privacy implication, see §7).

### 5.3 Re-transcode (re-pin new renditions)
The HLS ladder is *"replaced wholesale when a video is re-transcoded"*
(`0039_transcoding` comment). So on re-transcode: enqueue **unpin** of the old HLS
`car_root` + old rendition rows, and **pin** the new tree (new `object_key`s / new
`car_root`). Thumbnails/storyboards regenerated in the same pass get the same
treatment (upsert `pending`, worker re-pins → new CID).

### 5.4 Idempotency
Pinning an already-pinned CID is a no-op; the `Upsert` is keyed on `object_key`;
re-enqueue of a `pinned` row with an unchanged key is a no-op. A re-uploaded avatar
changes the key (extension can change) → old key unpinned, new key pinned (matches
the existing `internal/profileimage` "delete old blob on re-upload" behavior).

---

## 6. HLS on IPFS — directory/UNIXFS add for VOD, exclude live (RECOMMENDED)

The choice is **segment-level adds** (one CID per `.ts`) vs a **directory/CAR add**
of the rendition (one root CID wrapping the variant playlist + its segments).

**Recommendation: directory add per video's finalized HLS tree** (Kubo
`add ...&wrap-with-directory=true&recursive=true`, the archive's `AddDirectory`),
yielding **one `car_root` CID** for the whole `streaming-playlists/<id>/` tree.
- **Because VOD HLS is immutable once transcoded** (`0039` says the tree is
  replaced wholesale, never appended-to), a directory add is safe: the tree is
  finalized before we add it. The single root CID is the natural handle, dedupes
  shared segments internally, and the relative URIs inside the playlists resolve
  under the gateway path `{gateway}/ipfs/{car_root}/720p/seg_00001.ts` with no
  rewriting.
- Segment-level adds create thousands of ledger rows per video and give no atomic
  "is this rendition fully pinned?" answer. Reject.

**Live HLS is explicitly excluded from mirroring** because the live edge is
**mutable** — segments are appended during the broadcast, so there is no stable
tree to content-address. Only the **finalized replay VOD** (`live_streams.replay_enabled`,
`0061`) is mirrored, and only after it converts to a normal published video and
passes the §3 public gate. This mutability boundary is the reason live and VOD are
treated differently.

Ledger representation: the HLS tree is one `media_ipfs_pins` row with
`media_class='hls'`, `object_key='streaming-playlists/<id>/'` (trailing slash =
directory intent), `car_root` = the wrap CID, `cid` = same as `car_root`.

---

## 7. ⚠️ PRIVACY — first-class section (the gating decision)

> **See also:** the PRIVATE mirroring tier (a second, fully separate swarm.key'd
> node for private/unlisted media) is designed in
> [`ipfs-media-private.md`](./ipfs-media-private.md); its §7 carries the full
> per-class encryption analysis (Class A/B verdicts + Lit rejection). This §7
> below governs the PUBLIC mirror only — the v1 "already-public only" gate.

**Threat:** content on the public IPFS network is **world-readable forever** once
its CID is known, and **unpin does not guarantee erasure** (other nodes may have
cached/re-pinned it). A CID leaks trivially — it's in gateway URLs, API responses,
and DHT provider records. Therefore *what we add to IPFS is a permanent public
disclosure decision*, not a storage detail.

The archive (AUDIT §5) got this **wrong**: it configured the node for the public
network and pinned content with **no public/private gate and no encryption**. We
must not reproduce that.

### The three options

**(a) Mirror only already-public media — RECOMMENDED, v1 scope.**
Add+pin only objects that are *already world-visible through the normal product*:
public+published videos and their derivatives (HLS, thumbnail, storyboard,
captions, VP9), non-unlisted user/channel avatars+banners, public playlist covers.
Everything private/unlisted/quarantined/DM/export/transient/remote is **excluded**
(the §3 gate). Nothing is disclosed to IPFS that isn't already disclosed by the
site.
- Pros: zero new privacy surface; no key management; simple, auditable gate;
  matches the user's spirit ("IPFS badge is truthful for public content").
- Cons: "ALL media" is scoped to "all *public* media." Private videos, DM
  attachments, private avatars are **not** on IPFS.

**(b) Encrypt-before-add for private media.**
AES-256-GCM envelope each private object, add the ciphertext, keep the key in
Postgres/KMS; only key-holders can decrypt.
- Pros: private media could technically live on IPFS.
- Cons: you gain almost nothing — a content-addressed *public* network storing
  opaque ciphertext is just an expensive, un-garbage-collectable blob store; you
  still can't unpin reliably, so a **future key compromise retroactively exposes
  everything ever pinned**; you own a key-management + rotation burden;
  deduplication (IPFS's point) is defeated by per-object keys. **Explicitly OUT
  for DM attachments** — encrypted conversations already hold opaque Olm
  ciphertext (`0058_e2ee`), and pinning per-recipient-device envelopes to a
  permanent public network is a metadata/forensic liability for no user benefit.

**(c) Private IPFS network / IPFS Cluster.**
A swarm-key'd private network (or a permissioned IPFS Cluster) where content is
only reachable by operator-controlled nodes; no public gateway resolves it.
- Pros: real multi-node redundancy (STOR-05) inside a trust boundary; private
  media *could* be mirrored here without world exposure; the archive already had
  cluster auth (Bearer/mTLS) proving the wiring.
- Cons: not "distribute on the public IPFS" — it's private replicated storage.
  Operational weight (swarm key distribution, cluster ops). Doesn't make private
  content publicly resolvable (which is correct, but means the "IPFS badge" on
  private content would mean "replicated," not "public gateway").

### Recommendation

- **v1 = (a).** `IPFS_MIRROR_PRIVATE=false` is the **hard default**; the §3 gate
  excludes all non-public classes. Ship truthful mirroring of public media.
- **Unlisted is treated as private** for mirroring: a permanent public CID would
  defeat the URL-unguessability that "unlisted" promises.
- **DM attachments are never mirrored to public IPFS**, in any version. If they are
  ever replicated, it must be via **(c) a private cluster** only, and coordinated
  with the Messaging v2 spec (dependency:
  `/Users/yosefgamble/.claude/jobs/ba84d0be/tmp/messaging-v2/` — not present at
  authoring time; do not implement attachment mirroring until that spec defines
  the encryption/visibility contract).
- **Private/quarantined videos + private avatars**: excluded in v1; if a future
  operator wants them mirrored, gate strictly behind **(c)** and
  `IPFS_MIRROR_PRIVATE=true` + `IPFS_CLUSTER_API_URL` set (never raw public).

**This is the top open question for the user** (see SUMMARY.md): if "ALL media"
must literally include private/unlisted/DM content on the *public* network, that
changes the answer materially and requires an explicit, signed-off privacy waiver
— which this spec recommends against. If "ALL media" means "everything the site
already shows publicly," option (a) delivers it.

### Belt-and-suspenders
- CID validation (port the archive's `cid_validation.go`) before any URL/pin/fetch:
  CIDv1-only, codec whitelist, path-traversal + control-char blocks, length cap,
  SHA-256/Blake2b multihash.
- Never log full CIDs at info (they're capability-like handles to public content);
  see §9.

---

## 8. Config surface

New env vars (validated in `config.go` `Load` + `Validate`; all OFF by default so
`make ci` and existing deploys are unaffected):

| Env | Type | Default | Meaning |
|---|---|---|---|
| `IPFS_ENABLED` | bool | `false` | Master switch for the mirror. Off ⇒ no worker, no enqueue, endpoints 503. |
| `IPFS_API_URL` | url | `` | Kubo RPC address, e.g. `http://ipfs:5001`. **Required when enabled.** |
| `IPFS_GATEWAY_URL` | url | `` | Public-facing gateway base for API responses, e.g. `https://ipfs.example.org`. **Required when enabled** (never emit `ipfs.io` by default). |
| `IPFS_ADD_TIMEOUT` | duration | `60s` | Per add/pin RPC timeout. |
| `IPFS_PIN_CONCURRENCY` | int | `2` | Worker parallelism (bounded — pinning is I/O heavy). |
| `IPFS_RECONCILE_INTERVAL` | duration | `5m` | Backfill scan cadence. |
| `IPFS_MIRROR_PRIVATE` | bool | `false` | **Privacy gate.** `true` is only permitted when `IPFS_CLUSTER_API_URL` is set (else config error) — private media may only go to a private cluster, never public. |
| `IPFS_CLUSTER_API_URL` | url | `` | Optional IPFS Cluster REST API for replication. |
| `IPFS_CLUSTER_TOKEN` | secret | `` | Cluster Bearer token. **Sensitive** — add to `observability.IsSensitiveKey` denylist; never logged. |

Validation rules:
- `IPFS_ENABLED=true` ⇒ `IPFS_API_URL` and `IPFS_GATEWAY_URL` required, else
  `config: IPFS_API_URL and IPFS_GATEWAY_URL are required when IPFS_ENABLED`.
- `IPFS_MIRROR_PRIVATE=true` AND `IPFS_CLUSTER_API_URL=''` ⇒ hard error
  `config: IPFS_MIRROR_PRIVATE requires a private IPFS_CLUSTER_API_URL (refusing to mirror private media to a public network)`.
- `STORAGE_BACKEND=ipfs` stays rejected (unchanged). Update the doc comment at
  `config.go:280-283` to point at this spec instead of "ipfs later."

---

## 9. Observability

- **Structured logs** (slog, existing pattern): `ipfs_pin_ok{media_class, byte_size,
  attempts}`, `ipfs_pin_failed{media_class, attempts, err}`, `ipfs_unpin_ok`,
  `ipfs_node_unhealthy`. **CID at `debug` only** — never at info (no CID spam; CIDs
  are public capability handles). `object_key` and `video_id` are the info-level
  correlators.
- **Metrics** (whatever `internal/observability` exposes): gauges
  `ipfs_pins{state=pinned|pending|failed|unpinned}`, counters `ipfs_add_total`,
  `ipfs_add_failed_total`, `ipfs_bytes_pinned_total`; a node-health gauge.
- **Health**: a cheap `client.Version()` probe (Kubo `/api/v0/version`) feeds the
  admin status endpoint and a readiness signal. Never fail app readiness on IPFS
  (it's non-authoritative).
- **Admin visibility endpoint** (see §10): counts of pinned/pending/failed per
  media_class + node reachability + gateway URL in use.

---

## 10. Endpoint / OpenAPI changes (contract-first — edit `openapi.yaml` first)

Additive only; nothing existing changes shape.

1. `GET /api/v1/ipfs/status` (admin) → `{ enabled, node_reachable, gateway_url,
   cluster_enabled, pins: { pinned, pending, failed, unpinned }, by_class: [...] }`.
   `503` when `IPFS_ENABLED=false`. (Generalizes the archive's `/ipfs/metrics` +
   `/ipfs/gateways`.)
2. `POST /api/v1/admin/ipfs/reconcile` (admin) → kick an immediate backfill scan;
   returns counts enqueued. Idempotent.
3. **Additive fields on existing public read models** (only populated when the
   object is pinned; omitted/empty otherwise, so clients degrade cleanly):
   - Video detail: `ipfs` object `{ hls_cid, original_cid, gateway_url }` (only for
     public+published+pinned videos).
   - Video card / feed item: a boolean `ipfs_pinned` (drives the badge truthfully —
     resolves the `VideoCard.tsx:12` "no storage field" blocker).
   - Channel/user profile: `avatar_ipfs_cid` / `banner_ipfs_cid` when pinned.
   These come from a join/lookup on `media_ipfs_pins`; keep them out of hot list
   queries unless cheap (a single indexed `IN` on object keys, or the optional
   denormalized column from §4).

`make openapi-verify` must pass (the canonical gate includes it).

---

## 11. Test requirements

`make ci` = `fmt-check vet openapi-verify sqlc-verify test-race`. Must stay green
**with IPFS entirely absent** (no node in CI by default).

**Unit (always run, fake node):**
- A `FakeIPFSClient` implementing the client interface (Add/Pin/Unpin/Version/
  IsPinned) in-memory. No network.
- Enqueue-eligibility table: every media class × {public, private, unlisted,
  quarantined} → asserts the §3 gate (public video ⇒ enqueued; private/unlisted/DM
  ⇒ **not** enqueued). This is the privacy regression fence.
- Worker: pending→pinned happy path; add-fails→failed after backoff; unpin
  reference-count guard (two rows same CID ⇒ first delete does **not** unpin).
- Re-transcode: old HLS `car_root` unpinned, new pinned.
- Privacy config guard: `IPFS_MIRROR_PRIVATE=true` without cluster URL ⇒ config
  error (`config_test.go` table).
- CID validation: port the archive's fuzz/table tests (CIDv0 rejected, traversal
  blocked, codec whitelist).
- **Failure semantics**: node unreachable ⇒ upload still succeeds, row stays
  `pending`, no 5xx (drive the upload handler with the mirror pointed at a dead
  address).

**Integration (tagged, skipped when node absent):**
- Build tag `//go:build ipfs_integration` (or `testing.Short()` skip) so
  `go test ./...` and `make ci` never require a node. Runs against a real
  `ipfs/kubo` on `:15001` (the archive's CI pattern: a `setup-ipfs-test`-style
  step + a dedicated `ipfs-test` compose service). Covers a real add→pin→cat
  round-trip and a directory add of a small HLS tree resolving through the
  gateway. A separate CI job (opt-in) boots kubo and runs `-tags ipfs_integration`;
  the canonical `backend-ci.yml` gate does **not**, so it stays green.

---

## 12. Compose (local dev)

Add a `kubo` service under an `ipfs` profile (currently unchecked at
`fix_plan.md:127`), modeled on the archive:
```yaml
  ipfs:
    profiles: ["ipfs", "full"]
    image: ipfs/kubo:v0.32.1
    environment: [ "IPFS_PROFILE=server" ]
    ports:
      - "4001:4001"      # swarm TCP
      - "4001:4001/udp"  # swarm QUIC
      - "5001:5001"      # RPC API
      - "9090:8080"      # gateway (9090 avoids app:8080 clash)
    volumes: [ ipfs_data:/data/ipfs ]
    healthcheck:
      test: ["CMD","wget","--spider","-q","http://127.0.0.1:5001/api/v0/version"]
```
The api service, when `IPFS_ENABLED=true`, sets `IPFS_API_URL=http://ipfs:5001`
and `IPFS_GATEWAY_URL=http://localhost:9090` (dev). **Do not** copy the archive's
`configure-node.sh` public-network AutoRelay/hole-punching config wholesale into a
default profile — for dev keep the node local; document the public-network config
as an opt-in for operators who intend public distribution (it is a privacy-relevant
choice).

---

## 13. Slice order (vertical, contract-first — see fix_plan-tasks.core.md)

1. **Ledger + config + client (no worker yet)** — table, sqlc, config vars +
   validation (incl. the privacy guard), the Kubo RPC client + CID validation,
   `FakeIPFSClient`. Endpoints stubbed 503. (P2P-01 foundation.)
2. **Worker + enqueue for images** (avatars/banners/thumbnails/storyboards/captions/
   playlist covers — single-file adds, simplest). Reconciliation scan. Admin
   `/ipfs/status`. (P2P-01, STOR-05 groundwork.)
3. **Video originals + VP9** (single-file, quota-relevant) + additive API `ipfs`
   fields + feed `ipfs_pinned`.
4. **HLS directory add** (VOD only) + re-transcode re-pin + delete/unpin
   reference-count GC. (P2P-03 CID details.)
5. **Optional cluster replication** (STOR-05) + compose kubo + integration CI job.
6. **Backfill of pre-existing public media** (one-shot admin reconcile over all
   eligible rows). (STOR-04 adjacency — pinset is a distribution/backup surface.)

Each slice: OpenAPI/schema first, unit tests (fake node), canonical gates green,
pushed.

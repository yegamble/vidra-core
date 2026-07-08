# Spec extension: P19.P — Private IPFS Mirroring Phase (`vidra-core`)

> Extends the shipped `vidra-core/.ralph/specs/ipfs-media.md` (P19.1–P19.6,
> commits cdd2d8a..49a25d8). Drop-in target: append as a new top-level section of
> that spec (or a sibling `ipfs-media-private.md` cross-linked from §7).
> Same conventions: Go / Echo / sqlc / PostgreSQL, contract-first, mirror-sidecar
> (authoritative store stays local/S3), `make ci` green nodeless.
>
> Status: **designed, awaiting user go/no-go** (see SUMMARY.md decision points).
> Prompted by the user sharing Pinata's "What is Private IPFS?" post.
> Prereq: the P19 audit repairs (mark-pinned lost-update race; unlisted-owner
> gating) must land first — this phase builds on the eligibility re-evaluation
> machinery those fixes harden. This spec does not modify P19 behavior for
> public media.

---

## 0. What "private IPFS" actually is (research classification)

**The Pinata post describes a hosted product, not a protocol feature.** In
Pinata's model ("Private IPFS API", Q4 2024) the file is *not broadcast to the
public IPFS network*: "we simply keep it enclosed within the IPFS node and grant
access through the node's gateway or similar infrastructure." Content addressing
(CIDs) is preserved; privacy comes from **not announcing** the content plus
**auth-gated retrieval through Pinata-operated gateways**. The post does not
mention swarm.key, private swarms, or any self-host pattern — it is a managed
service pitch. ([pinata.cloud](https://pinata.cloud/blog/what-is-private-ipfs/))

**The self-hostable pattern is different and stronger — network-level isolation:**

- **kubo private networks (LibP2P PNet / `swarm.key`)** — a pre-shared key at
  `$IPFS_PATH/swarm.key`; peers only talk to peers holding the same key.
  `LIBP2P_FORCE_PNET=1` makes the daemon **fail to start** unless a private
  network is configured (fail-closed). Default bootstrap nodes are unreachable
  by design — you must run your own bootstrap peers.
  ([kubo experimental-features.md](https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md))
- **Announce/reprovide suppression** — `Routing.Type=none` disables routing
  entirely (explicit peering only); `Reprovider.Interval=0` (newer kubo:
  `Provide.DHT.Interval`) disables content announcements. On a swarm.key'd
  network these are belt-and-suspenders: there is no public DHT to leak to, but
  suppression keeps even the private DHT quiet on small clusters.
  ([kubo config.md](https://github.com/ipfs/kubo/blob/master/docs/config.md),
  [discuss.ipfs.tech](https://discuss.ipfs.tech/t/how-can-i-disable-dht-in-kubo/19249))
- **IPFS Cluster on a private swarm** — cluster peers share a `CLUSTER_SECRET`
  (32-byte hex; authenticates cluster membership) *in addition to* the node
  swarm.key; `ipfs-cluster-ctl pin add` replicates a pin across all followers.
  Ports: 9094 REST API, 9095 IPFS proxy, 9096 cluster swarm.
  ([ELEKS private-cluster writeup](https://eleks.com/research/ipfs-network-data-replication/),
  [geekdecoder guide](https://geekdecoder.com/setting-up-a-private-ipfs-network-with-ipfs-and-ipfs-cluster/))
- **Auth-gated serving** — kubo's gateway has **no built-in auth**; the
  documented pattern for private serving is `Gateway.NoFetch=true` (serve only
  local repo content), bind internally, and put auth in a reverse proxy or —
  our case — serve through the application's already-authenticated API.
  ([IPFS gateway concepts](https://docs.ipfs.tech/concepts/ipfs-gateway/),
  [kubo config.md](https://github.com/ipfs/kubo/blob/master/docs/config.md))

**Operational realities (accept before going in):**
- **swarm.key distribution is manual** (copy to every node) and **there is no
  rotation or per-node revocation mechanism**: possession of the key = full
  network membership; rotating means generating a new key and doing a
  coordinated restart of every node (nodes on different keys cannot talk). This
  is a known, community-acknowledged gap.
  ([discuss.ipfs.tech identity/revocation thread](https://discuss.ipfs.tech/t/discussion-identity-revocation-and-node-roles-in-private-ipfs-networks/20247))
- **Public resolution breaks, by design**: public gateways (ipfs.io etc.) can
  never resolve private-swarm CIDs; external pinning services cannot join;
  cross-instance replication is limited to peers you key in.
- **NAT traversal quirks on PNet**: AutoNAT/relay behave differently on private
  networks ([kubo#7067](https://github.com/ipfs/kubo/issues/7067)) — irrelevant
  for a LAN/VPC cluster, relevant if peers span NATed sites.

**Consequence for naming:** on the private network, "on IPFS" means
**replicated within an operator-controlled trust boundary** — not "publicly
distributed". The product surface must never imply otherwise.

---

## 1. Architecture: a second, fully separate private swarm — never dual-home

**Decision: run a dedicated private kubo (+ optional IPFS Cluster) that is a
DIFFERENT node (and swarm) from the public-mirror node. One node never serves
both networks.**

Why a hard split, not one node with two personalities:
1. **PNet is all-or-nothing per node.** A kubo with a swarm.key talks *only* to
   same-key peers; it cannot simultaneously participate in the public DHT. A
   "dual-homed" design would require running without PNet and relying purely on
   announce suppression — one config regression away from announcing private
   CIDs publicly. The swarm.key makes the failure mode "private content
   unreachable", never "private content public".
2. **Blast radius.** The public node's whole job is to be publicly reachable
   (AutoRelay etc., per P19 §12). Those settings are actively dangerous on a
   node holding private media. Separate nodes = separate configs = no shared
   failure surface.
3. **Fail-closed enforcement exists only per-node**: `LIBP2P_FORCE_PNET=1` on
   the private node guarantees it never boots keyless. There is no equivalent
   "never announce this subset" guarantee on a shared node.

```
 public media           private/unlisted media
      │                          │
      ▼                          ▼
 kubo-public  (P19)         kubo-private  (P19.P)
 public DHT, AutoRelay      swarm.key + LIBP2P_FORCE_PNET=1
 IPFS_API_URL               IPFS_PRIVATE_API_URL
 gateway URL in API         NO public gateway; Gateway.NoFetch;
 responses                  serving stays on the authenticated app API
      │                          │
 (optional) public-side     (optional) ipfs-cluster on the private swarm
 cluster                    (CLUSTER_SECRET; replication factor)
```

The `ipfsmirror` worker gains a second client handle and routes each ledger row
to exactly one network. **A private row must never fall back to the public
client** (fail-closed routing; unit-tested).

---

## 2. Config surface (reframes the P19 §8 guard)

The shipped guard — `IPFS_MIRROR_PRIVATE=true` without `IPFS_CLUSTER_API_URL`
is a hard config error — **stays in force until this phase ships**, then is
*replaced* by the stricter shape below (the old guard piggybacked on the cluster
URL as a proxy for "private infra exists"; the real requirement is a dedicated
private node):

| Env | Type | Default | Meaning |
|---|---|---|---|
| `IPFS_MIRROR_PRIVATE` | bool | `false` | Master opt-in for mirroring non-public media. Unchanged name; new validation. |
| `IPFS_PRIVATE_API_URL` | url | `` | RPC of the **dedicated private-swarm kubo**. **Required when `IPFS_MIRROR_PRIVATE=true`.** |
| `IPFS_PRIVATE_CLUSTER_API_URL` | url | `` | Optional IPFS Cluster REST on the private swarm (replication). |
| `IPFS_PRIVATE_CLUSTER_TOKEN` | secret | `` | Cluster auth; sensitive-key denylist like `IPFS_CLUSTER_TOKEN`. |
| `IPFS_PRIVATE_ADD_TIMEOUT` / `IPFS_PRIVATE_PIN_CONCURRENCY` | | inherit public defaults | Per-network worker tuning. |

Validation (all hard errors at boot):
- `IPFS_MIRROR_PRIVATE=true` ⇒ `IPFS_PRIVATE_API_URL` required.
- `IPFS_PRIVATE_API_URL == IPFS_API_URL` ⇒ error
  `config: the private IPFS mirror must be a separate node from the public mirror (refusing to dual-home)`.
- `IPFS_MIRROR_PRIVATE=true` with `IPFS_ENABLED=false` is allowed (an operator
  may run only the private replication tier).
- **No `IPFS_PRIVATE_GATEWAY_URL` exists.** Private CIDs are never emitted with
  gateway URLs (see §5); not having the knob is the guarantee.

---

## 3. Ledger change: a `network` column

One migration on the shipped table (naming continues the P19 sequence):

```sql
-- P19.P: route each pin to exactly one swarm. Existing rows are all public
-- (P19 only ever enqueued public-eligible media).
ALTER TABLE media_ipfs_pins
    ADD COLUMN network TEXT NOT NULL DEFAULT 'public'
        CHECK (network IN ('public', 'private'));

-- Admin counts and the reconcile scan slice by network.
CREATE INDEX media_ipfs_pins_network_state_idx
    ON media_ipfs_pins (network, state, media_class);
```

- `object_key` stays the PK: an object lives on exactly one network at a time,
  derived from eligibility. A **privacy flip** (public↔private) transitions the
  same row: e.g. public→private = `state='unpinning'` on `network='public'`;
  once unpinned, the row is re-armed `network='private', state='pending',
  cid=''` (CID is recomputed on the private add — same bytes give the same CID,
  but never assume; the ledger records what the private node returned).
  This rides the same re-evaluation triggers the P19 audit repair hardens
  (privacy update, `users.unlisted` toggle, publish/delete transitions).
- sqlc deltas: `ClaimDue`/counts/reconcile queries take a `network` param;
  `CountByStateClass` becomes `CountByNetworkStateClass`.

---

## 4. Eligibility matrix v2 (per-class × network)

Supersedes the P19 §3 "v1 Mirror?" column. "—" = not mirrored anywhere.

| Media class / condition | Network | Rationale |
|---|---|---|
| Video public+published (+ all derivatives: HLS, thumb, storyboard jpg+vtt, captions, webm) | **public** | unchanged P19 |
| Video `privacy='private'` (+ derivatives) | **private** | the point of the phase |
| Video `privacy='unlisted'` (+ derivatives) | **private** | unlisted promises URL-unguessability; public CIDs would break it — private swarm only |
| Video `state='quarantined'` | **—** | unvetted content stays out of every mirror until approved; replicating pre-moderation media multiplies legal exposure |
| Avatars/banners, owner not unlisted, account active | **public** | unchanged P19 |
| Avatars/banners of `users.unlisted=true` or deactivated accounts | **private** | identity assets of low-discoverability accounts |
| Playlist cover, `visibility='public'` | **public** | unchanged P19 |
| Playlist cover, non-public playlist | **private** | follows owner visibility |
| **DM attachments — plaintext threads** (`message_attachments`, `dm-attachments/…`) | **—** | **Messaging v2 D7 rule: plaintext must not land readable on ANY multi-node network.** These files are participant-gated plaintext; replicating them — even to a private swarm — widens the plaintext trust surface (every keyed node + cluster peer) for zero user-facing benefit. Not mirrored, any network, this phase. |
| **DM attachments — E2EE threads** (future `e2ee-blobs/<conversation>/<id>`, Messaging v2 D7) | **private (deferred)** | D7 stores **ciphertext only** (client-encrypted, no metadata) — safe to replicate to the private swarm *when that slice exists*, since even keyed nodes hold opaque bytes. Deferred until the e2ee-blob endpoint ships; recorded here so the D7 storage shape isn't precluded. Never public: CIDs of ciphertext still leak conversation-activity metadata. |
| Account exports | **—** | PII + 7-day expiry; mirroring fights the deletion semantics |
| Upload chunks, live HLS edge, remote-video thumbnail cache | **—** | unchanged P19 (transient / mutable / not ours) |

**Rule stated per thread type (the D7 contract):** plaintext conversations →
attachments never mirrored on any network; encrypted conversations → only the
client-encrypted `e2ee-blobs` ciphertext may ever be pinned, private network
only, and only after the Messaging v2 E2EE-attachment slice defines it.
**Full encryption analysis — including why encrypt-then-pin-publicly is
rejected for private videos and why Lit Protocol doesn't fit — in §7.**

---

## 5. Serving path: the private mirror is replication, not distribution

- **Private CIDs never appear in public API responses.** `ipfs_pinned` and the
  `ipfs{}` object (P19 §10) remain public-network-only signals; a private video
  keeps `ipfs_pinned` absent/false in every list/detail payload. No gateway URL
  is ever constructed for a private pin (no config knob exists, §2).
- Viewer-facing serving of private/unlisted media is **unchanged**: the
  authenticated app API reads the authoritative backend (local/S3), exactly as
  today. The private swarm is a durability/replication tier (DR restore,
  multi-node redundancy — the STOR-05 story for non-public media), not a
  playback path.
- The private kubo's gateway is **not exposed**: internal bind only,
  `Gateway.NoFetch=true`, no reverse proxy route. If an operator wants gateway
  reads for DR tooling, that is an infra decision outside the app contract
  (reverse-proxy auth per the IPFS docs pattern) — the app never links to it.
- **Admin surface**: `GET /api/v1/ipfs/status` gains a `networks` split —
  `{public:{…}, private:{enabled, node_reachable, pins{…}, by_class[]}}` —
  additive, and the reconcile endpoint takes an optional `network` filter.
  (vidra-user AdminIpfsView renders the second column; badge semantics on
  public surfaces are untouched — a "Replicated" owner-facing chip is possible
  later but is out of this phase.)

---

## 6. Hosted-provider abstraction (Pinata et al.) — evaluated, not adopted

Should the private tier support a hosted "private IPFS" API (Pinata) behind an
interface, alongside self-hosted kubo/cluster? **Recommendation: no — not in
this phase; keep a seam, ship nothing.**

- **Trust boundary is the whole point.** The private tier exists so non-public
  media never leaves operator control. A hosted provider re-introduces a
  third-party data processor for exactly the content classes users marked
  private — a worse privacy posture than today's S3 (which at least the
  operator chooses and scopes), and a GDPR/data-processing-agreement burden.
- **Vendor lock-in + egress economics.** Pinata's private API is proprietary
  (it is not the standard IPFS Pinning Service API), and video-scale objects
  make per-GB storage/egress dominate; the authoritative S3 store already
  provides paid durability without adding a second vendor.
- **Secrets blast radius.** A hosted API key becomes a bearer credential to
  *all* private media — a bigger single secret than the swarm.key (which only
  grants network membership inside the operator's own perimeter).
- **The seam we keep:** the mirror worker talks to a narrow client interface
  (add/pin/unpin/health). If a hosted adapter is ever justified (e.g. an
  operator who cannot run nodes), it slots in as another implementation behind
  that interface plus a config choice — a later, self-contained slice. Do not
  widen the interface preemptively; do not implement now.

---

## 7. Encryption strategies — the serious analysis (per-class verdicts)

> Amended after the user's follow-up ("Encryption might be the best way for IPFS
> for encrypted private messaging and private videos") and the shared Pinata ×
> Lit Protocol tutorial
> ([pinata.cloud](https://pinata.cloud/blog/how-to-encrypt-and-decrypt-files-on-ipfs-using-lit-protocol-and-pinata/)).
> This replaces the one-paragraph encrypt-before-add rejection in
> `ipfs-media.md §7(b)` with a real engagement. The distinction that changes
> everything: **who encrypted the bytes, and who holds the key.**

### 7.1 Ground truth first (verified in the repo, read-only)

- **E2EE-thread attachments do not exist yet.** The committed Messaging v2 spec
  (`vidra-core/.ralph/specs/messaging-v2.md` D7) keeps the **422** on
  `POST /conversations/{id}/attachments` for encrypted conversations. D7 records
  a *future* design: client encrypts with a random content key
  (XChaCha20-Poly1305 / AES-256-GCM), uploads **ciphertext only** to
  `POST /api/v1/e2ee/blobs` (`e2ee-blobs/<conversation>/<id>`; server stores
  only size + storage key; filename/type/dimensions/content key travel inside
  the Olm envelope). So "already-E2EE attachments" are a **planned** risk
  class, not a shipped one — any pinning decision for them activates only when
  that slice ships.
- **Plaintext-thread attachments exist today and are NOT encrypted** —
  `message_attachments` rows point at raw blobs under `dm-attachments/…`
  (`internal/messaging` has no encryption path; participant gating is the only
  protection).
- Private/unlisted videos and derivatives are server-readable plaintext on the
  authoritative store.

### 7.2 Class A — already-E2EE content (future `e2ee-blobs`): pinning ciphertext

When D7's slice ships, the server holds bytes it *cannot read*. Pinning that is
a fundamentally different risk class from server-side encryption: **no key ever
exists server-side**, so there is no key to leak, rotate, or subpoena from the
operator. Confidentiality of the *content* on a public network would be as
strong as Olm itself. What still leaks if pinned **publicly**:

- **Blob size + upload timing** — classic traffic analysis; sizes fingerprint
  media types and, combined with timing, correlate to conversation activity.
- **Pin-graph correlation** — the instance's node is the DHT provider for every
  blob CID; an observer harvesting provider records learns *this instance's
  users exchanged N encrypted files at these times*, and CID publication order
  clusters blobs into probable conversations.
- **Instance IP exposure** — provider records tie the operator's node identity
  and address to that activity (participants themselves are not exposed —
  clients fetch through the app API, never the DHT — unless a future client
  ever fetched via public gateways, which would leak reader interest to
  gateway logs).
- **Harvest-now, decrypt-later permanence** — a later compromise of a
  participant device (leaking content keys inside stored Olm envelopes)
  retroactively decrypts everything an adversary harvested; public pinning
  makes harvesting free and complete.

And the benefit of public pinning is ~zero: the audience of an E2EE blob is the
conversation's participants, who are served through the authenticated app —
there is no CDN/distribution win to buy with that metadata.

**Verdict (Class A): private swarm, yes — public network, no.** Pinning
ciphertext to the **private swarm** captures the entire redundancy benefit with
none of the metadata cost (nothing observable outside operator-keyed peers).
This confirms the §4 row: `e2ee-blobs` → private, **deferred** until the D7
slice exists. It also *upgrades* the rationale: this is not a grudging
exception — E2EE ciphertext is the *best-suited* private content class for IPFS
replication, precisely because the operator can replicate what it cannot read.

### 7.3 Class B — server-readable private media: encrypt-then-pin-publicly

The honest steelman, not the strawman. A modern design would be: per-object
random **DEK** (AES-256-GCM), DEKs wrapped by an operator **KEK** (env/KMS),
ciphertext pinned to the public network, keys served to authorized viewers by
the authenticated API. Notably, HLS even has native support for this shape
(AES-128 segment encryption with an auth-gated key URI), so encrypted VOD on
public IPFS is *technically* coherent. Rotation is cheap (re-wrap DEKs under a
new KEK). What the steelman still cannot fix:

- **Rotation does not re-protect fetched ciphertext.** Re-wrapping keys only
  guards the key store. Anyone who harvested the public ciphertext decrypts it
  the day any DEK/KEK leaks — and public pinning means we must assume complete
  harvest. The blast radius is **monotonically growing for the lifetime of the
  operator's keys**, which is exactly the wrong property for content a user
  marked private. (Even Pinata's own tutorial concedes: *"there will always be
  limitations to encryption and eventually current methods may be cracked."*)
- **The metadata leaks of §7.2 apply identically** (sizes, timing, pin-graph,
  instance IP) — for videos they are worse: rendition-ladder size patterns
  fingerprint content, and a privacy *flip* (public→private) can't retract the
  already-harvested public ciphertext.
- **Distribution utility is illusory for this audience.** Public gateways can
  serve the ciphertext, but only key-holders can use it, and keys come from the
  operator's API anyway — so the effective audience equals the audience the
  private swarm already serves, minus nothing. Dedup across identical plaintext
  is destroyed by per-object DEKs.
- **New ops surface**: KEK custody/KMS, a key-serving endpoint (a fresh
  attack target), client-side decrypt paths (HLS key delivery, image decrypt in
  the browser for covers/avatars — heavy for `<img>` surfaces).

**Head-to-head for Class B (private/unlisted video + derivatives):**

| Criterion | Encrypt → public network | Private swarm (§1) |
|---|---|---|
| Privacy guarantee | Cryptographic only; existence/size/timing public forever | Network-level; nothing observable outside keyed peers |
| Blast radius over time | Grows forever (harvest-now-decrypt-later; permanence unfixable) | Bounded: key leak alone insufficient — adversary must also reach operator peers; content removable (unpin actually works inside a closed swarm) |
| Ops burden | KEK/KMS custody + key endpoint + client decrypt (HLS key URIs, browser decrypt) | swarm.key custody + one extra node/cluster (no per-request crypto) |
| Egress/replication utility | None beyond what auth-gated serving already has | Exactly matches the audience: operator-controlled replicas, DR restore |

**Verdict (Class B): private swarm. Encrypt-then-pin-publicly is rejected — now
on the merits, not by reflex.** The one legitimate future niche for encrypted
HLS on public IPFS is DRM-lite *public* distribution (pay-gated but
world-replicated content) — a different product feature, out of scope.

### 7.4 Lit Protocol specifically — assessed for a self-hosted, federated platform

What the tutorial actually shows: **client-side** encryption
(`LitJsSdk.encryptFileAndZipWithMetadata`), keys held by **Lit's threshold
network** (no single custodian, but decidedly *not the operator*), decryption
gated by **programmable, blockchain-native access-control conditions** —
wallet balance (`eth_getBalance`), NFT ownership (`ERC721 balanceOf`), DAO
membership, or wallet-address match — authorized via a wallet signature
(`checkAndSignAuthMessage({ chain: 'ethereum' })`) against the Lit network
(the tutorial runs on the `cayenne` testnet). The ciphertext + encrypted
symmetric key + ACCs are bundled and pinned.

Fit assessment for Vidra:

- **Auth-model mismatch.** Vidra authorization is session/JWT + Postgres rows
  ("participant of conversation X", "owner of channel Y"). Lit ACCs are
  wallet/on-chain predicates; mapping Vidra's authz onto them would mean
  putting an on-chain mirror of Vidra's permission state (or custom Lit
  Actions) in front of every decrypt. Vidra users don't have wallets.
- **Availability coupling.** Every decrypt requires the external Lit network to
  be up and reachable — a hard runtime dependency on a third-party network for
  reading one's own private media, on a platform whose premise is
  self-hosting.
- **Key custody leaves the operator.** Threshold custody across Lit nodes is a
  *different* trust model, not a lesser one — but it is precisely not
  "operator-controlled", which is the requirement this phase exists to satisfy.
  Federation compounds it: every instance would independently depend on Lit.
- **Maturity**: tutorial-grade, testnet-anchored; a moving SDK surface.

**Verdict: rejected as the privacy layer for core private media.** Worth
remembering as a *product* option if Vidra ever ships wallet-native, token-gated
content (e.g. an Inner-Circle-style membership gate) — that is Lit's actual
shape: public distribution with crypto-native entitlements, not private storage.
**The explicit decision point for the user is therefore: self-managed keys
(only if Class B encryption were ever revisited) vs Lit (only if token-gating
becomes a product goal) — for this phase, neither: the private swarm carries
Class B, and Class A pins its own client-made ciphertext.**

### 7.5 Per-media-class verdict matrix

| Media class | Public net, raw | Public net, ciphertext | Private swarm | Not mirrored | Verdict + rationale |
|---|---|---|---|---|---|
| Public media (P19 classes) | ✅ ships | — pointless | — pointless | | **Public raw** (unchanged P19) |
| Private video + derivatives | ❌ never | ❌ rejected §7.3 (permanence, metadata, growing blast radius, no audience win) | ✅ | | **Private swarm** |
| Unlisted video + derivatives | ❌ breaks unguessability | ❌ same as above | ✅ | | **Private swarm** |
| Quarantined video | ❌ | ❌ | ❌ unvetted | ✅ | **Not mirrored** until approved |
| Plaintext-thread DM attachments (exist today, unencrypted) | ❌ | ❌ server-side encrypting them makes the operator the key-holder for user files — Class B with an even smaller audience | ❌ D7: plaintext must not land readable on any multi-node network; server-encrypted copies still widen the plaintext-capable surface (operator keys + every keyed node) | ✅ | **Not mirrored, any network** |
| E2EE-thread attachments (future `e2ee-blobs` — client-encrypted ciphertext, D7; **do not exist yet**) | ❌ | ⚠️ content-safe but metadata-leaky + harvestable (§7.2) → rejected | ✅ best-fit class for replication | (default until slice ships) | **Private swarm, deferred** on the D7 slice |
| Account exports / upload chunks / live edge / remote cache | ❌ | ❌ | ❌ | ✅ | unchanged P19 |

Record the Class A/Class B verdicts + the Lit rejection in
`product-decisions.md` at close-out (P19.P4) so Ralph doesn't relitigate.

## 8. Failure semantics

Everything inherits P19's "the mirror never breaks the product":
- Private node down ⇒ uploads/privacy-flips still succeed; rows sit `pending`
  on `network='private'`; reconciliation backfills on recovery.
- **No cross-network fallback, ever**: a private row with an unreachable
  private node stays pending — the worker must be provably incapable of
  pinning it via the public client (fail-closed routing; the network is chosen
  from the ledger row, and the public client handle is never passed to the
  private path). This is the phase's cardinal invariant, unit-tested.
- Boot-time misconfig (missing/duplicate API URL) fails config validation, not
  runtime behavior (§2).
- Privacy flip races resolve through the ledger state machine (the repaired
  P19 mark-pinned CAS): a flip during an in-flight public pin lands as
  unpin-then-re-arm, never as a private object pinned public.

---

## 9. Compose + test strategy

**Compose (dev, new `ipfs-private` profile):**
- `kubo-private`: pinned `ipfs/kubo` image; a tiny init script generates
  `swarm.key` into a named volume on first boot (`/key/swarm/psk/1.0.0/` +
  `/base16/` + 32 random bytes) — dev-only convenience, prod keys are
  operator-managed; env `LIBP2P_FORCE_PNET=1`; init clears default bootstrap
  (`ipfs bootstrap rm --all`), sets `Routing.Type=none` (explicit peering) and
  `Reprovider.Interval=0` (belt-and-suspenders), `Gateway.NoFetch=true`,
  gateway bound to the compose network only (no host port). RPC on a distinct
  host port (e.g. `5002:5001`) for `IPFS_PRIVATE_API_URL`.
- Optional `kubo-private-2` + `ipfs-cluster` pair sharing the volume-mounted
  swarm.key + a generated `CLUSTER_SECRET` for replication testing (cluster
  ports 9094/9096 internal).

**Unit (fake clients, always in `make ci`):**
- Network-routing table: every media class × privacy state → expected network
  (`public`/`private`/none) — extends the P19 eligibility fence and asserts the
  D7 rules (plaintext DM attachments: none on both networks).
- Fail-closed routing: private row + dead private fake ⇒ row pending, **zero
  calls observed on the public fake** (and vice versa).
- Privacy-flip transitions: public→private re-arms the row (unpin public →
  pending private), private→public the reverse; delete unpins on whichever
  network holds the pin (reference-check per network).
- Config validation table incl. the dual-home rejection.

**Integration (`//go:build ipfs_private_integration`, skipped when absent —
`make ci` untouched):**
- Two kubo containers sharing a generated swarm.key + one kubo **outside** the
  swarm. Assert: add+pin on the private pair round-trips (cat from the second
  peer proves intra-swarm replication); the outside node **fails/times out**
  fetching that CID (isolation proof); with `LIBP2P_FORCE_PNET=1` and the key
  removed, the daemon refuses to start (fail-closed proof).
- Optional cluster leg: `ipfs-cluster-ctl status` shows PINNED on both peers.

---

## 10. Rollout order (vertical slices — see fix_plan-tasks-private.md)

1. **P19.P1** — config reframe + `network` column + sqlc + dual-client worker
   routing (fake clients; fail-closed tests). No behavior change with
   `IPFS_MIRROR_PRIVATE=false` (default).
2. **P19.P2** — eligibility v2 + privacy-flip network transitions + network-aware
   reconcile/backfill + admin status `networks` split (+ vidra-user AdminIpfsView
   column, tracked in that repo).
3. **P19.P3** — compose `ipfs-private` profile + swarm.key'd integration pair +
   isolation tests + optional cluster replication.
4. **P19.P4** — ops runbook (swarm.key custody: generation, distribution,
   rotation-by-coordinated-restart, "possession = membership" warning; what
   breaks publicly) + ledger/product-decisions close-out. E2EE-blob pinning
   stays a recorded deferral pending the Messaging v2 E2EE-attachment slice.

---

### Sources
- [Pinata — What is Private IPFS?](https://pinata.cloud/blog/what-is-private-ipfs/)
- [Pinata — Encrypt/decrypt files on IPFS with Lit Protocol](https://pinata.cloud/blog/how-to-encrypt-and-decrypt-files-on-ipfs-using-lit-protocol-and-pinata/)
- [kubo experimental-features.md — private networks, swarm.key, LIBP2P_FORCE_PNET](https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md)
- [kubo config.md — Routing.Type, Reprovider/Provide.DHT.Interval, Gateway.NoFetch](https://github.com/ipfs/kubo/blob/master/docs/config.md)
- [IPFS docs — gateway concepts / auth via reverse proxy](https://docs.ipfs.tech/concepts/ipfs-gateway/)
- [discuss.ipfs.tech — disabling DHT/providing](https://discuss.ipfs.tech/t/how-can-i-disable-dht-in-kubo/19249)
- [discuss.ipfs.tech — identity/revocation gaps in private networks](https://discuss.ipfs.tech/t/discussion-identity-revocation-and-node-roles-in-private-ipfs-networks/20247)
- [ELEKS — private IPFS network with IPFS-Cluster](https://eleks.com/research/ipfs-network-data-replication/)
- [geekdecoder — private IPFS + cluster setup](https://geekdecoder.com/setting-up-a-private-ipfs-network-with-ipfs-and-ipfs-cluster/)
- [kubo#7067 — AutoNAT on private networks](https://github.com/ipfs/kubo/issues/7067)

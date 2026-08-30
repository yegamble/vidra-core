# Federation design (ActivityPub) — vidra-core

Status: DESIGN (no code yet). This spec grounds P10 in `.ralph/fix_plan.md` so the
21 federation items become an ordered, decided backlog instead of open questions.
ActivityPub is PeerTube-parity; ATProto/Bluesky is a separate, independently-enabled
Vidra extension (P10.2) and is out of scope here beyond the "enable independently" rule.

PeerTube reference behaviour (not code): actors for accounts + channels, WebFinger
discovery, HTTP-signed inbox/outbox, `Video`/`Note` objects, `Follow`/`Accept`,
`Announce` (a channel announces its videos to followers), `Create`/`Update`/`Delete`,
and a delivery queue with retry. We mirror the *behaviour*, not the source.

---

## 1. Prerequisites and configuration

Federation needs a canonical public origin — there is none today (only `INSTANCE_NAME`,
`CORS_ALLOWED_ORIGINS`). Add:

- `PUBLIC_BASE_URL` (e.g. `https://videos.example`) — the canonical https origin used to
  build every actor/object id and URL. Required when `FEDERATION_ENABLED=true`; validated
  at boot (https scheme, host, no path/trailing slash). The **domain** in `acct:` handles
  is derived from it (`PUBLIC_BASE_URL` host).
- `FEDERATION_ENABLED` (default `false`) — master gate. When false, all federation routes
  return 404 (same "only exposed when configured" pattern as the live ingest hooks) and no
  keypairs are generated. Zero cost when off.

Both go in `internal/config`, `.env.example`, `docker-compose.yml` passthrough, and
`README`/`AGENT.md` in the slice that first reads them (Slice 1).

Object/actor id scheme (stable, never reused):
- Account actor:   `${PUBLIC_BASE_URL}/accounts/{username}`
- Channel actor:   `${PUBLIC_BASE_URL}/video-channels/{handle}`
- Video object:    `${PUBLIC_BASE_URL}/videos/{uuid}` (AP id and `url`; the same URL RSS,
  the sitemap and oEmbed advertise and the frontend routes. The legacy
  `/videos/watch/{uuid}` form we used to mint is still ACCEPTED inbound so replies
  federated under it keep resolving — it is never emitted again.)
- Comment (Note):  `${PUBLIC_BASE_URL}/comments/{uuid}`
- Activities:      `${actor}/activities/{uuid}` (Create/Announce/Follow/…)
- Per-actor collections: `${actor}/{inbox,outbox,followers,following}`
- Shared inbox:    `${PUBLIC_BASE_URL}/inbox`

These are ActivityPub JSON-LD ids and live at the **root**, NOT under `/api/v1` (that
namespace is the REST contract in `api/openapi.yaml`). See §5 for the OpenAPI drift-guard
handling.

---

## 2. Actor model (accounts + channels)

Both `users` (Person) and `channels` (Group, PeerTube models channels as `Group`) are
federated actors. Decision: **store the actor keypairs in dedicated 1:1 side tables**
(`account_actor_keys`, `channel_actor_keys`) keyed by `user_id`/`channel_id`, rather than
columns on `users`/`channels` or a shared polymorphic `actors` table.
- Rationale (revised in Slice 2a): adding columns to `users`/`channels` makes several
  existing queries' `RETURNING`/`SELECT` lists diverge from the shared sqlc model, so sqlc
  stops reusing `User`/`Channel` and emits new `*Row` types — churning every service
  Repository interface that references those models. Dedicated tables keep the core models
  untouched (zero ripple), give proper `ON DELETE CASCADE` cleanup, and cleanly separate
  federation-only, lazily-minted data from core account/channel data.

Each side table (migration 0035, Slice 2a):
- `{user,channel}_id UUID PRIMARY KEY REFERENCES … ON DELETE CASCADE`
- `public_key_pem  TEXT NOT NULL` — PEM SPKI public key (safe to serve).
- `private_key_pem TEXT NOT NULL` — private key at rest (secret; see §3).
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` — when the keypair was minted.
A row is inserted lazily on first federation use (absent row = not yet minted).
Inbox/outbox/followers/following/actor URLs are **derived** from `PUBLIC_BASE_URL` +
username/handle at serialization time — not stored (no drift). Remote actors still live in a
separate `remote_actors` table (Slice 4).

Remote actors (Slice 4): `remote_actors` table — `id UUID`, `actor_url TEXT UNIQUE`,
`type`, `preferred_username`, `domain`, `inbox_url`, `shared_inbox_url NULL`,
`public_key_pem`, `followers_url`, fetched-at/refreshed-at. Remote follows and inbound
objects reference it.

---

## 3. Key handling (resolves security.md:31 "no plaintext private keys at rest without documented KMS")

- Algorithm: RSA-2048 (broad fediverse compatibility; PeerTube/Mastodon interoperate on RSA
  + RSA-SHA256 HTTP signatures). Generated with `crypto/rand`.
- Generation: **lazily on demand** when an actor is first federated (first actor-doc fetch or
  first outbound activity) and only when `FEDERATION_ENABLED`; stored once. Never regenerated
  silently (rotation is an explicit, later, logged operation — tracked with the JWT
  key-rotation item in P15).
- Public key: served in the actor document `publicKey.publicKeyPem`. Not secret.
- **Private key at rest — decision:** stored in the `private_key_pem` column. To satisfy
  security.md, the at-rest protection is **envelope encryption with a config-provided KEK**:
  `FEDERATION_KEY_KEK` (32-byte base64; AES-256-GCM via a small `internal/secretbox` helper).
  When the KEK is set, `private_key_pem` holds `enc:<base64 nonce||ciphertext>`; when unset
  (dev only), it holds the raw PEM and the api logs a loud boot WARNing (same shape as the
  other dev-only knobs). Production boot **requires** `FEDERATION_KEY_KEK` when
  `FEDERATION_ENABLED` (config validation). This is the "documented KMS/compatibility plan":
  a real KMS/HSM can later replace the local KEK behind the same encrypt/decrypt seam.
- Private keys are NEVER logged, traced, labelled, returned by any endpoint, or included in
  any read model / actor document. Covered by the existing `TestNoSensitiveLogKeys` guard;
  add `private_key`/`kek` to its banned-key list in the key-minting slice.

---

## 4. Discovery documents (keypair-free)

- **NodeInfo**: `/.well-known/nodeinfo` → links to `/nodeinfo/2.1`; `/nodeinfo/2.1` →
  `{version:"2.1", software:{name:"vidra", version}, protocols:["activitypub"],
  services:{inbound:[],outbound:[]}, openRegistrations: cfg.RegistrationEnabled,
  usage:{users:{total: CountUsers}, localPosts: CountVideos, localComments: CountComments}}`.
  Reuses `version.Version` + `cfg.RegistrationEnabled` (already in `handleInstance`).
  Needs `CountVideos`/`CountComments` sqlc queries (`CountUsers` exists). Available even when
  `FEDERATION_ENABLED=false`? No — NodeInfo advertises federation, so gate it on
  `FEDERATION_ENABLED` too (404 when off).
- **WebFinger**: `GET /.well-known/webfinger?resource=acct:{name}@{domain}` → JRD:
  `{subject, links:[{rel:"self", type:"application/activity+json", href: actorURL}]}`.
  `{name}` resolves against `users.username` first, then `channels.handle`; `{domain}` must
  equal the `PUBLIC_BASE_URL` host (else 404 — we only WebFinger our own actors). Unknown
  name → 404. No keys needed.

---

## 5. HTTP surface, content negotiation, and the OpenAPI drift guard

Federation endpoints serve/consume JSON-LD, not the REST contract. They must NOT bloat
`api/openapi.yaml`, and the route↔spec drift guard (`TestOpenAPIContract`) must stay green.
- Mount them on the root Echo instance (outside the `/api/v1` group) behind a
  `FEDERATION_ENABLED` guard, wired via `WithFederation(...)` like other optional subsystems.
- **Exclude** them from `TestOpenAPIContract` the same way the dev mail-capture endpoint is
  excluded (`fullRouteOptions` omits the federation option), and document the contract HERE
  instead. Add a test asserting the federation routes are ABSENT when `FEDERATION_ENABLED`
  is false (prod-safe by default), mirroring the dev-endpoint test.
- Content negotiation on actor/object endpoints: serve AP JSON
  (`Content-Type: application/activity+json`) only when the `Accept` header includes
  `application/activity+json` or `application/ld+json`; otherwise 406 (the human-facing HTML
  profile is `vidra-user`'s concern — no HTML here). WebFinger/NodeInfo always return their
  JRD/JSON types.
- All remote fetches (actor resolution, media, inbox delivery targets) go through the
  existing `internal/urlsafety` SSRF `Guard` (secure default) — never a bare `http.Client`.

---

## 6. Objects and activities (mapping to existing tables)

- `users` → `Person`; `channels` → `Group` with `attributedTo` the owner account.
- `videos` → `Video` (name, content/description, duration, `url` media links to the
  original/HLS, `attributedTo` channel, `sensitive` from privacy, published). Only
  public+published videos federate; unlisted/private never leave.
- `comments` → `Note` (`inReplyTo` the video or parent note, `attributedTo` account).
- Ratings → `Like`/`Dislike` (PeerTube uses `Dislike`; keep behind a compat note).
- Channel publishing a video → `Announce` to the channel's followers.
- Follow: remote `Follow` of a local channel → auto-`Accept` (channels are open); local
  `Follow` of a remote channel → send `Follow`, await `Accept`. Reuses the existing
  `channel_follows` table extended with a nullable `remote_actor_id` + `state`
  (pending/accepted) for remote edges.
- `Update`/`Delete` propagate edits/removals; inbound deletes are validated against the
  signing actor's authority over the object.

---

## 7. Signatures and inbound safety (Slice 3+)

- Outbound: sign every delivery with HTTP Signatures (RSA-SHA256) over
  `(request-target) host date digest`; `Digest: SHA-256=…` of the body; `Date` within skew.
- Inbound: verify the signature against the sending actor's fetched public key; reject on
  bad/missing signature, stale date, or digest mismatch. Fetch the actor via the SSRF guard.
- Every remote payload is size-bounded (reuse the import byte-cap approach), JSON-LD parsed
  defensively, and de-duplicated by activity `id` (idempotent inbox). Fuzz the AP parser
  (P15 "fuzz tests for ActivityPub parsing").

---

## 8. Delivery queue (Slice 5)

Outbound activities enqueue to a `federation_deliveries` table (target inbox, payload,
attempts, next_attempt_at, state) drained by a worker with exponential backoff and a
dead-letter state after N attempts. Followers-collection delivery fans out to unique inbox
URLs (prefer sharedInbox). Observability: per-delivery span/log with the activity id +
target host, never the payload of private objects.

---

## 9. Ordered implementation slices (each = one loop, each ships tests + docs)

1. **Config + NodeInfo** — `PUBLIC_BASE_URL` + `FEDERATION_ENABLED` config (+ validation,
   `.env.example`, compose, docs); `CountVideos`/`CountComments` queries; `/.well-known/nodeinfo`
   + `/nodeinfo/2.1` behind the gate; absent-when-disabled test. (Keypair-free.)
   → ticks fix_plan P10 partial (NodeInfo) — note it's not a numbered P10 line; record under P10 notes.
2. **Actor identity + WebFinger** — key columns migration (both entities) + `internal/secretbox`
   (KEK) + lazy keypair minting; `internal/federation` with actor serialization; root routes
   `/accounts/:h`, `/video-channels/:h` (AP JSON, content-negotiated) + WebFinger; banned-log-key
   update. → P10 "local actor model (accounts)", "(channels)", "WebFinger", "actor endpoints".
3. **HTTP signatures** — sign/verify helpers over the SSRF-guarded client; actor fetch+cache
   (`remote_actors`). → P10 "HTTP signatures", "JSON-LD signature strategy/compat plan".
4. **Inbox + Follow/Accept** — shared+per-actor inbox, signature-verified, idempotent;
   remote follow of a local channel → Accept; `channel_follows` remote columns. → P10 "inbox",
   "follow remote…", (partial) "receive remote video activity".
5. **Outbox + Create/Announce + delivery queue** — outbox collections; publish→`Create`+`Announce`;
   `federation_deliveries` worker with retry/dead-letter. → P10 "outbox", "announce video",
   "federation queue/retry/dead-letter".
6. **Federated comments, updates/deletes, remote media cache** → the remaining P10 objects.
7. **Contract + fuzz tests** — golden-fixture contract tests against real Mastodon/PeerTube
   sample payloads; AP-parser fuzz. → P10 "federation contract tests", P15 "AP parsing fuzz".

Frontend (vidra-user) federation UI (its P11) consumes read-only surfaces first: it can show
remote-follow state and federated origin once Slices 2/4 land — do not build frontend
federation UI before the backend contract for that surface exists.

## 10. ATProto (P10.2) — deferred

Independent of ActivityPub and enabled by its own config. Requires a posting/syndication
strategy spec before any code (per fix_plan). Not started; no shared code with §1–9 beyond
the "enable independently" rule.

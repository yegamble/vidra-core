# Vidra Core

The Go backend for **Vidra** — a clean-room, PeerTube-inspired federated video
platform. This repository (`vidra-core`) exposes the Vidra HTTP API. The Next.js
frontend lives in a separate `vidra-user` repository and consumes this API.

> Status: early bootstrap. The HTTP service, configuration, health/readiness
> probes, database/Redis wiring, migrations, and CI are in place. Product
> features are tracked in `.ralph/fix_plan.md` and the parity ledgers under
> `.ralph/specs/`.

## Quick start

```bash
cp .env.example .env
make up        # postgres + redis + migrations + api via Docker Compose
```

Then:

```bash
curl localhost:8080/healthz          # liveness
curl localhost:8080/readyz           # readiness (postgres + redis)
curl localhost:8080/version          # build version / commit / date
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
curl localhost:8080/api/v1/instance  # public about/config (name, software, registration_enabled)
```

Registration can be closed per-instance with `REGISTRATION_ENABLED=false`: signup then
returns `403` and `GET /api/v1/instance` reports `registration_enabled: false` so the
frontend can hide the form. The instance endpoint also surfaces optional about/legal
metadata — `description`, `terms_url`, `privacy_url`, `contact_email` (from the matching
`INSTANCE_*` env vars; empty when unset) — for the frontend's footer/about pages.

**Runtime-mutable instance settings.** A defined subset of instance settings is a DB
overlay on top of config: an admin edits them live via `GET`/`PATCH
/api/v1/admin/instance-settings` and they take effect without a restart. The mutable
subset is `instance_name`, `instance_description`, `terms_url`, `privacy_url`,
`contact_email`, `registration_enabled`, `registration_require_approval`,
`quarantine_new_uploads`, and the feature toggles `uploads_enabled`, `imports_enabled`,
`live_enabled`, `comments_enabled` (all default true, from `FEATURE_*_ENABLED`). The
matching env vars are the boot-time DEFAULTS; a stored override wins. When a toggle is
off, its endpoint returns `403 feature_disabled` (new upload sessions + direct upload,
URL import, live-stream create, comment create). Boot-time-only settings — the database
DSN, the KEKs, the JWT secret, the storage backend — deliberately STAY config-only
(unsafe to hot-swap and/or secret) and are never represented in the overlay table.

All non-2xx responses share one envelope: `{"error":{"code","message","request_id"}}`
(see `api/openapi.yaml` → `ErrorResponse`). The readiness probe returns its own
`ReadinessResponse` on 503. `make build` injects version/commit/date into `/version`
via `-ldflags`.

Request validation: handlers decode+validate input via `bindAndValidate`. Malformed
bodies get `400 bad_request`; failed validation gets `422 unprocessable_entity` with a
`fields` array (`{field, message}`) so forms can highlight the offending inputs.

Auth: `POST /api/v1/auth/register` and `POST /api/v1/auth/login` create an account /
verify credentials and return an HS256 JWT access token plus a rotating refresh token
(`{token, refresh_token, token_type, expires_in, user}`). Passwords are bcrypt-hashed;
the first account on a fresh instance is granted the `admin` role. Login reports
unknown-account and wrong-password identically (`401`) to prevent enumeration. Configure
signing via `JWT_SECRET` (required in production), `JWT_ISSUER`, `JWT_AUDIENCE`,
`JWT_ACCESS_TTL`, `JWT_REFRESH_TTL`.

Sessions: `POST /api/v1/auth/refresh` exchanges a refresh token for a new pair and
revokes the old one (rotation); reusing an already-rotated token is treated as
compromise and revokes all of that user's sessions. `POST /api/v1/auth/logout` revokes
the presented refresh token (idempotent `204`); `POST /api/v1/auth/logout-all`
(bearer-authenticated) signs the account out everywhere. Refresh tokens are opaque
256-bit values; only their SHA-256 hash is stored in the `sessions` table.

Cookie-mode sessions (browser clients): register/login/refresh accept
`{"cookie_mode": true}` — or detect an existing `vidra_refresh` cookie — and then carry
the rotating refresh token in an httpOnly `vidra_refresh` cookie instead of the JSON
body (which omits `refresh_token`). The cookie is scoped to `Path=/api/v1/auth`,
`SameSite=Lax`, `Max-Age` = the refresh TTL, and `Secure` when the instance is https
(derived from `PUBLIC_BASE_URL`; always on in production). `refresh`/`logout` fall back
to the cookie when the body omits the token; logout/logout-all clear it. CORS sends
`Access-Control-Allow-Credentials: true` for the explicit `CORS_ALLOWED_ORIGINS`
allow-list (never combined with a wildcard origin).

OAuth/OIDC login: set `OAUTH_PROVIDERS` (comma list; per provider
`OAUTH_<NAME>_ISSUER`/`_CLIENT_ID`/`_CLIENT_SECRET`, optional `_SCOPES`;
`PUBLIC_BASE_URL` required) and the instance document advertises the names in
`oauth_providers`. The browser navigates (top-level) to
`GET /api/v1/auth/oauth/{provider}?return_to=/path` → 302 to the provider with
state + nonce + PKCE (sealed in a signed, httpOnly, 10-minute
`vidra_oauth_state` cookie); the provider redirects to
`GET /api/v1/auth/oauth/{provider}/callback`, which verifies state/nonce and
the id_token (JWKS via OIDC discovery), then logs in — a known identity signs
its account in, a new identity with a provider-verified email matching an
existing account links to it, anything else creates an account (username
derived + deduplicated, `email_verified` inherited). Sessions are always
cookie-mode (the SPA then calls `/auth/refresh` with credentials); the
callback 302s to the validated same-origin `return_to`, or with
`?oauth_error=<code>` on user-actionable failures. The redirect URI sent to
the provider is always derived server-side from `PUBLIC_BASE_URL`, never from
request parameters. `GET /api/v1/me/oauth-identities` lists the caller's
linked identities; `DELETE /api/v1/me/oauth-identities/{provider}` unlinks one
(`422` when it is a passwordless account's last sign-in method).

Two-factor authentication (TOTP, RFC 6238 SHA1/6-digit/30s ±1 step):
`POST /api/v1/auth/mfa/totp` (bearer) starts enrollment and returns the base32
secret + `otpauth://` URI exactly once; `POST /api/v1/auth/mfa/totp/verify
{code}` confirms with the first valid authenticator code, enables MFA, and
returns 10 single-use recovery codes exactly once (stored SHA-256-hashed).
`GET /api/v1/auth/mfa` reports `{enabled, recovery_codes_remaining}`;
`DELETE /api/v1/auth/mfa/totp {password}` disables (password re-auth required).
With MFA enabled, a credential-valid login returns `{mfa_required: true,
mfa_token}` (short-lived 5-minute single-purpose token) with no session tokens;
`POST /api/v1/auth/mfa/challenge {mfa_token, code}` — a TOTP code or one
recovery code, consumed on use — completes the login with the full auth
response (cookie mode supported, rate-limited like login). TOTP secrets are
sealed at rest with `MFA_KEY_KEK` (falls back to `FEDERATION_KEY_KEK`; raw in
dev with a boot warning); the issuer label comes from `TOTP_ISSUER` (default
`INSTANCE_NAME`). OAuth/OIDC logins are not TOTP-gated — the IdP owns that
factor.

Authorization: routes are gated by `requireAuth` (valid bearer token) and, where
role-restricted, `requireRole(...)` off the JWT's `role` claim — an authenticated
principal lacking an allowed role gets `403`.

Channels: a channel is a publishing identity owned by a user. `POST /api/v1/channels`
(auth) creates one (`handle` 3–30 chars `[A-Za-z0-9_]`, unique case-insensitively →
`409`); `GET /api/v1/me/channels` (auth) lists the caller's channels;
`GET /api/v1/channels/{handle}` is the public channel page lookup (`404` when absent).
`PATCH /api/v1/channels/{handle}` (owner-only, partial: `display_name`/`description`)
and `DELETE /api/v1/channels/{handle}` (owner-only) manage it — a non-owner gets `403`.
The handle is immutable after creation. `POST`/`DELETE /api/v1/channels/{handle}/follow`
(auth, idempotent `204`) follow/unfollow a channel; every channel view carries a
`follower_count`.

Avatars and banners: accounts and channels each take an avatar and a banner image.
`POST`/`DELETE /api/v1/me/avatar` and `/api/v1/me/banner` (auth) manage the caller's
own; `POST`/`DELETE /api/v1/channels/{handle}/avatar` and `/banner` are owner-only
(non-owner/unknown handle → `404`). Uploads are a `multipart/form-data` `file` part,
JPEG/PNG/WebP by extension (else `415`), bounded by the global `HTTP_BODY_LIMIT`
(same cap as the custom video thumbnail); re-upload replaces the image and delete
removes both the record and the stored object (`404` when none is set). Serving is
public: `GET /api/v1/users/{id}/avatar` | `/banner` and
`GET /api/v1/channels/{handle}/avatar` | `/banner` return the image with the
Content-Type derived from the upload's extension, `404` when unset. The `/auth/me`
view and every channel view carry `has_avatar`/`has_banner` flags. Blobs live at
deterministic PeerTube-style keys (`avatars/users/<id><ext>`,
`banners/channels/<id><ext>`, …) recorded in the `user_images`/`channel_images`
tables.

Donation addresses (simple, NON-CUSTODIAL crypto tips): a creator lists public
wallet addresses on their account or a channel they own; viewers see them and send
funds peer-to-peer, entirely outside Vidra. Vidra never holds funds, balances, or
private keys and processes no payments, payouts, escrow, or taxes. `POST`/`GET`/
`DELETE /api/v1/me/donation-addresses[/{id}]` (auth) manage the caller's own
addresses (`network` ∈ `bitcoin|ethereum|litecoin|monero`, each validated against
its address format → `422`; an optional `channel_id` scopes it to a channel the
caller owns → `403`/`404`; duplicates → `409`). `GET /api/v1/users/{id}/donation-addresses`
(account-level only) and `GET /api/v1/channels/{handle}/donation-addresses` are the
public reads, exposing each address's `verified` flag but never the internal
challenge state. Ownership can be proven where a practical message-signing standard
exists: `POST /api/v1/me/donation-addresses/{id}/challenge` returns a nonce-bearing
message (10-min expiry) and `POST .../verify` checks the signature — **ethereum**
(EIP-191 `personal_sign`, verified with the minimal `decred/dcrd` secp256k1 library
+ keccak256, no go-ethereum dependency) is supported; **bitcoin** (BIP-137) is
intentionally deferred and **monero/litecoin** have no practical standard, so those
networks return `501` and stay unverified-only. Backed by the `donation_addresses`
table (migration 0063).

Videos: `POST /api/v1/channels/{handle}/videos` (owner-only) creates a draft video
(`title`, optional `description`/`privacy`; starts `state: draft`, `privacy` defaults
`private`). `GET /api/v1/videos/{id}` returns public/unlisted videos to anyone with the
id; a `private` video is returned only to its owner (bearer token) and is `404` to
everyone else so its existence is not leaked. `PATCH`/`DELETE /api/v1/videos/{id}`
(owner-only; non-owner/unknown → `404`) edit/remove it. `GET /api/v1/channels/{handle}/videos`
lists a channel's videos — all of them for the owner, public-only for everyone else.
`GET /api/v1/videos` is the public cross-channel feed, ordered by `?sort`
(`recent` default, `popular` = most all-time views, `trending` = views decayed by
age; unknown → recent) and paginated with `?limit` 1–100 default 20 and `?offset`.
Each feed item carries its `views` and `has_thumbnail` so cards have what they
need (the query LEFT JOINs `video_view_counts` and checks for a stored poster).
`GET /api/v1/videos/search?q=` fuzzy-searches
public titles (pg_trgm, ranked by similarity then recency; same pagination). It
also accepts the same facet filters as the feed — `&tag=`, `&category=`,
`&language=` (unknown taxonomy values `422`; any active filter excludes remote
results). Search results and the channel video lists
(`GET /api/v1/channels/{handle}/videos`) carry the same `views`/`has_thumbnail`
card data as the feed, so every video grid is consistent. The video **detail**
response (`GET /api/v1/videos/{id}`) also carries `channel_handle` +
`channel_display_name` for the related-rail.
`POST /api/v1/videos/{id}/file` (owner-only, `multipart/form-data` with a single
`file` part) stores the original through the storage backend, then finalises the
video: `draft → processing → published` (or `failed` if a configured media probe
rejects it). Re-uploading replaces the prior original, and non-owner/unknown → `404`.
**Malware scanning** (`MALWARE_SCAN_ENABLED=true`, streams the original to the
clamd at `CLAMAV_ADDR` — the compose `scan` profile ships one): an INFECTED file
always fails. `MALWARE_SCAN_MODE` decides the fallback on a scan *error*:
`fail-closed` (default — not published), `fail-open` (published anyway, logged
loudly), or `quarantine` (parked in the moderator review queue).
The file extension must be an accepted video container (else `415`) and the body must
be within `UPLOAD_MAX_SIZE` (else `413`; this route is exempt from the small
`HTTP_BODY_LIMIT` that guards the JSON API). The stored file is tracked in
`video_files`. **Per-user storage quotas**: a user's usage is the summed
size of their `video_files` rows (originals, renditions, thumbnails) across the
videos owned via their channels, aggregated live. The effective quota is the
per-user override when an admin set one, else `INSTANCE_DEFAULT_QUOTA_BYTES`
(0/unset = unlimited); a direct or resumable upload that would not fit is
rejected with `422 quota_exceeded` before storing, and an async URL import that
would not fit fails its job with a quota reason (the fetch is also hard-capped
while streaming, so a lying/absent `Content-Length` can't bypass it). `GET
/api/v1/me/quota` (auth) returns `{used_bytes, quota_bytes}` (`quota_bytes`
null = unlimited), and admins manage overrides via `PATCH
/api/v1/admin/users/{id}` `storage_quota_bytes` (null resets to the instance
default, 0 = unlimited).

**Chunked/resumable upload** (P6.1): `POST /api/v1/videos/{id}/upload-session`
(owner; body `{size, filename}` validated up front — extension `415`, size vs
`UPLOAD_MAX_SIZE` `413`, quota `422`) returns `{upload_id, chunk_size (8 MiB),
total_chunks, expires_at}`; the client PUTs each fixed-size chunk to `PUT
/api/v1/uploads/{upload_id}/chunks/{n}` (raw body, idempotent re-PUT),
reads progress from `GET /api/v1/uploads/{upload_id}` (the received-chunk ledger
— the resume contract, no Redis needed), then `POST
/api/v1/uploads/{upload_id}/complete` assembles the chunks in order through the
same `AttachOriginal → Process` pipeline as a direct upload. `DELETE
/api/v1/uploads/{upload_id}` cancels. Chunk bytes live in the storage backend at
`uploads/<session>/<n>` (so S3 works too); sessions expire after 24h and a
background sweeper deletes expired/cancelled sessions' chunks (the failed-upload
cleanup). **Async URL import** (P2.2): `POST /api/v1/videos/{id}/import` (owner,
body `{url}`) now enqueues an `import_jobs` row and returns `202 {import_job}`
instead of blocking on the fetch; a background worker performs the SSRF-guarded
fetch and runs the bytes through the same pipeline (retry/backoff, dead-letter
after 5). Poll `GET /api/v1/videos/{id}/import` for the job status (`state`
pending/running/done/failed, plus a safe `error` reason on failure). A single
import runs per video at a time; re-posting while one is in flight returns it.

Finalisation runs through an injected `Prober` seam: at startup the server uses
the FFprobe-backed prober when `ffprobe` is on `PATH` (it is in the Docker image),
extracting technical metadata (duration, width, height) that the detail endpoint
exposes and persisting it to `video_metadata`; a probe error marks the video
`failed`. Where `ffprobe` is absent the original is trusted and published unprobed
(no metadata). The public discovery surfaces — `GET /api/v1/videos`,
`/videos/search`, and the public view of a channel's videos — return only
`published` videos. When transcoding is enabled (below), the publish transition
also enqueues an HLS transcode job, best-effort — it never blocks publishing.

`GET /api/v1/videos/{id}/original` streams the stored original bytes for direct
playback — same visibility as the detail endpoint (private → owner only, else
`404`; a video with no stored original is `404`). It honours HTTP `Range`
requests (`206 Partial Content`) so a `<video>` element can seek; the local
backend serves via `http.ServeContent`.

**HLS transcoding** (`TRANSCODING_ENABLED=true`, default off; needs `ffmpeg` +
`ffprobe` on `PATH` — both are in the Docker image): publishing a video enqueues
a durable job in `transcode_jobs` (mirroring the federation delivery queue) and
an in-process worker produces an H.264/AAC HLS ladder — rungs from
1080p/720p/480p/360p, capped at the source height (never upscaled; a smaller
source gets a single rung at its own size) — stored under
`streaming-playlists/<video_id>/` with ~4s MPEG-TS segments. Failures retry
with exponential backoff and dead-letter after 5 attempts. Once the
`streaming_playlists` row is `ready`, the video detail carries `hls_url` +
`renditions [{height,width}]`, and playback is served (same visibility rules as
`/original`) by `GET /api/v1/videos/{id}/hls/master.m3u8`
(`application/vnd.apple.mpegurl`) and
`GET /api/v1/videos/{id}/hls/{rendition}/{file}` (variant playlists + `video/mp2t`
segments; all playlist URIs are relative so proxying works). Progressive
playback of the original remains available regardless.

**VP9 alternate** (`TRANSCODING_VP9_ENABLED=true`, needs transcoding on): the
transcoder additionally emits a progressive VP9/WebM file at the top ladder rung
(`libvpx-vp9`/`libopus`, best-effort — a VP9 failure never fails the H.264 HLS).
It is stored as a `webm` file and surfaced by `GET /api/v1/videos/{id}/download`
(kind `webm`) and served with Range/206 at `GET /api/v1/videos/{id}/webm`. VP9 is
a progressive **download alternate** rather than an HLS variant on purpose
(HLS+VP9 needs fMP4/CMAF with patchy client support). **AV1** is deferred:
`TRANSCODING_AV1_ENABLED=true` fails config validation with a defer note.

During finalisation an FFmpeg-backed thumbnailer (when `ffmpeg` is on `PATH`)
extracts a poster frame and stores it as a `thumbnail` file;
`GET /api/v1/videos/{id}/thumbnail` serves the JPEG (same visibility as the
detail endpoint), and the detail response carries a `has_thumbnail` flag.
Thumbnail generation is best-effort — a failure never blocks publishing. The
**preview** is this thumbnail (there is no separate animated preview).

**Storyboard** (seek-preview sprite sheet; needs `ffmpeg` + `ffprobe`): a
sprite sheet of up to 100 160×90 tiles plus a WebVTT sprite map are generated
best-effort during finalisation and stored at `storyboards/<id>.{jpg,vtt}`.
`GET /api/v1/videos/{id}/storyboard.jpg` and `…/storyboard.vtt` serve them
(same visibility as the detail endpoint), and the detail carries a
`has_storyboard` flag.

**Media garbage collection**: `POST /api/v1/admin/media/gc` (admin) lists stored
objects under the known media prefixes and deletes those with no database
reference. It defaults to a dry run (`{"dry_run":false}` deletes); it never
lists or touches an unknown prefix, and it is audited. A daily in-process worker
runs the same sweep.

**Captions** (P13). A video owner uploads WebVTT tracks with
`POST /api/v1/videos/{id}/captions` (multipart `file` + `language` [+ `label`]);
anyone lists them (`GET …/captions`) and downloads a track by language
(`GET …/captions/{lang}`, `text/vtt`). **Auto-captions (Whisper)**
(`WHISPER_ENABLED=true`, default off; requires `WHISPER_ENDPOINT`): the owner
requests an auto-generated track with `POST /api/v1/videos/{id}/captions/auto`
(body `{language?}`, default `WHISPER_DEFAULT_LANGUAGE`). It enqueues a
`caption_jobs` row and returns `202 {caption_job}` (`503` when disabled, `409`
while a job is already pending/running, `422` for a bad language tag). A
background worker extracts the audio (`ffmpeg` → 16 kHz mono WAV), POSTs it to
`WHISPER_ENDPOINT` (whisper.cpp `/inference`-compatible, trusted operator config
— not SSRF-guarded), renders the response to WebVTT, and stores it through the
same replace-by-language path a manual upload uses — then sends the owner a
`caption_ready` notification (honoring prefs). Poll
`GET /api/v1/videos/{id}/captions/auto` for the job status (retry/backoff,
dead-letter after 5; a safe `error` reason on failure). Run the bundled server
with the `captions` compose profile.

**ATProto / Bluesky cross-posting** (P10.2, a Vidra extension; see
`.ralph/specs/atproto.md`). Gated by `ATPROTO_ENABLED` (default off) and
INDEPENDENT of ActivityPub — an instance may enable either, both, or neither.
v1 is OUTBOUND ONLY: a creator links a Bluesky account with
`PUT /api/v1/me/atproto` (`{handle, app_password, pds_url?, auto_post?}` — an
*app* password, never the main password). The backend verifies the credentials
via `com.atproto.server.createSession` on the (https, public, SSRF-guarded) PDS,
resolves the DID, and stores the app password SEALED at rest with
`ATPROTO_KEY_KEK` (falls back to `FEDERATION_KEY_KEK`; required in production when
enabled, raw in dev with a boot warning). `GET /api/v1/me/atproto` returns the
status (handle/DID/auto-post/last-post — never the password); `DELETE` unlinks.
When a PUBLIC video whose owner has `auto_post` is published, an
`atproto_posts` row is enqueued (via the video publish-hook); a background worker
creates a fresh session and posts an `app.bsky.feed.post` with the title and an
external-link embed to the public watch URL (thumbnail uploaded when ≤1 MiB),
recording the post URI. One post per video (`video_id` UNIQUE), 429 backoff,
dead-letter after 6. The endpoints answer `503` while the extension is disabled.

**Live streaming** (P12). A channel owner creates a live stream with
`POST /api/v1/channels/{handle}/live` (`{title, description?, privacy?,
permanent?, replay_enabled?}`) and receives a stream key ONCE plus the RTMP URL;
only the key's SHA-256 hash is stored. `GET/PATCH/DELETE /api/v1/live/{id}` read/
edit/delete it (`PATCH` edits title/description/privacy/permanent/replay_enabled),
`POST /api/v1/live/{id}/key` rotates the key, and `GET /api/v1/channels/{handle}/live`
lists a channel's streams (owner only; keys are never returned).

The RTMP boundary is the `media` compose profile: an nginx-rtmp server ingests
RTMP, packages HLS, records sessions, and drives the api via media-server-facing
hooks `POST /api/v1/live/ingest/{start,stop}` (authenticated by the
`X-Ingest-Secret: $LIVE_INGEST_SECRET` header — 404 when the secret is unset).
On publish the api returns a 302 that renames the RTMP session to the **stream
ID**, so the raw key never lands in a file path/URL; on-publish flips the stream
`live`, on-publish-done returns it to `offline` (permanent) or `ended` (one-shot).
While live, the api serves HLS from `LIVE_HLS_ROOT` (the shared volume) keyed by
ID at `GET /api/v1/live/{id}/hls/master.m3u8` and `…/hls/{file}` — same privacy
gating as VOD HLS, and only while `state=live`; the live view carries `hls_url`
then.

**Replay → VOD** (`replay_enabled`): the media server records each session; on
ingest-stop the api best-effort creates a draft video on the stream's channel
(titled `"<stream title> (replay)"`, privacy inherited) and runs the recording
through the normal pipeline (scan/probe/thumbnail/transcode), so the replay
appears as an ordinary published video. It is fully best-effort — a replay
failure never breaks the stop hook — and each outcome is logged + audited
(`content.live.replay`). To run the whole live plane locally:

```bash
LIVE_INGEST_SECRET=$(openssl rand -hex 24) LIVE_HLS_ROOT=/live-hls \
  docker compose --profile core --profile media up --build
# publish to rtmp://localhost:1935/live/<stream-key>
```

`POST /api/v1/videos/{id}/view` records a view (same visibility as detail; only
published videos accrue views), de-duplicated per viewer per hour in Redis
(`SETNX` over the authenticated user id, else the client IP, hashed — raw
ids/IPs are never used as keys). It always returns `204`; the running `views`
total is exposed on the detail endpoint. (View counts live in a `video_view_counts`
side table; surfacing them on feed cards and a trending sort are later slices.)

Authenticated requests send `Authorization: Bearer <token>`. `GET /api/v1/auth/me`
(protected) returns the current account, reloaded from the database so it reflects
live role/verification state. A missing, malformed, invalid, or expired token yields
`401` without revealing which check failed; a deactivated account is treated as `401`.
`PATCH /api/v1/auth/me` updates the profile (`display_name`, `bio`; partial); identity
fields (username/email) are not editable there pending a re-verification flow.

Account lifecycle: `POST /api/v1/auth/me/deactivate` (`{password}`) reversibly
disables the account (an admin can re-enable). `DELETE /api/v1/auth/me`
(`{password}`) is the IRREVERSIBLE hard delete (product-decisions.md §1): owned
channels/videos are purged (federated `Delete` fan-out for previously-public
videos; media blobs removed best-effort, including the HLS ladder via the
storage backends' `DeletePrefix`), the account's comments become `"[deleted]"`
tombstones with reply threads preserved, per-user rows (ratings, saved videos,
history, playlists, follows, mutes, blocks, OAuth identities, TOTP, tokens,
notifications) are erased, all sessions are revoked, and the `users` row is
anonymised (`deleted-<8char>` username + email sentinel, cleared hash,
`deleted_at`) rather than removed so audit rows and DM history stay coherent.
Admins can hard-delete any account except their own via
`DELETE /api/v1/admin/users/{id}` (self-guard → `422`). Both variants are
audited (`auth.account.delete` / `admin.user.delete`).

Account export/import (P4): `POST /api/v1/me/export` queues a durable job (one
active per user → `409`) whose worker writes a JSON archive of the caller's
data — profile, channels, video metadata incl. taxonomy/tags, playlists,
comments, follows, saved videos, watch history, notification prefs; NEVER the
password hash or any token — to `exports/accounts/<user>/<id>.json` in the
media storage backend. `GET /api/v1/me/export` reports status;
`GET /api/v1/me/export/download` streams the archive while it lasts (7 days,
then a sweeper deletes it; expired → `410`). Media files are not bundled in
v1 — each video entry carries its original's download URL instead.
`POST /api/v1/me/import` accepts that same archive and re-creates the SAFE
subsets (profile fields, playlists matched by locally-present video ids,
follows of local channels, notification prefs); everything else is reported
per-section as skipped in the response summary.

Direct messaging: `POST /api/v1/conversations` starts (or idempotently returns)
the 1:1 conversation with a recipient; `GET /api/v1/me/conversations` is the
inbox (with per-conversation `unread_count`); `POST`/`GET /api/v1/conversations/{id}/messages`
send/list. A user block in either direction refuses messaging with `403`.

DM completeness (product-decisions.md §14): **attachments** — `POST /api/v1/conversations/{id}/attachments`
(multipart `file`, ≤25 MiB, image/video/audio/pdf, ClamAV fail-closed when
`MALWARE_SCAN_ENABLED`) returns an `attachment_id` to reference in a send
(`attachment_ids: []`, ≤4, own-uploaded); `GET /api/v1/attachments/{id}` serves
the bytes participant-gated; attachments are plaintext-only (encrypted
conversations `422`). **Link previews** — the first URL in a plaintext body is
fetched asynchronously through the SSRF guard (1 MiB, HTML-only OpenGraph;
`HTTP_IMPORT_ALLOW_PRIVATE_URLS` also relaxes this guard in dev) and joined onto
the message when ready; the fetch never blocks or fails the send. **Read
receipts** — `POST /api/v1/conversations/{id}/read` advances the caller's
watermark (idempotent); the thread exposes the peer's `peer_last_read_message_id`
and the inbox exposes unread counts; `GET`/`PATCH /api/v1/me/messaging-prefs`
toggles `read_receipts` (when off, the caller's watermark is hidden from peers).
**Per-message** — `DELETE /api/v1/messages/{id}` sender-only tombstones a message
(`[deleted]`); `POST /api/v1/messages/{id}/report` lets either participant report
it (snapshotting the body into the moderation queue). Typing presence is an
intentional difference (polling, no presence).

Encrypted messaging (E2EE, `.ralph/specs/e2ee.md`): pass `{"encrypted": true}`
at conversation creation for a ciphertext-only thread — the type is immutable
and distinct from the pair's plaintext conversation. ALL cryptography is
client-side (Olm); the backend is a key directory + envelope store and cannot
decrypt. Devices register PUBLIC keys via `POST/GET /api/v1/e2ee/devices` and
`DELETE /api/v1/e2ee/devices/{id}`; one-time prekeys are uploaded/counted via
`POST /api/v1/e2ee/devices/{id}/one-time-keys[/count]` and claimed atomically
(single-use, `FOR UPDATE SKIP LOCKED`) via `POST /api/v1/users/{id}/e2ee/claim`;
peer keys are readable via `GET /api/v1/users/{id}/e2ee/devices` — both peer
endpoints are participant-gated (404 without a shared conversation) and refused
across blocks. Sends carry one ≤64KiB opaque envelope per recipient device
(`{sender_device_id, envelopes[], expires_in_seconds?}`); the wrong body shape
for a conversation's type is `422` in both directions; reads return only the
caller's devices' envelopes, byte-identical to what was posted. Optional
disappearing messages (30s–90d) are filtered from reads at expiry and
hard-deleted by a background sweeper. One-time-key claims are audited with
counts only; ciphertext/keys never appear in logs.

Email delivery: password-reset and email-verification tokens are handed to a `Mailer`
adapter. By default nothing is sent (tokens are still generated and consumable). Set
`MAIL_ENABLED=true` plus `SMTP_HOST`, `SMTP_PORT` (default 587), `SMTP_FROM`, and
optional `SMTP_USERNAME`/`SMTP_PASSWORD` (AUTH PLAIN; the password is a secret — never
logged) to deliver plain-text mail over SMTP, with STARTTLS whenever the relay offers
it. The dev capture seam (`DEV_MAIL_CAPTURE_ENABLED`) wins over SMTP when both are on.

Request guards: bodies over `HTTP_BODY_LIMIT` (default `8M`) are rejected with `413`;
each request carries a `HTTP_REQUEST_TIMEOUT` (default `30s`) context deadline that
handlers and DB/Redis calls observe (a fired deadline renders as a `503`
`request_timeout`), with the server `WriteTimeout` as the hard backstop.

Rate limiting: the `/api` surface is rate limited per client IP with a Redis
fixed-window limiter (`RATE_LIMIT_REQUESTS` per `RATE_LIMIT_WINDOW`, default 120/min;
disable with `RATE_LIMIT_ENABLED=false`). Responses carry `X-RateLimit-Limit`,
`X-RateLimit-Remaining`, and `X-RateLimit-Reset`; over-budget requests get `429`
`rate_limited` with `Retry-After`. System probes (`/healthz`, `/readyz`, `/version`)
are exempt. If Redis is unreachable the limiter fails open (logs a warning) so a
Redis blip degrades protection, not availability. Rate limits are deploy-time
config only — there is no runtime mutation endpoint; the effective non-secret
values are surfaced read-only on `GET /api/v1/admin/system` (`rate_limits`).

Media storage goes through a small `internal/storage.Backend` interface
(Put/Open/Delete/Exists over forward-slash object keys). The default `local` backend
(`STORAGE_BACKEND=local`, `STORAGE_LOCAL_ROOT`) writes under a root directory with
path-traversal-safe key resolution. `STORAGE_BACKEND=s3` stores objects in any
S3-compatible store (MinIO, AWS S3, Backblaze B2, DigitalOcean Spaces) via the MinIO
Go SDK: set `STORAGE_S3_ENDPOINT` (host[:port], no scheme), `STORAGE_S3_BUCKET`,
`STORAGE_S3_ACCESS_KEY`/`STORAGE_S3_SECRET_KEY` (credentials — never logged), and
optionally `STORAGE_S3_REGION`, `STORAGE_S3_USE_SSL` (default true), and
`STORAGE_S3_FORCE_PATH_STYLE` (required by MinIO). The bucket is created at boot when
missing. Provider endpoint examples live in `.env.example`; for local dev the compose
`storage` profile runs MinIO (`docker compose --profile storage up -d minio`; the
api-against-minio env is documented at the top of `docker-compose.yml`). The S3
backend exposes no filesystem paths — ffprobe/ffmpeg/clamav read via the temp-file
download fallback, and HTTP Range/206 serving works through its seekable object
reader. IPFS lands later behind the same interface.

## Observability

Structured logging is always on (`slog`; `LOG_LEVEL`, `LOG_FORMAT=json|text`) with
one line per request carrying `request_id`/`correlation_id`. Two opt-in layers add
metrics and tracing at zero cost when off:

- **Metrics** (`METRICS_ENABLED=true`): Prometheus RED metrics at `GET /metrics`
  — request rate/errors/duration histograms labelled by method + Echo route
  template + status class (bounded cardinality; never ids/raw URLs), plus a
  `vidra_queue_depth{queue,state}` gauge. Unauthenticated root ops surface —
  network-scope it, and it is intentionally excluded from `api/openapi.yaml`.
- **Tracing** (`OTEL_ENABLED=true`): OTLP spans for HTTP, PostgreSQL queries,
  Redis commands, and outbound HTTP (federation/import/whisper) with inbound +
  outbound W3C `traceparent`. Run a local Jaeger with
  `docker compose --profile core --profile otel up` (collector at
  `otel-collector:4317`, UI at http://localhost:16686).
- **Admin ops endpoints**: `GET /api/v1/admin/system` (build/uptime/dependency
  health + effective non-secret `rate_limits`) and `GET /api/v1/admin/jobs`
  (per-queue depth + oldest-pending age + recent failures) back the admin
  dashboards.

Backups, restore, and production deploy notes: `docs/operations.md`.

## Migrating from PeerTube

A one-way import brings an existing **PeerTube** instance (its PostgreSQL DB +
media storage) into Vidra: accounts (bcrypt passwords kept working), channels,
videos + files/thumbnails/captions, threaded comments, playlists, tags, and
subscriptions. It is read-only on the source, idempotent, resumable, dry-runnable,
and audited.

- CLI: `cmd/peertube-import` (`--source-dsn`, `--source-storage`, `--conflict-policy`,
  `--dry-run`, `--resume`, `--force`).
- Admin API (server-configured source only — the browser never sends a DSN):
  `POST /api/v1/admin/peertube-import` (`dry_run`|`run`), `GET /api/v1/admin/peertube-import`,
  `GET /api/v1/admin/peertube-import/{id}`. Gated by `PEERTUBE_IMPORT_ENABLED` +
  `PEERTUBE_SOURCE_DATABASE_URL` (see `.env.example`; source creds are secrets).
- Supported PeerTube schema versions and what is imported vs. deferred (HLS/
  moderation/history are regenerated/reconciled afterwards): see the operator
  guide **`docs/peertube-migration.md`**.

## Local development (without Docker for the app)

```bash
cp .env.example .env
# bring up just the datastores:
docker compose --profile core up postgres redis
make migrate-up   # requires the `migrate` CLI
make run          # runs the API against local Postgres/Redis
```

## Developer commands

Run `make help` for the full list (fmt, vet, test, test-race, cover, build,
run, sqlc, sqlc-verify, migrate-up, up/down).

## Tech stack

Go · Echo · PostgreSQL (pg_trgm, uuid-ossp) · pgx · sqlc · Redis · Docker.

## API contract

`api/openapi.yaml` is the source of truth for the HTTP API and is consumed by the
`vidra-user` frontend. It is kept in lock-step with the code by a drift guard:
`make openapi-verify` (the `TestOpenAPIContract` test) fails if a route is added,
removed, or renamed without a matching spec edit, and the `openapi.yml` CI workflow
lints the spec and runs the same check on every change. Lint locally with
`make openapi-lint`.

## Project docs

- API contract: `api/openapi.yaml`
- Architecture: `.ralph/specs/architecture.md`
- Security: `.ralph/specs/security.md`
- Observability: `.ralph/specs/observability.md`
- Operations (backup/restore/deploy): `docs/operations.md`
- PeerTube migration guide: `docs/peertube-migration.md`
- Testing: `.ralph/specs/testing.md`
- PeerTube parity ledgers: `.ralph/specs/peertube-*.md`

## License

TBD.

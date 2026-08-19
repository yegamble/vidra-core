# Vidra Core feature reference

Detailed feature-by-feature reference; the README links here.

## Auth & accounts

### JWT auth

Auth: `POST /api/v1/auth/register` and `POST /api/v1/auth/login` create an account /
verify credentials and return an HS256 JWT access token plus a rotating refresh token
(`{token, refresh_token, token_type, expires_in, user}`). Passwords are bcrypt-hashed.
Accounts never gain the `admin` role by registering first: on a fresh instance boot
mints a one-time setup token (printed to the server log; only its hash is stored) and
every signup path answers `403 owner_claim_required` until the operator redeems it at
`POST /api/v1/setup/claim-owner`, which creates THE `admin` account. A restart while
the claim is outstanding re-mints the token and invalidates the previous one — even
once users exist — and `GET /api/v1/instance` reports the first-run state as
`owner_claim_pending`. `OWNER_CLAIM_TOKEN` pins the token to a fixed value for dev/test
harnesses (refused in production). Login reports unknown-account and wrong-password
identically (`401`) to prevent enumeration. Configure signing via `JWT_SECRET`
(required in production), `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_ACCESS_TTL`,
`JWT_REFRESH_TTL`.

### Sessions

Sessions: `POST /api/v1/auth/refresh` exchanges a refresh token for a new pair and
revokes the old one (rotation); reusing an already-rotated token is treated as
compromise and revokes all of that user's sessions. `POST /api/v1/auth/logout` revokes
the presented refresh token (idempotent `204`); `POST /api/v1/auth/logout-all`
(bearer-authenticated) signs the account out everywhere. Refresh tokens are opaque
256-bit values; only their SHA-256 hash is stored in the `sessions` table.

### Cookie-mode sessions

Cookie-mode sessions (browser clients): register/login/refresh accept
`{"cookie_mode": true}` — or detect an existing `vidra_refresh` cookie — and then carry
the rotating refresh token in an httpOnly `vidra_refresh` cookie instead of the JSON
body (which omits `refresh_token`). The cookie is scoped to `Path=/api/v1/auth`,
`SameSite=Lax`, `Max-Age` = the refresh TTL, and `Secure` when the instance is https
(derived from `PUBLIC_BASE_URL`; always on in production). `refresh`/`logout` fall back
to the cookie when the body omits the token; logout/logout-all clear it. CORS sends
`Access-Control-Allow-Credentials: true` for the explicit `CORS_ALLOWED_ORIGINS`
allow-list (never combined with a wildcard origin).

### OAuth / OIDC login

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

### Two-factor authentication (TOTP)

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

### Authorization

Authorization: routes are gated by `requireAuth` (valid bearer token) and, where
role-restricted, `requireRole(...)` off the JWT's `role` claim — an authenticated
principal lacking an allowed role gets `403`.

### Account (`/auth/me`)

Authenticated requests send `Authorization: Bearer <token>`. `GET /api/v1/auth/me`
(protected) returns the current account, reloaded from the database so it reflects
live role/verification state. A missing, malformed, invalid, or expired token yields
`401` without revealing which check failed; a deactivated account is treated as `401`.
`PATCH /api/v1/auth/me` updates the profile (`display_name`, `bio`; partial); identity
fields (username/email) are not editable there pending a re-verification flow.

### Account lifecycle

Account lifecycle: `POST /api/v1/auth/me/deactivate` (`{password}`) reversibly
disables the account (an admin can re-enable). `DELETE /api/v1/auth/me`
(`{password}`) is the IRREVERSIBLE hard delete: owned
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

### Account export & import

Account export/import: `POST /api/v1/me/export` queues a durable job (one
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

## Channels & social

### Channels

Channels: a channel is a publishing identity owned by a user. `POST /api/v1/channels`
(auth) creates one (`handle` 3–30 chars `[A-Za-z0-9_]`, unique case-insensitively →
`409`); `GET /api/v1/me/channels` (auth) lists the caller's channels;
`GET /api/v1/channels/{handle}` is the public channel page lookup (`404` when absent).
Auth on it is **optional**: an anonymous request gets the public projection, while a
signed-in caller additionally gets their own relationship with the channel —
`is_following` and, when following, their `notification_setting` — so the channel page
paints the Follow button and the bell in one request.
`PATCH /api/v1/channels/{handle}` (owner-only, partial: `display_name`/`description`)
and `DELETE /api/v1/channels/{handle}` (owner-only) manage it — a non-owner gets `403`.
The handle is immutable after creation. `POST`/`DELETE /api/v1/channels/{handle}/follow`
(auth, idempotent `204`) follow/unfollow a channel; every channel view carries a
`follower_count`. `GET /api/v1/me/subscriptions` (auth, paginated) lists the local
channels the caller follows — the "FOLLOWING" list — most recently followed first,
each channel view plus a `followed_at` timestamp and the caller's
`notification_setting`; the videos from those channels are a
separate feed at `GET /api/v1/me/subscriptions/videos`, and remote-channel follows live
at `GET /api/v1/me/remote-follows`.

**The notification bell.** `PUT /api/v1/channels/{handle}/follow/notifications` (auth,
body `{notification_setting}`) sets the caller's bell for a channel they follow: `all`
notifies them of every new **public** video the channel publishes, `none` mutes those
notifications while keeping the subscription (the channel stays in their feed). A new
follow starts at `all`. An unsupported mode is `422` before any write; a caller who
does not follow the channel gets `404`, exactly as an unknown handle does — the bell is
part of a subscription, and the two cases are deliberately indistinguishable so the
endpoint cannot be used to probe which handles exist. There is no "personalized" third
mode (an INTENTIONAL_DIFFERENCE from YouTube): nothing personalises it here, so
offering it would be a lie in the UI. The per-user `new_video` notification preference
(`PATCH /api/v1/me/notification-prefs`) is the master switch layered on top.

Publishing a video fans the `new_video` notification out to the channel's local
followers in a single set-based statement, from the publish transition — so direct,
scheduled, post-transcode and moderator-approved publishes all fire it once. A
follower is told only when the video is published **and** public (unlisted, private and
password-protected videos are never announced) and unblocked, their bell is `all`,
their `new_video` preference is not off, they have not muted the channel owner, neither
side has blocked the other, and they are not the owner. Re-running the fan-out for the
same video inserts nothing, so a hook that fires twice cannot double-notify.

### Avatars & banners

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

### Donation addresses

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

### Channel auto-sync

**Channel auto-sync** (`POST/GET /api/v1/channel-syncs`,
`DELETE /api/v1/channel-syncs/{id}`, `POST /api/v1/channel-syncs/{id}/sync-now`)
mirrors an external platform channel's recent uploads into a local channel you
own. A periodic worker lists the remote channel with the sandboxed yt-dlp
extractor (`--flat-playlist`) and, for each not-yet-seen upload, creates a
**private draft** video and enqueues a `ytdlp` import — so the bytes flow through
the same `AttachOriginal → Process` scan pipeline (nothing is servable before the
ClamAV scan, and the instance never auto-publishes mirrored content). It is **OFF
by default and effective only when `YTDLP_IMPORT_ENABLED` is also on** — the sync
*is* a yt-dlp import path, so the same egress-proxy / no-internal-route deploy
stance above applies. Config: `CHANNEL_SYNC_ENABLED=true` to opt in,
`CHANNEL_SYNC_INTERVAL` (default `1h`), `CHANNEL_SYNC_MAX_PER_USER` (default `5`),
`CHANNEL_SYNC_BATCH` (default `15`, newest uploads per pass). When disabled the
endpoints answer `503`.

## Video pipeline

### Videos

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

### Original file upload, malware scanning & quotas

`POST /api/v1/videos/{id}/file` (owner-only, `multipart/form-data` with a single
`file` part) stores the original through the storage backend, then finalises the
video: `draft → processing → published` (or `failed` if a configured media probe
rejects it). Re-uploading replaces the prior original, and non-owner/unknown → `404`.
**Malware scanning** (`MALWARE_SCAN_ENABLED=true`, streams the original to the
clamd at `CLAMAV_ADDR` — the compose `scan` profile ships one): an INFECTED file
always fails. `MALWARE_SCAN_MODE` decides the fallback on a scan *error*:
`fail-closed` (default — not published), `fail-open` (published anyway, logged
loudly), or `quarantine` (parked in the moderator review queue). A single scan is
bounded by `CLAMAV_TIMEOUT` (default `60s`), so a slow/unreachable clamd surfaces
as a scan error (resolved by the mode above) rather than hanging the upload. Any
outcome that keeps an upload out of `published` (infection, or unscannable under a
non-publishing mode) writes a `content.upload.malware_rejected` audit event —
safe ids/outcome/policy only, never file content.
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

### Chunked / resumable upload

**Chunked/resumable upload**: `POST /api/v1/videos/{id}/upload-session`
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
cleanup).

### Async URL import

**Async URL import**: `POST /api/v1/videos/{id}/import` (owner,
body `{url}`) now enqueues an `import_jobs` row and returns `202 {import_job}`
instead of blocking on the fetch; a background worker performs the SSRF-guarded
fetch and runs the bytes through the same pipeline (retry/backoff, dead-letter
after 5). Poll `GET /api/v1/videos/{id}/import` for the job status (`state`
pending/running/done/failed, plus a safe `error` reason on failure). A single
import runs per video at a time; re-posting while one is in flight returns it.

### yt-dlp platform extractor

The import body also takes an optional `resolver` (`auto`|`direct`|`ytdlp`,
default `auto`) and the job view carries the concrete `resolver` plus a coarse
`stage` (`resolving`/`downloading`/`processing`). `auto` tries a direct media
fetch and falls back to the **yt-dlp platform extractor** when it is enabled;
`ytdlp` forces it and is refused with `503` when disabled. **yt-dlp import is OFF
by default** (`YTDLP_IMPORT_ENABLED=true` to opt in; `YTDLP_PATH`, `YTDLP_TIMEOUT`,
`YTDLP_MAX_HEIGHT`, `YTDLP_PROXY`). The extractor is hard-sandboxed: a fixed argv
allowlist (no shell, `--ignore-config`, no `--exec`, no runtime self-update),
`--no-playlist`, `--max-filesize`, a private `0700` per-job workdir that is always
removed, and a hard wall-clock timeout. The source URL is validated by the SSRF
guard *before* the subprocess sees it, and downloaded bytes enter storage only via
`AttachOriginal → Process`, so the ClamAV scan hook fires before anything is
servable. **Residual risk (deploy note):** yt-dlp makes its own outbound fetches
that the dial-time SSRF guard cannot pin, so a hostile page could try to redirect
it to internal addresses. Mitigate, in order: (1) it is off by default; (2) set
`YTDLP_PROXY` to a forward proxy that denies RFC1918/loopback/link-local (see the
`ytdlp-egress` sketch in `docker-compose.yml`); (3) run the api/worker with no
route to internal networks. Pin the yt-dlp binary in the image
(`--build-arg YTDLP_VERSION=…`); it is never self-updated at runtime.

### Finalisation & probing

Finalisation runs through an injected `Prober` seam: at startup the server uses
the FFprobe-backed prober when `ffprobe` is on `PATH` (it is in the Docker image),
extracting technical metadata (duration, width, height) that the detail endpoint
exposes and persisting it to `video_metadata`; a probe error marks the video
`failed`. Where `ffprobe` is absent the original is trusted and published unprobed
(no metadata). The public discovery surfaces — `GET /api/v1/videos`,
`/videos/search`, and the public view of a channel's videos — return only
`published` videos. When transcoding is enabled (below), the publish transition
also enqueues an HLS transcode job, best-effort — it never blocks publishing.

### HLS transcoding

**HLS transcoding** (`TRANSCODING_ENABLED`, default **on**; set `=false` to serve
originals only; needs `ffmpeg` + `ffprobe` on `PATH` — both are in the Docker
image, and a host without them degrades gracefully to originals-only with a boot
warning): this ladder is what powers the player's resolution/quality selector —
with it off a video only serves its single progressive original, so playback is
stuck at one resolution. Publishing a video enqueues
a durable job in `transcode_jobs` (mirroring the federation delivery queue) and
an in-process worker produces an H.264/AAC HLS ladder — rungs from
1080p/720p/480p/360p, capped at the source height (never upscaled; a smaller
source gets a single rung at its own size) — stored under
`streaming-playlists/<video_id>/` with ~6s MPEG-TS segments whose boundaries are
aligned to forced IDR frames for independent decoding and clean ABR switches.
Every rung also carries a compact one-frame-per-second I-frame-only byte-range
playlist (`iframe.m3u8` + `iframe.ts`) for native HLS trick-play scanning.
Failures retry with exponential backoff and dead-letter after 5 attempts. Once the
`streaming_playlists` row is `ready`, the video detail carries `hls_url` +
`renditions [{height,width}]`, and playback is served (same visibility rules as
`/original`) by `GET /api/v1/videos/{id}/hls/master.m3u8`
(`application/vnd.apple.mpegurl`) and
`GET /api/v1/videos/{id}/hls/{rendition}/{file}` (variant playlists + `video/mp2t`
segments; all playlist URIs are relative so proxying works). Progressive
playback of the original remains available regardless.

### VP9 alternate

**VP9 alternate** (`TRANSCODING_VP9_ENABLED=true`, needs transcoding on): the
transcoder additionally emits a progressive VP9/WebM file at the top ladder rung
(`libvpx-vp9`/`libopus`, best-effort — a VP9 failure never fails the H.264 HLS).
It is stored as a `webm` file and surfaced by `GET /api/v1/videos/{id}/download`
(kind `webm`) and served with Range/206 at `GET /api/v1/videos/{id}/webm`. VP9 is
a progressive **download alternate** rather than an HLS variant on purpose
(HLS+VP9 needs fMP4/CMAF with patchy client support). **AV1** is deferred:
`TRANSCODING_AV1_ENABLED=true` fails config validation with a defer note.

### Thumbnails

During finalisation an FFmpeg-backed thumbnailer (when `ffmpeg` is on `PATH`)
extracts a poster frame and stores it as a `thumbnail` file;
`GET /api/v1/videos/{id}/thumbnail` serves the JPEG (same visibility as the
detail endpoint), and the detail response carries a `has_thumbnail` flag.
Thumbnail generation is best-effort — a failure never blocks publishing. The
**preview** is this thumbnail (there is no separate animated preview).

### Storyboard

**Storyboard** (seek-preview sprite sheet; needs `ffmpeg` + `ffprobe`): a
sprite sheet of up to 100 160×90 tiles plus a WebVTT sprite map are generated
best-effort during finalisation and stored at `storyboards/<id>.{jpg,vtt}`.
`GET /api/v1/videos/{id}/storyboard.jpg` and `…/storyboard.vtt` serve them
(same visibility as the detail endpoint), and the detail carries a
`has_storyboard` flag.

### Chapters

**Chapters** (seek-bar marks): `GET /api/v1/videos/{id}/chapters`
returns `{chapters:[{start_seconds,title}]}` in ascending order (same visibility
as the detail endpoint; `[]` when none), and the detail carries a `has_chapters`
flag. `PUT /api/v1/videos/{id}/chapters` (owner only; non-owner/unknown → 404)
replaces the whole set atomically — an empty array clears it. The set is
validated in full before any write (400 on: non-ascending/duplicate starts, a
start `>=` the probed duration, a title not 1–120 chars after trim, or more than
100 chapters).

### Media garbage collection

**Media garbage collection**: `POST /api/v1/admin/media/gc` (admin) lists stored
objects under the known media prefixes and deletes those with no database
reference. It defaults to a dry run (`{"dry_run":false}` deletes); it never
lists or touches an unknown prefix, and it is audited. A daily in-process worker
runs the same sweep.

## Playback

### Original streaming

`GET /api/v1/videos/{id}/original` streams the stored original bytes for direct
playback — same visibility as the detail endpoint (private → owner only, else
`404`; a video with no stored original is `404`). It honours HTTP `Range`
requests (`206 Partial Content`) so a `<video>` element can seek; the local
backend serves via `http.ServeContent`.

### Video passwords & embed privacy

**Video passwords + embed privacy**: the `privacy` enum gains
`password`. A `password` video is excluded from public listings exactly like
`unlisted`; its detail returns **401 `code=password_required`** (the documented
exception to 404-for-invisible) until unlocked. The owner manages passwords via
`GET/POST/PUT /api/v1/videos/{id}/passwords` + `DELETE …/passwords/{passwordId}`
(bcrypt-hashed, write-only — a plaintext/hash is never returned; each 6–100
chars, at most 20; a video may be `privacy=password` only while it has ≥1
password, and the last password of a `password` video can't be deleted → 409).
A viewer unlocks with `POST /api/v1/videos/{id}/unlock` (`{password}` →
`{playback_token, expires_in:21600}`), which mints a **6-hour, video-scoped
HMAC playback token** carrying no account identity. Every video read endpoint
accepts it as `Authorization: Bearer <playback_token>` **or** `?pt=<token>` (the
header-less path for Safari native-HLS and progressive playback); an HLS
playlist requested with `?pt=` has its relative variant/segment URIs rewritten
to propagate the token. The unlock endpoint is rate-limited under the same
budget as login; the token is a secret (never logged). Embed privacy lives in
two columns on `videos`: `GET/PUT /api/v1/videos/{id}/embed-privacy`
(`{status: enabled|disabled|whitelist, allowed_domains?}`; owner-only write, ≤50
bare hostnames for `whitelist`). Enforcement is at the embed page in
`vidra-user` (referrer / ancestor-origin check); server-side Referer enforcement
is a non-goal.

### Per-user player settings

**Per-user player settings**: the signed-in user's playback defaults —
`GET /api/v1/me/player-settings` always returns the full effective object
(`{autoplay_next, default_speed, default_quality, captions_default,
theater_default, video_card_previews_enabled}`; a user who never saved gets the
effective defaults, no 404). Until the user explicitly chooses,
`video_card_previews_enabled` inherits the public `GET /api/v1/instance` flag
`features.video_card_previews_default_enabled` (backed by the admin setting of
the same name, default false); explicit true or false survives later changes to
that default. It is the user half of a two-factor hover-preview gate: the public
`features.video_card_previews` global flag (backed by the admin
`video_card_previews_enabled` setting, also default false) must be true as well.
`PUT /api/v1/me/player-settings` is a **merge**: any subset of the six fields is
accepted and omitted fields keep their stored value; omitting the preview field
preserves inherited state. Validation is 400 — `default_speed` must be one of
the shared playback rates `0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3, 3.5,
4` (kept in lockstep with the frontend `PLAYBACK_RATES`) and `default_quality`
must be `auto` or a
rendition height like `720p` (`^[0-9]{2,4}p$`). Scope is per-user only —
per-video/per-channel overrides are deliberately not ported.

### View recording

`POST /api/v1/videos/{id}/view` records a view (same visibility as detail; only
published videos accrue views), de-duplicated per viewer per hour in Redis
(`SETNX` over the authenticated user id, else the client IP, hashed — raw
ids/IPs are never used as keys). It always returns `204`; the running `views`
total is exposed on the detail endpoint. (View counts live in a `video_view_counts`
side table; surfacing them on feed cards and a trending sort are later slices.)

### Captions & Whisper auto-captions

**Captions**. A video owner uploads WebVTT tracks with
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

## Live streaming

### Live streaming

A channel owner creates a live stream with
`POST /api/v1/channels/{handle}/live` (`{title, description?, privacy?,
permanent?, replay_enabled?}`) and receives a stream key ONCE plus the RTMP URL;
only the key's SHA-256 hash is stored. `GET/PATCH/DELETE /api/v1/live/{id}` read/
edit/delete it (`PATCH` edits title/description/privacy/permanent/replay_enabled),
`POST /api/v1/live/{id}/key` rotates the key, and `GET /api/v1/channels/{handle}/live`
lists a channel's streams (owner only; keys are never returned).
`GET /api/v1/live` is the public "Live now" listing: currently-live PUBLIC
streams across all channels (never unlisted/private), most-recently-started
first, paginated (`limit`/`offset`); each card carries `id, title, channel_*,
started_at, is_live` (+ `hls_url` when a media server is configured). It has no
viewer/concurrent count (no server-side counter exists yet) and no live
thumbnail (none is generated yet). `started_at` is stamped when a
stream goes live and also appears on the single-stream views.

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

### Replay → VOD

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

## Messaging & E2EE

### Direct messaging

Direct messaging: `POST /api/v1/conversations` starts (or idempotently returns)
the 1:1 conversation with a recipient; `GET /api/v1/me/conversations` is the
inbox (with per-conversation `unread_count`); `POST`/`GET /api/v1/conversations/{id}/messages`
send/list. A user block in either direction refuses messaging with `403`.

DM completeness: **attachments** — `POST /api/v1/conversations/{id}/attachments`
(multipart `file`, ≤100 MiB, image/video/audio/pdf/doc, ClamAV fail-closed when
`MALWARE_SCAN_ENABLED`) returns an `attachment_id` to reference in a send
(`attachment_ids: []`, ≤30, own-uploaded); `GET /api/v1/attachments/{id}` serves
the bytes participant-gated; attachments are plaintext-only (encrypted
conversations `422`). Facebook-Messenger-parity limits apply instead of storage-quota
counting: per-file 100 MiB (`413`), 30 per message (`422`),
allowlist adds office documents as kind `doc` (DOC/DOCX/PPT/PPTX/XLS/XLSX; anything
else `415`), and the upload route is per-user rate limited (`429`,
`ATTACHMENT_UPLOAD_RATE_LIMIT_*`) as the no-quota compensating control. **Link previews** — the first URL in a plaintext body is
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

### Encrypted messaging (E2EE)

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

## Federation & identity

### ATProto / Bluesky cross-posting

**ATProto / Bluesky cross-posting** (a Vidra extension; see
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

## Storage

### Media storage backends (local & S3)

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
reader.

### IPFS mirror (public)

IPFS is NOT an authoritative backend (`STORAGE_BACKEND=ipfs` is rejected);
it is an orthogonal, opt-in **mirror sidecar** for eligible already-public media
(`IPFS_ENABLED`, `IPFS_*`; admin `GET /api/v1/ipfs/status` +
`POST /api/v1/admin/ipfs/reconcile` answer 503 when disabled). For local dev the
compose `ipfs` profile runs Kubo v0.41
(`docker compose --profile core --profile ipfs up`; then `IPFS_ENABLED=true` with
`IPFS_API_URL=http://ipfs:5001` and dev gateway `http://localhost:9090`, which are
the compose defaults). It is genuinely **local-only** by default: the init hook
blocks all swarm addresses, removes bootstrap peers, and disables routing,
providing, and relay/NAT traversal.
Set `IPFS_PUBLIC_NETWORK=true` for live public distribution and point
`IPFS_GATEWAY_URL` at the client-facing public/self-hosted gateway. Live mode
enables DHT providing plus relay/hole-punch reachability and is deliberately
explicit because every public CID is a permanent disclosure. Kubo RPC ports are
published on host loopback only. Once an eligible public asset is fully pinned, its
stable application URL returns a short-lived `307` to the immutable gateway CID for
video thumbnails, storyboard sprite sheets, user/channel avatars and banners, and
public playlist covers. Missing, pending, failed, invalid, or private-swarm pins — and
IPFS lookup failures — transparently fall back to the authoritative local/S3 object.
The small storyboard VTT map stays on the stable application URL and points at the
independently redirected `storyboard.jpg`. An optional **IPFS Cluster** (`IPFS_CLUSTER_API_URL`,
`IPFS_CLUSTER_TOKEN` — a secret Bearer token) replicates node pins across peers.
Real-node round-trip tests live behind the `ipfs_integration` build tag
(`make test-ipfs-integration`; self-skips without a node) and a dedicated optional
CI job — the canonical `make ci` gate stays green nodeless. See
`.ralph/specs/ipfs-media.md`.

### Private IPFS tier

**Private IPFS tier (`.ralph/specs/ipfs-media-private.md`).** Non-public
media (private/unlisted videos + derivatives, unlisted/deactivated-owner avatars &
banners, non-public playlist covers) is replicated to a **second, fully separate
`swarm.key`'d Kubo node** — never the public node (dual-homing is a hard config error).
The design is **replication, not distribution**: private CIDs never appear in any API
response, there is deliberately **no** `IPFS_PRIVATE_GATEWAY_URL` knob, and viewer
serving stays on the authenticated app API. The failure mode is always "private content
unreachable", never "private content public" — `LIBP2P_FORCE_PNET=1` makes the private
daemon **refuse to boot** without a key. Enable with `IPFS_MIRROR_PRIVATE=true` +
`IPFS_PRIVATE_API_URL` (may run standalone with `IPFS_ENABLED=false`). For local dev the
compose **`ipfs-private`** profile runs the private node
(`docker compose --profile core --profile ipfs-private up`; RPC on host `:5002`, gateway
NOT published; the init script auto-generates a **dev-only** `swarm.key`, clears
bootstrap, and sets `Routing.Type=none` / `Provide.Enabled=false` /
`Gateway.NoFetch=true`, with public relay/NAT traversal disabled).
The optional **`ipfs-private-cluster`** profile adds a second keyed node + an IPFS Cluster
peer (`IPFS_PRIVATE_CLUSTER_API_URL`, `IPFS_PRIVATE_CLUSTER_SECRET` — both secrets) for
replication testing. **`swarm.key` custody:** possession == full network membership; there
is no per-node revocation and rotation means a new key + a coordinated restart of every
node. In production you generate the key once, distribute the same file to every node,
mount it read-only, and **never commit it** (`.gitignore` guards `deploy/ipfs-private/*.key`).
Isolation/fail-closed proofs (keyed pair replicates; an outside node cannot fetch; a
keyless `LIBP2P_FORCE_PNET=1` daemon refuses to start) live behind the
`ipfs_private_integration` build tag (`make test-ipfs-private-integration`; self-skips
without nodes) — `make ci` stays green nodeless.

## Platform

### Instance metadata & registration

Registration can be closed per-instance with `REGISTRATION_ENABLED=false`: signup then
returns `403` and `GET /api/v1/instance` reports `registration_enabled: false` so the
frontend can hide the form. The instance endpoint also surfaces optional about/legal
metadata — `description`, `short_description`, `terms_url`, `privacy_url`,
`contact_email`, `default_language`, `categories`, `moderator_languages`,
`server_country`, `is_sensitive`, `sensitive_content_policy`,
`contact_form_enabled`, `social_links`, and `features` (empty/false/defaulted when
unset) — for the frontend's footer/about pages. Long-form operator markdown lives at
`GET /api/v1/instance/about`; the public contact form posts to
`POST /api/v1/instance/contact` and is available only when `contact_form_enabled` is on,
an effective contact email is set, and outbound mail is configured.

### Runtime-mutable instance settings

**Runtime-mutable instance settings.** A defined subset of instance settings is a DB
overlay on top of config: an admin edits them live via `GET`/`PATCH
/api/v1/admin/instance-settings` and they take effect without a restart. The mutable
subset is `instance_name`, `instance_description`, `instance_short_description`,
`terms_url`, `privacy_url`, `contact_email`, the platform-information markdown/link
keys (`support_text`, `terms`, `code_of_conduct`, `moderation_info`,
`administrator_info`, `creation_reason`, `maintenance_lifetime`, `business_model`,
`hardware_info`, `website_link`, `mastodon_link`, `x_link`, `bluesky_link`),
taxonomy-backed list keys (`instance_categories`, `moderator_languages`), platform
defaults (`default_language`, `server_country`, `instance_is_sensitive`,
`sensitive_content_policy`, `contact_form_enabled`), `registration_enabled`,
`registration_require_approval`, `quarantine_new_uploads`, and the feature toggles
`uploads_enabled`, `imports_enabled`, `live_enabled`, `comments_enabled` (all default
true, from `FEATURE_*_ENABLED`). The matching env vars are the boot-time DEFAULTS where
they exist; platform-information keys otherwise use hardcoded defaults. A stored
override wins. When a toggle is off, its endpoint returns `403 feature_disabled` (new
upload sessions + direct upload, URL import, live-stream create, comment create). When
`sensitive_content_policy` is `hide`, videos marked `is_sensitive` are excluded from
public browse/list/search surfaces while owner, admin, and direct watch reads remain
unfiltered. Boot-time-only settings — the database DSN, the KEKs, the JWT secret, the
storage backend — deliberately STAY config-only (unsafe to hot-swap and/or secret) and
are never represented in the overlay table.

### Error envelope

All non-2xx responses share one envelope: `{"error":{"code","message","request_id"}}`
(see `api/openapi.yaml` → `ErrorResponse`). The readiness probe returns its own
`ReadinessResponse` on 503. `make build` injects version/commit/date into `/version`
via `-ldflags`.

### Request validation

Request validation: handlers decode+validate input via `bindAndValidate`. Malformed
bodies get `400 bad_request`; failed validation gets `422 unprocessable_entity` with a
`fields` array (`{field, message}`) so forms can highlight the offending inputs.

### Email delivery (SMTP)

Email delivery: password-reset and email-verification tokens are handed to a `Mailer`
adapter. By default nothing is sent (tokens are still generated and consumable). Set
`MAIL_ENABLED=true` plus `SMTP_HOST`, `SMTP_PORT` (default 587), `SMTP_FROM`, and
optional `SMTP_USERNAME`/`SMTP_PASSWORD` (AUTH PLAIN; the password is a secret — never
logged) to deliver plain-text mail over SMTP, with STARTTLS whenever the relay offers
it. The dev capture seam (`DEV_MAIL_CAPTURE_ENABLED`) wins over SMTP when both are on.

### Request guards

Request guards: bodies over `HTTP_BODY_LIMIT` (default `8M`) are rejected with `413`;
each request carries a `HTTP_REQUEST_TIMEOUT` (default `30s`) context deadline that
handlers and DB/Redis calls observe (a fired deadline renders as a `503`
`request_timeout`), with the server `WriteTimeout` as the hard backstop.

### Rate limiting

Rate limiting: the `/api` surface is rate limited per client IP with a Redis
fixed-window limiter (`RATE_LIMIT_REQUESTS` per `RATE_LIMIT_WINDOW`, default 120/min;
disable with `RATE_LIMIT_ENABLED=false`). Responses carry `X-RateLimit-Limit`,
`X-RateLimit-Remaining`, and `X-RateLimit-Reset`; over-budget requests get `429`
`rate_limited` with `Retry-After`. A stricter per-IP budget
(`AUTH_RATE_LIMIT_REQUESTS`) layers over the sensitive auth endpoints, and the DM
attachment upload route carries a separate **per-user** budget
(`ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS` per `ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW`,
default 60/10m) as the no-quota anti-abuse control. System probes (`/healthz`,
`/readyz`, `/version`) are exempt. If Redis is unreachable the limiter fails open
(logs a warning) so a Redis blip degrades protection, not availability. Rate limits
are deploy-time config only — there is no runtime mutation endpoint; the effective
non-secret values are surfaced read-only on `GET /api/v1/admin/system`
(`rate_limits`).

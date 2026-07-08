# W1 — Watch & Player Backports: vidra-core spec

**Programme:** `../../.ralph/specs/backport/PROGRAM.md` (monorepo root), wave W1.
**Feature IDs (FEATURE_VISION.md):** CORE-15 (chapters), CORE-16 (storyboards),
CORE-17 (embed privacy + video passwords), PLAY-07 (per-user player settings).
**Reference contracts (adapt, NEVER serve verbatim):**
`../../.ralph/specs/backport/api-reference/openapi_video_passwords.yaml`,
`openapi_video_embed_privacy.yaml`, `openapi_player_settings.yaml`,
`openapi_video_storyboards.yaml`. Chapters have no reference yaml — the backup's
contract was `GET/PUT /api/v1/videos/{id}/chapters` with `{timecode, title}` rows
(PeerTube-compatible model).

## Ground rules (from PROGRAM.md §1 and §4)

- Port **contracts and ideas, not code**. Where the archived reference conflicts
  with current conventions, **current conventions win**. Concretely, every W1
  endpoint follows the existing `vidra-core/api/openapi.yaml` house style:
  - snake_case JSON fields (`start_seconds`, `created_at`), NO `{success,data}`
    envelope (the envelope in the reference yamls is a backup-era artifact).
  - UUID ids (the reference's `int64` ids become `uuid`).
  - String enums, not integer enums (the reference's embed `status: 1|2|3`
    becomes `"enabled" | "disabled" | "whitelist"`).
  - Per-user resources live under `/api/v1/me/...` (not `/users/me/...`).
  - Invisible/not-owned resources are **404** (matching the thumbnail/HLS
    endpoints), except the explicit password-gate signal defined below.
  - Optional-auth endpoints declare `security: [{bearerAuth: []}, {}]`.
- **Contract first**: every slice updates `api/openapi.yaml` in the same commit;
  the route↔spec drift guard (`TestOpenAPIContract`) and the `openapi` workflow
  stay green. The reference yamls are never copied into `api/`.
- Stack: Echo handlers (`internal/httpapi` + a domain package), sqlc queries,
  PostgreSQL migrations as `migrations/00NN_<name>.up.sql` / `.down.sql`
  (next free number after the current `0067_peertube_import`; renumber to
  whatever is free at implementation time).
- Gate: `make ci` green locally AND on branch CI before any box ticks; pushed.

## Cross-cutting context

The frontend wave (`vidra-user/.ralph/specs/backport-w1-watch-player.md`)
replaces the native `<video controls>` with a **custom player shell**. Its
quality selector consumes the existing HLS surface (`hls_url`, `renditions[]`,
`GET /videos/{id}/hls/master.m3u8`). There is a **separate ongoing
investigation** into whether the transcode ladder actually emits multiple
renditions for uploaded videos; W1 does not fix that pipeline, but W1.C0 below
pins the correct contract with tests so the selector has a verified target.

---

## W1.C0 — Playback-surface verification (supports CORE-16 + the quality selector)

No new endpoints. Storyboards (CORE-16) are **already shipped** in vidra-core:
`GET /api/v1/videos/{id}/storyboard.jpg` (sprite sheet) + `GET
/api/v1/videos/{id}/storyboard.vtt` (WebVTT map with `#xywh=` regions) + the
`has_storyboard` flag on the video detail. Do NOT add the backup's
`GET /videos/{id}/storyboards` JSON-list endpoint — the VTT+JPG contract is the
current convention and the frontend adapts to it (deliberate deviation from
`openapi_video_storyboards.yaml`, recorded here).

**Work:**
1. Service/integration tests that pin, for a multi-rendition source
   (e.g. a 1280×720 fixture with a ladder that should produce ≥2 rungs):
   - the master playlist contains one `#EXT-X-STREAM-INF` per produced rung,
     each with distinct `RESOLUTION` and `BANDWIDTH` attributes, variant URIs
     relative (`"720p/playlist.m3u8"`);
   - `renditions[]` on the detail matches the rungs in the master playlist
     (same set of heights, tallest first);
   - every advertised rendition's `playlist.m3u8` and first segment return 200.
2. A storyboard test asserting the VTT cues cover `[0, duration_seconds]` with
   no gaps and each cue's `#xywh` region lies inside the sprite-sheet bounds.
3. If (1) FAILS because the ladder only ever emits one rung, do not fix the
   transcoder inside W1 — mark the task `BLOCKED (ladder investigation)` with
   the failing evidence and continue; the test then becomes the acceptance
   test for that investigation.

**Non-goals:** changing ladder policy, new storyboard endpoints, per-rendition
bitrate tuning.

---

## W1.C1 — CORE-15 · Video chapters

### API contract (add to `api/openapi.yaml`, tag `videos`)

`GET /api/v1/videos/{id}/chapters`
- Auth: optional (`bearerAuth` or none). Visibility identical to
  `GET /videos/{id}` (private → owner only, blocked → moderators, else 404;
  password-protected → same gate as the detail once W1.C2 lands).
- 200: `{ "chapters": [ { "start_seconds": 0, "title": "Intro" }, ... ] }`
  — sorted ascending by `start_seconds`; `{ "chapters": [] }` when none.
- 404: no such video / not visible (`ErrorResponse`).

`PUT /api/v1/videos/{id}/chapters`
- Auth: required; owner only (non-owner or unknown id → 404, matching the
  thumbnail POST convention).
- Body: `{ "chapters": [ { "start_seconds": int, "title": string }, ... ] }` —
  **replaces the whole set atomically** (empty array clears all chapters; there
  is no per-row endpoint).
- Validation (400 `ErrorResponse` on violation):
  - `0 <= start_seconds`; strictly increasing (implies unique);
  - `start_seconds < duration_seconds` when the probe recorded a duration;
  - `title` 1–120 chars after trim;
  - at most 100 chapters.
- 200: the stored `{ "chapters": [...] }` (same shape as GET).
- 401 missing/invalid token.

### Video detail composition

Add `has_chapters: boolean` to the `Video` schema, present on the **detail**
view only (same presence rule as `has_thumbnail` / `has_storyboard`), so the
player fetches `/chapters` only when they exist.

### DB / migration sketch (`00NN_video_chapters.{up,down}.sql`)

```sql
CREATE TABLE video_chapters (
    video_id      uuid    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    start_seconds integer NOT NULL CHECK (start_seconds >= 0),
    title         text    NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    PRIMARY KEY (video_id, start_seconds)
);
```

sqlc queries: `ListVideoChapters` (ordered by `start_seconds`),
`DeleteVideoChapters`, `InsertVideoChapter` (replace = delete + insert in one
tx at the service layer). New domain package `internal/chapter` (or fold into
`internal/video` if the team prefers — one package, not both).

### Tests

- Unit/service: validation table (ordering, bounds, title length, cap,
  duration clamp), atomic replace (failure mid-tx leaves the old set), empty
  replace clears, visibility matrix (anon/other-user/owner/moderator ×
  public/unlisted/private).
- Contract: routes registered, `TestOpenAPIContract` green, handler tests for
  200/400/401/404 shapes.

### Non-goals

Auto-extracting chapters from description timestamps (PeerTube extension —
future wave), chapters on live streams, federating chapters over AP.

---

## W1.C2 — CORE-17 · Video passwords + embed privacy

### Part A — password-protected videos

**Privacy model.** Extend the video `privacy` enum from
`[public, unlisted, private]` to `[public, unlisted, private, password]`
(string enum — current convention; the backup treated passwords as an
orthogonal table, PeerTube as numeric privacy 5; we adapt to the string enum).
Rules:
- A video may be set to `privacy: password` only if it has ≥ 1 password
  (400 otherwise). Deleting the last password of a `password` video → 409.
- `password` videos are excluded from all public listings (feed, search,
  public channel lists, trending) exactly like `unlisted`; direct links work
  after unlock. Owner/moderator access is never gated.

**Owner management endpoints** (tag `videos`; all owner-only — non-owner or
unknown id → 404; 401 without token). Plaintext is write-only: responses never
carry passwords or hashes.

- `GET /api/v1/videos/{id}/passwords`
  → 200 `{ "passwords": [ { "id": "<uuid>", "created_at": "<ts>" } ] }`
- `POST /api/v1/videos/{id}/passwords` body `{ "password": string }`
  (6–100 chars; 400 outside) → 201 `{ "id": "<uuid>", "created_at": "<ts>" }`
- `PUT /api/v1/videos/{id}/passwords` body `{ "passwords": [string, ...] }`
  (replace-all, each 6–100 chars, ≥ 1 entry when the video is
  `privacy: password`, ≤ 20 entries) → 200 the GET shape
- `DELETE /api/v1/videos/{id}/passwords/{passwordId}` → 204;
  404 unknown passwordId; 409 when it is the last password of a
  `privacy: password` video.

Hashing: bcrypt (same cost policy as account passwords).

**Viewer unlock flow** (the adaptation replacing PeerTube's
per-request password header — decided so Safari native-HLS and progressive
`<video src>` playback, which cannot set headers, still work):

- `GET /api/v1/videos/{id}` on a `password` video without a valid credential →
  **401** with `ErrorResponse.code = "password_required"` (deliberate deviation
  from the 404-for-invisible rule so the watch page can render a prompt; a
  plain wrong-id is still 404).
- `POST /api/v1/videos/{id}/unlock` body `{ "password": string }`
  - Auth: optional. Rate-limited via `internal/ratelimit` (same budget family
    as login).
  - 200 `{ "playback_token": "<opaque>", "expires_in": 21600 }` — an
    HMAC-signed token scoped to exactly this video id, TTL 6 h, carrying no
    account identity.
  - 401 wrong password; 404 unknown video or video not `privacy: password`.
- All video-scoped read endpoints (`GET /videos/{id}`, `/chapters`,
  `/original`, `/download`, `/webm`, `/hls/master.m3u8`, `/hls/{rendition}/{file}`,
  `/thumbnail`, `/storyboard.jpg`, `/storyboard.vtt`, `/captions*`) accept the
  playback token as `Authorization: Bearer <playback_token>` **or** query
  param `?pt=<playback_token>` (only this token type is ever accepted in a
  query; account tokens never are). When an HLS playlist request carries
  `?pt=`, the served playlist rewrites its relative variant/segment URIs to
  append the same `?pt=` so native-HLS chains keep working.

### Part B — embed privacy

Columns on `videos` (not a separate table — one row per video, no fan-out):

- `GET /api/v1/videos/{id}/embed-privacy`
  - Auth: optional; same visibility as the detail (the embed page needs it
    pre-unlock for `password` videos → for those it is readable with the
    `password_required` gate applied to the *detail*, but this endpoint itself
    returns the status so the embed page can decide; it exposes nothing else).
  - 200 `{ "status": "enabled" | "disabled" | "whitelist",
           "allowed_domains": ["example.com", ...] }`
    (`allowed_domains` present only for `whitelist`).
- `PUT /api/v1/videos/{id}/embed-privacy` (owner only; 404 non-owner)
  - Body `{ "status": "...", "allowed_domains": [...] }`; 400 on unknown
    status, `whitelist` with an empty/invalid domain list (hostnames only, no
    scheme/path, ≤ 50 entries), or `allowed_domains` supplied with a
    non-whitelist status.
  - 200: the GET shape.

Enforcement is at the embed page (vidra-user checks `document.referrer` /
`location.ancestorOrigins` against the policy and refuses to play). Server-side
Referer enforcement on media endpoints is a **non-goal** (Referer is spoofable
and breaks legitimate proxies; this matches how PeerTube enforces it).
DROPPED from the reference: `GET /videos/{id}/embed-privacy/allowed` — the
client evaluates the policy from the GET response; a dedicated check endpoint
adds surface for no gain.

### DB / migration sketch (`00NN_video_passwords_embed.{up,down}.sql`)

```sql
CREATE TABLE video_passwords (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id      uuid        NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX video_passwords_video_id_idx ON video_passwords (video_id);

-- privacy: extend the existing CHECK/enum to include 'password'
-- (exact ALTER depends on how privacy is constrained today — keep the same
-- mechanism, just add the value)

ALTER TABLE videos
    ADD COLUMN embed_privacy text NOT NULL DEFAULT 'enabled'
        CHECK (embed_privacy IN ('enabled','disabled','whitelist')),
    ADD COLUMN embed_allowed_domains text[] NOT NULL DEFAULT '{}';
```

### Tests

- Unit/service: bcrypt verify; unlock token is video-scoped (token for video A
  rejected on video B), expires, survives none of the account-auth paths;
  rate-limit on `/unlock`; listing exclusion of `password` videos from
  feed/search/channel/trending; last-password 409; playlist `?pt=` rewrite;
  embed-privacy validation matrix.
- Contract: all new paths in `openapi.yaml`, drift guard green; handler tests
  for every status code listed above, including the `password_required` 401
  code on the detail.

### Non-goals

oEmbed/RSS discovery (FED-09), password-protected live streams, federation of
password videos (they are excluded from AP publishing like `unlisted`),
server-side Referer enforcement, per-viewer password analytics.

---

## W1.C3 — PLAY-07 · Per-user player settings

**Adaptation note (recorded per PROGRAM §1):** the archived
`openapi_player_settings.yaml` scoped settings per-video/per-channel. The
FEATURE_VISION PLAY-07 definition — and this wave — is **per-user defaults**.
Per-video/per-channel overrides are a non-goal.

### API contract (add to `api/openapi.yaml`, same tag as the neighbouring
`/api/v1/me/*` endpoints)

`GET /api/v1/me/player-settings`
- Auth: required (401 otherwise).
- 200 — always returns the full effective object; a user who never saved gets
  the defaults (no 404):

```json
{
  "autoplay_next":    true,
  "default_speed":    1,
  "default_quality":  "auto",
  "captions_default": false,
  "theater_default":  false
}
```

`PUT /api/v1/me/player-settings`
- Auth: required. Body: any subset of the five fields; omitted fields keep
  their stored value (merge-PUT — documented explicitly in the spec text).
- Validation (400): `default_speed` must be one of
  `0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3, 3.5, 4`
  (the shared speed ladder — keep in lockstep with the frontend's
  `PLAYBACK_RATES`); `default_quality` must be `"auto"` or `"<height>p"`
  matching `^[0-9]{2,4}p$` (same pattern as the HLS rendition path segment);
  booleans are booleans.
- 200: the full effective object (GET shape).

### DB / migration sketch (`00NN_user_player_settings.{up,down}.sql`)

```sql
CREATE TABLE user_player_settings (
    user_id          uuid         PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    autoplay_next    boolean      NOT NULL DEFAULT true,
    default_speed    numeric(4,2) NOT NULL DEFAULT 1,
    default_quality  text         NOT NULL DEFAULT 'auto',
    captions_default boolean      NOT NULL DEFAULT false,
    theater_default  boolean      NOT NULL DEFAULT false,
    updated_at       timestamptz  NOT NULL DEFAULT now()
);
```

sqlc: `GetUserPlayerSettings`, `UpsertUserPlayerSettings` (INSERT ... ON
CONFLICT (user_id) DO UPDATE with COALESCE-style merge handled at the service
layer).

### Tests

- Unit/service: defaults for a fresh user; merge-PUT semantics (partial body
  leaves other fields); speed/quality validation table; per-user isolation.
- Contract: paths in `openapi.yaml`, drift guard green, 200/400/401 handler
  tests.

### Non-goals

Per-video/per-channel overrides, anonymous persistence (signed-out defaults
are a frontend concern), volume/mute persistence (session-local by design),
`loop` default (the archived spec had one; a per-user *default loop* is a
foot-gun and no consumer asks for it — dropped, recorded here).

---

## W1.C4 — Public live-listing contract (W0 audit follow-up)

Not in the original staged W1 scope — this closes a W0 audit gap recorded in the
root gate: the home "Live now" rail had **no public contract**. The only live
listing was `GET /api/v1/channels/{handle}/live` (owner-scoped, requires auth,
returns keys-free owner metadata), and video feed cards carry no live fields, so
there was no way to render currently-live streams on the home page.

### API contract (add to `api/openapi.yaml`, tag `live`)

`GET /api/v1/live` — public "Live now" listing.
- Auth: optional (`security: [{bearerAuth: []}, {}]`); it is a **public read**
  and — like the sibling `GET /live/{id}` — is **not** gated on the live feature
  toggle (that toggle guards create/ingest). When live is disabled there are
  simply no live streams to list.
- Selection: **only** streams that are BOTH `state = 'live'` AND
  `privacy = 'public'`. Unlisted/private and offline/ended streams are **never**
  listed. Ordered most-recently-started first. Paginated: `limit` (1–100,
  default 30), `offset` (>= 0).
- 200 `LivePublicListResponse`: `{ "live_streams": [ LiveStreamCard ], "limit",
  "offset" }`.
- `LiveStreamCard` = `{ id, title, description?, channel_handle,
  channel_display_name, started_at?, is_live, hls_url? }`. Stream keys are
  **never** returned.

### Truthful field decisions (never fake it)

- **started_at** — a real, new nullable column `live_streams.started_at`,
  stamped when a stream transitions offline/ended → live (in `SetLiveStreamState`)
  and cleared when it leaves live. It is the *current session's* start (not
  `created_at`, not `updated_at`). Also surfaced on the single-stream views
  (`LiveStream.started_at`, present while live).
- **is_live** — always `true` on a listing card; this is the card contract that
  can cheaply and truthfully carry it, and it gives a shared front-end card
  renderer a discriminator. The **video feed card deliberately does NOT gain an
  is_live flag**: in vidra-core live streams are a table **disjoint from
  `videos`** (unlike PeerTube's `video.isLive`), so a video card cannot
  truthfully carry it — the dedicated `/api/v1/live` listing is the live surface.
- **viewer/concurrent-viewer count — OMITTED.** No server-side counter exists
  today (no live viewer tracking, no presence). Rather than fake it, the field
  is omitted and recorded as a **wave W4 (live completion) dependency**: when a
  real concurrent-viewer counter lands, add `viewer_count` to `LiveStreamCard`.
- **thumbnail/preview — OMITTED.** Live streams have no server-generated poster
  yet; the field is omitted (the rail can fall back to the channel avatar).

### DB / migration (`0076_live_stream_started_at.{up,down}.sql`)

```sql
ALTER TABLE live_streams ADD COLUMN started_at TIMESTAMPTZ; -- nullable
CREATE INDEX live_streams_live_public_idx
    ON live_streams (started_at DESC)
    WHERE state = 'live' AND privacy = 'public'; -- partial, matches the listing
```

sqlc: new `ListLivePublicStreams` (limit/offset); `SetLiveStreamState` extended
to manage `started_at` via a CASE (stamp on offline→live, preserve on an
idempotent live re-assert, clear on leaving live); `started_at` added to the
existing Create/Update/Get/ListByChannel RETURNING lists so the domain `Stream`
carries it uniformly.

### Consumer

The **rail UI lands in the next frontend wave** (vidra-user) — this slice ships
the backend contract ahead of it, consumer-shaped (cards, not full stream
objects). No vidra-user change in this wave.

## Slice order & pairing with the vidra-user wave

1. **W1.C0** verification (unblocks/pins the quality-selector target).
2. **W1.C1** chapters → consumed by vidra-user W1.U4.
3. **W1.C2** passwords + embed privacy → consumed by vidra-user W1.U7.
4. **W1.C3** player settings → consumed by vidra-user W1.U6.
5. **W1.C4** public live listing (W0 follow-up) → rail UI in the next FE wave.

Every slice: openapi in the same commit, `make ci` green locally and on branch
CI, pushed, before its box ticks (PROGRAM §4 — no exceptions).

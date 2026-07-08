# W2 — Upload & import pipeline: vidra-core spec

**Programme:** `../../.ralph/specs/backport/PROGRAM.md` (monorepo root), wave W2 (§3 W2 row).
**Feature IDs (FEATURE_VISION.md numbering):** UPLOAD-02, 03, 07, 09, 10, 12, 13.
NOTE ON IDs: in FEATURE_VISION.md, **UPLOAD-12 = ClamAV** and **UPLOAD-13 = channel
auto-sync**. Some programme notes say "UPLOAD-13 ClamAV" — that is a mislabel; this
spec uses the FEATURE_VISION numbering throughout.

## 0. Verified current state (2026-07-08 — re-verify at execution time)

Much of the FEATURE_VISION gap map describes the ARCHIVED backups, not this repo.
The clean-room repo already ships more than the gap map assumes:

| Area | Status in vidra-core today | Evidence |
|---|---|---|
| Direct upload (UPLOAD-01) | SHIPPED | `POST /api/v1/videos/{id}/file` |
| Chunked/resumable upload (UPLOAD-02) | SHIPPED (backend) | migration `0059_uploads_and_imports`; `POST /videos/{id}/upload-session`, `GET/DELETE /uploads/{upload_id}`, `PUT /uploads/{upload_id}/chunks/{n}`, `POST /uploads/{upload_id}/complete`; expiry sweeper |
| Draft recovery (UPLOAD-03) | PARTIAL | resume contract exists (`GET /uploads/{id}`); recovery is frontend-localStorage only — no server-side session lookup by file fingerprint, no "my active uploads" listing |
| Schedule publication (UPLOAD-07) | SHIPPED end-to-end | migration `0045_videos_publish_at` (`scheduled` state + due-publish sweep in `internal/video/service.go`); `publish_at` on create/update/detail in `api/openapi.yaml`; UI + `e2e/studio-schedule.spec.ts` in vidra-user |
| URL import, direct file (part of UPLOAD-09) | SHIPPED | `import_jobs` (0059), `internal/videoimport/service.go` (async worker, backoff, dead-letter, SAFE client-visible errors), `POST/GET /videos/{id}/import`, SSRF guard `internal/urlsafety` (dial-time public-IP enforcement, defeats DNS rebinding), `HTTP_IMPORT_ALLOW_PRIVATE_URLS` test knob, `FEATURE_IMPORTS_ENABLED` flag |
| yt-dlp platform-URL import (rest of UPLOAD-09) | MISSING | no yt-dlp anywhere in the repo — this is the W2 marquee |
| Torrent import (rest of UPLOAD-09) | MISSING — **recommend DEFER** (see §6) | — |
| Batch upload (UPLOAD-10) | MISSING (backend needs only a concurrency guard; the per-video endpoints already compose) | — |
| ClamAV scan (UPLOAD-12) | SHIPPED — **DO NOT RE-PORT** | `internal/media/clamav.go` (INSTREAM), `Scanner` seam in `internal/video/service.go`, `MALWARE_SCAN_MODE` fail-closed default + `quarantined` state (0048), compose `scan` profile (`clamav/clamav:1.4`), EICAR integration tests |
| Channel auto-sync (UPLOAD-13) | MISSING | reference contract only (`specs/backport/api-reference/openapi_channel_sync.yaml`) |

## 1. Non-negotiable invariants (apply to every slice)

1. **ClamAV integration, not re-implementation.** Every new ingestion path lands
   bytes EXCLUSIVELY via `video.AttachOriginal` → `video.Process`
   (scan → probe → quarantine/transcode). Downloaded files live only in a per-job
   private tmp dir until AttachOriginal; the video stays in `processing` (never
   `published`, never servable, never linkable in feeds/search) until Process
   completes. No slice may add a second write path into blob storage.
2. **SSRF guard.** Every user-supplied URL is validated through
   `internal/urlsafety` BEFORE any subprocess sees it (scheme http/https only, no
   userinfo, no literal non-public address). See §5 for the yt-dlp residual-risk
   stance.
3. **Contract first.** `api/openapi.yaml` updated in the same slice;
   `TestOpenAPIContract` drift guard and the `openapi` workflow stay green.
4. **Job-queue shape.** New async work mirrors `transcode_jobs`/`import_jobs`:
   `pending → running → done|failed`, `next_attempt_at` partial index, exponential
   backoff, dead-letter after bounded attempts, unique partial index for
   single-active-job, SAFE client-visible `error` (never raw errors, never the URL).
5. **Tests.** Table-driven unit/service tests with fake repos + injected clients;
   integration tests behind build tags (`//go:build integration`) for anything
   needing clamd/yt-dlp/DB; `make ci` green locally and on branch CI before a box
   ticks.
6. **Migrations.** Next free number at authoring time is `0079` — take the next
   free number at execution time; always paired `.up.sql`/`.down.sql` with the
   repo's comment style.

## 2. Slices (vertical, one loop iteration each)

### W2.C0 — Opening slice: spec-in-repo + close-out of already-shipped IDs
- Copy this spec to `vidra-core/.ralph/specs/backport-w2-upload-import.md`; add the
  W2.C section to `vidra-core/.ralph/fix_plan.md`.
- UPLOAD-07 (schedule): verify shipped behavior (scheduled video parks in
  `scheduled`, sweep publishes at `publish_at`, public surfaces filter it out) with
  a pointer to existing tests; record CLOSED — no port needed.
- UPLOAD-12 (ClamAV): record the integration-only stance (§1.1) in the spec copy;
  verify the EICAR integration test still runs in the `backend-integration` lane.
- No product code. Exit: fix_plan bookkeeping + evidence notes.

### W2.C1 — yt-dlp platform-URL import (UPLOAD-09, marquee)
Extend the EXISTING per-video import (keep ownership auth + the
`import_jobs_active_video_idx` single-active-job semantics; do NOT introduce the
backup's top-level `/videos/imports/` surface).

- **Contract** (`api/openapi.yaml`, same slice):
  - `POST /api/v1/videos/{id}/import` body gains
    `resolver: "auto" | "direct" | "ytdlp"` (optional, default `auto`).
    `auto` = try HEAD/content-type as a direct media file, else fall back to
    yt-dlp when `YTDLP_IMPORT_ENABLED`. 503 with a stable code when the chosen
    resolver is disabled.
  - `import_job` view gains `resolver` and `stage`
    (`"queued" | "resolving" | "downloading" | "processing"`) so the UI can show
    honest progress; `error` stays the SAFE reason.
- **Migration** (`0079_import_jobs_resolver`):
  `ALTER TABLE import_jobs ADD COLUMN resolver TEXT NOT NULL DEFAULT 'direct'
  CHECK (resolver IN ('direct','ytdlp'))`, `ADD COLUMN stage TEXT NOT NULL DEFAULT ''`.
  Down: drop both.
- **Worker** (`internal/videoimport`): a `Resolver` interface with the existing
  direct fetch as one implementation and a new `internal/ytdlp` package as the
  other. yt-dlp runs in two phases, both sandboxed (§5):
  1. metadata: `yt-dlp -J --no-download --no-playlist <url>` → title, description,
     duration, thumbnail URL. Apply title/description to the draft video ONLY
     where the user left placeholders; fetch the thumbnail through the existing
     urlsafety-guarded client into the existing poster path.
  2. download: `yt-dlp --no-playlist --max-filesize <IMPORT_MAX_BYTES>
     --restrict-filenames -f <best mp4/webm ≤ configured height> -o <tmpdir>/media.%(ext)s <url>`
     → then `AttachOriginal` → `Process` (scan hook fires here).
- **Config** (`internal/config`): `YTDLP_IMPORT_ENABLED` (default `false`),
  `YTDLP_PATH` (default `yt-dlp`), `YTDLP_TIMEOUT` (default `15m`),
  `YTDLP_PROXY` (optional egress proxy, passed as `--proxy`; see §5),
  `YTDLP_MAX_HEIGHT` (default `1080`). Validate combinations at boot.
- **Tests**: unit — resolver selection table (auto/direct/ytdlp × flag on/off ×
  content types); argv construction table (proves no shell, fixed allowlist,
  URL is the single positional arg); safe-error mapping; stage transitions with a
  fake runner. Integration (`//go:build integration`, skips when yt-dlp absent) —
  import a local file served by `httptest` with `HTTP_IMPORT_ALLOW_PRIVATE_URLS=true`
  via a stub extractor; EICAR-through-import proof: the imported EICAR body must
  end `failed`/`quarantined` per `MALWARE_SCAN_MODE`, never `published`.
- **Compose**: document (comment in `docker-compose.yml`, same style as the clamav
  block) an optional `ytdlp` egress-proxy pairing for production; the binary
  itself ships in the api image or a pinned sidecar — pin the version, never
  `--update` at runtime.

### W2.C2 — Server-side draft recovery (UPLOAD-02/03 completion)
- **Contract**: `POST /videos/{id}/upload-session` body gains optional
  `file_fingerprint` (opaque client string, ≤128 chars; recipe documented as
  SHA-256 over size + first/last 1 MiB). New
  `GET /api/v1/me/uploads` (auth) — the caller's ACTIVE upload sessions
  (id, video_id, filename, total_size, chunk_size, received chunk count,
  `file_fingerprint`, expires_at), optional `?fingerprint=` filter. This is the
  cross-refresh/cross-device resume contract; localStorage becomes a cache, not
  the source of truth.
- **Migration** (`0080_upload_session_fingerprint`):
  `ALTER TABLE upload_sessions ADD COLUMN file_fingerprint TEXT NOT NULL DEFAULT ''`;
  partial index `(user_id, file_fingerprint) WHERE state = 'active' AND
  file_fingerprint <> ''`.
- **Tests**: sqlc query tests via fake repo; handler table tests (owner-only,
  active-only, fingerprint filter, empty list); extend the existing
  upload-session service tests for fingerprint persistence.

### W2.C3 — Batch upload guard (UPLOAD-10, backend share)
Decision: NO new tables, NO `/uploads/batch` endpoint (the backup's
`086_add_batch_uploads` shape is rejected — the per-video draft + session
endpoints already compose; batching is client orchestration).
- **Config**: `UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER` (default `5`), enforced at
  `createUploadSession` with a stable 429/422 error code the UI can queue on.
- **Contract**: document the limit + error code on `POST /videos/{id}/upload-session`.
- **Tests**: table test — N active sessions → next create rejected; cancel/complete
  frees a slot; limit disabled when 0.

### W2.C4 — Channel auto-sync (UPLOAD-13) — depends on W2.C1
- **Contract** (house naming, snake_case; reference:
  `api-reference/openapi_channel_sync.yaml` uses `/video-channel-syncs` +
  camelCase — adapt, do not copy):
  - `POST /api/v1/channel-syncs` `{channel_id, external_channel_url}` → 201
  - `GET /api/v1/channel-syncs` (mine) → list with `state`
    (`waiting_first_run | syncing | idle | failed`), `last_sync_at`, safe
    `last_error`
  - `DELETE /api/v1/channel-syncs/{id}`
  - `POST /api/v1/channel-syncs/{id}/sync-now` → 202 (manual trigger)
  - 503 stable code when `CHANNEL_SYNC_ENABLED=false` or yt-dlp import disabled.
- **Migration** (`0081_channel_syncs`): `channel_syncs`
  (id UUID PK, channel_id FK ON DELETE CASCADE, user_id FK, external_channel_url
  TEXT, state TEXT CHECK as above, last_sync_at TIMESTAMPTZ NULL, last_error TEXT
  NOT NULL DEFAULT '', next_run_at TIMESTAMPTZ, created/updated_at; UNIQUE
  (channel_id, external_channel_url)); `channel_sync_seen`
  (sync_id FK ON DELETE CASCADE, external_id TEXT, PRIMARY KEY (sync_id,
  external_id)) as the dedupe ledger.
- **Worker**: periodic tick (`CHANNEL_SYNC_INTERVAL`, default 1h; `sync-now` sets
  `next_run_at = now()`): sandboxed `yt-dlp -J --flat-playlist --playlist-end
  <CHANNEL_SYNC_BATCH=15> <external_channel_url>`; for unseen external_ids,
  create a draft video in the target channel and enqueue a `ytdlp` import job
  (W2.C1 path — scan hook included by construction); record seen ids; quota
  failures mark the item skipped-with-reason, not the sync failed.
- **Config**: `CHANNEL_SYNC_ENABLED` (default `false`), `CHANNEL_SYNC_INTERVAL`,
  `CHANNEL_SYNC_MAX_PER_USER` (default `5`), `CHANNEL_SYNC_BATCH` (default `15`).
- **Tests**: service tests with fake runner/repo (dedupe, cap per user, ownership,
  disabled flags, safe errors); handler table tests; integration test behind tag
  with a stub lister.

### W2.C5 — Thumbnail frame-pick support (UPLOAD-04 completion, backend share)
- **Contract**: `POST /api/v1/videos/{id}/thumbnail` gains an `application/json`
  body variant `{at_seconds: number}` (multipart image upload unchanged): server
  extracts the exact frame from the stored original via ffmpeg (`-ss <t> -frames:v 1`)
  and stores it through the existing poster path. 409 while the video has no
  processed original; 422 when `at_seconds` is outside the duration.
- **No migration.**
- **Tests**: handler table tests (content-type dispatch, bounds, ownership);
  ffmpeg extraction behind the existing media/ffmpeg test guards.

## 3. Suggested execution order
W2.C0 → W2.C2 → W2.C5 → W2.C3 → W2.C1 → W2.C4.
(C2/C5/C3 are small and unblock the UI wave early; C1 is the long pole; C4 depends
on C1. The vidra-user W2.U tasks mark BLOCKED-on-backend exactly like W1 did.)

## 4. Explicit non-goals
- Re-porting ClamAV (shipped), scheduled publication (shipped), direct-file URL
  import (shipped), PeerTube import (shipped separately, P18/0067).
- The backup's `/uploads/initiate|status|resume|batch` and `/videos/imports/`
  surfaces — current contracts win.
- Torrent/magnet import (deferred, §6).

## 5. Security stance — yt-dlp sandboxing
- Subprocess isolation: `exec.CommandContext` with a FIXED argv allowlist (no
  shell, no `--exec`, no `--update`, no config-file loading: pass
  `--ignore-config`), the URL as the only attacker-influenced argument, per-job
  private tmp workdir (0700) that is always removed, hard wall-clock timeout
  (`YTDLP_TIMEOUT`) with kill, `--max-filesize` + post-download size re-check
  against `IMPORT_MAX_BYTES` and quota.
- Input URL pre-validated by `internal/urlsafety` (scheme, userinfo, literal
  non-public IP) BEFORE the subprocess sees it.
- RESIDUAL RISK, stated honestly: yt-dlp performs its own HTTP fetches, so the
  dial-time SSRF guard cannot pin its connections; a malicious page could redirect
  it to internal addresses. Mitigations, in order: (1) feature is OFF by default
  (`YTDLP_IMPORT_ENABLED=false`, admin opt-in); (2) `YTDLP_PROXY` — production
  deployments SHOULD route the subprocess through an egress proxy that denies
  RFC1918/loopback/link-local (compose comment shows the pairing); (3) container
  deployments SHOULD run the api/worker with no route to internal networks.
  Document all three in the spec copy and README deploy notes.
- Version pinning: yt-dlp version is pinned in the image; upgrades are image
  rebuilds, never runtime self-update.

## 6. Torrent import — RECOMMEND DEFER (decision requested)
Deferring the torrent half of UPLOAD-09 to W6 (decentralization), because:
1. It embeds a full BitTorrent client: DHT participation and arbitrary peer
   connections in both directions. The urlsafety model (validate one URL, guard
   one dial) does not apply to swarm traffic; a private-address peer filter would
   have to be built into the torrent engine itself.
2. While downloading, the client seeds by default — the instance redistributes
   content before ClamAV has scanned it, which violates the "nothing servable
   before scan" invariant unless seeding is disabled, at which point the
   ecosystem value drops further.
3. Resource-abuse surface (disk-fill via hostile piece maps, connection
   exhaustion) is large relative to demand — yt-dlp covers effectively all real
   user requests for "import my video from elsewhere".
4. W6 already brings WebTorrent/P2P infrastructure with its own security review;
   torrent IMPORT should ride that review, not precede it.
If wired anyway, minimum bar: magnet-only, no seeding, private-IP peer filter,
per-job disk cap, admin-only flag. Until decided, the import contract stays
URL-only and the deferral lives under `## Optional / Deferred / Non-Blocking`.

## 7. Completeness contract (PROGRAM.md §4 — binds every W2.C task)
Vertical slices only; contract-first (`api/openapi.yaml` same slice, drift guard
green); a checkbox flips only with (a) unit/service tests, (b) a Playwright e2e on
the consuming UI slice, (c) for data-mutating flows a backend-backed e2e proving
the DB row changed AND the UI shows it after refetch; 1:1 feature-ID traceability;
`make ci` green locally and on branch CI. True deferrals only under
`## Optional / Deferred / Non-Blocking`.

## 8. Execution notes — W2.C1 (2026-07-08, as-built)
Adaptations from the §W2.C1 sketch, recorded for the reviewer and downstream
slices (C4 depends on the yt-dlp path):
- **Migration `0079_import_jobs_resolver`** (next free number confirmed on disk).
  The `resolver` CHECK is **widened to `('auto','direct','ytdlp')`** (the sketch
  listed only `direct|ytdlp`). Rationale: `auto` must be representable as the
  *requested* value so the worker owns resolution during the `resolving` stage —
  which is what makes the new `stage` column meaningful. The worker rewrites the
  column to the concrete `direct`/`ytdlp` it actually ran (`SetImportJobResolver`),
  so a settled row never shows `auto`. Default stays `'direct'` (pre-existing rows
  are direct imports). `stage` ∈ `{'', resolving, downloading, processing}`, cleared
  on done/failed/reschedule.
- **`auto` resolution** happens in the worker, not the request path: recognised
  video extension → `direct`; else a bounded, SSRF-guarded HEAD content-type →
  `direct` when it is a known container; else `ytdlp` when enabled, else a `direct`
  attempt that fails safely. The enqueue-time `503` fires only for an **explicit**
  `resolver=ytdlp` while disabled (synchronous, no job created).
- **`internal/ytdlp`** is the only shell-out site: pure `metadataArgs`/`downloadArgs`
  builders (unit-tested for the fixed allowlist, `--ignore-config`, `--no-playlist`,
  `--restrict-filenames`, `--max-filesize`, `--` option-terminator with the URL as
  the sole final positional, and the ABSENCE of `--exec`/`--update`/`-U`),
  `exec.CommandContext` with a minimal env (no proxy/credential inheritance), a
  hard `YTDLP_TIMEOUT`, and a private `0700` per-job workdir the resolver always
  removes (`workdirCloser`). Metadata + download run as two phases.
- **Metadata prefill** fills only EMPTY draft `title`/`description`
  (`video.Service.PrefillMetadata`). The remote **thumbnail URL is intentionally
  NOT fetched**: `Process` already extracts a poster from the imported original via
  ffmpeg, so relying on the server-side frame avoids a second attacker-influenced
  outbound fetch (consistent with the W2.C5 server-side frame-pick philosophy and
  the "minimise egress surface" stance). `ytdlp.Meta.Thumbnail` is parsed and
  available but deliberately unused by the resolver.
- **ClamAV invariant proven on the new path**: unit
  `TestImportViaYtdlpInfectedNeverPublishes` (fake scanner, runs in `make ci`) +
  integration `TestImportViaYtdlpEicarRealClamdNeverPublishes`
  (`//go:build integration`, real clamd via `CLAMAV_TEST_ADDR`, REAL EICAR bytes
  delivered through the ytdlp stub) — both assert the imported infected/EICAR body
  ends `failed`, never `published`.
- **Binary shipping**: `Dockerfile` gains an `ARG YTDLP_VERSION` that, when set,
  bakes a PINNED yt-dlp; empty (default) keeps the base image lean. `docker-compose.yml`
  documents `YTDLP_*` env + a commented `ytdlp-egress` Squid proxy pairing.
- **Consumer**: the vidra-user W2.U import UI (resolver picker + honest stage
  progress) lands in the frontend wave; the backend contract ships ahead,
  consumer-shaped (this mirrors the W1 pattern).

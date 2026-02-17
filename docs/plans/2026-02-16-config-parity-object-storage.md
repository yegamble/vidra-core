# Configuration Parity: Object Storage & Video Serving Domain Enforcement

Created: 2026-02-16
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No

> **Status Lifecycle:** PENDING → COMPLETE → VERIFIED
> **Iterations:** Tracks implement→verify cycles (incremented by verify phase)
>
> - PENDING: Initial state, awaiting implementation
> - COMPLETE: All tasks implemented
> - VERIFIED: All checks passed
>
> **Approval Gate:** Implementation CANNOT proceed until `Approved: Yes`
> **Worktree:** Set at plan creation (from dispatcher). `No` works directly on current branch (default)

## Summary

**Goal:** Achieve object storage configuration parity with PeerTube's production.yaml.example. Add per-category S3 prefixes and CDN base URLs, upload ACL settings, private file proxying, retry logic, configurable CSP, and video serving domain enforcement.

**Architecture:** Extend the existing config/storage/middleware layers with new sub-structs (ObjectStorageConfig, CSPConfig) to avoid bloating the main Config struct. Use shared-bucket-with-prefixes model for per-category storage. Auto-include CDN domains in CSP directives.

**Tech Stack:** Go (existing), AWS SDK v2 (existing), Chi middleware (existing)

## Scope

### In Scope

- Object storage per-category config: prefix and base_url for streaming_playlists, web_videos, user_exports, original_video_files, captions
- Upload ACL settings (public/private, null support for providers like Backblaze)
- Private file proxy (proxify_private_files) — proxy S3 objects through the server instead of redirect
- Static files private auth toggle
- Configurable max_upload_part size (replace hardcoded 10MB)
- Configurable max_request_attempts (retry logic)
- S3 path style config via env var (field exists but not loaded)
- store_live_streams config for streaming playlists
- Configurable CSP (enabled, report_only, report_uri) with auto-inclusion of CDN domains
- Update .env.example with all new variables

### Out of Scope

- Multi-bucket support (different bucket names per category) — deferred, shared bucket model only
- S3 region per category — shared region only
- Separate VIDEOS_DOMAIN config — CDN base_url per category handles this
- Setup wizard changes — S3 config via .env only for now
- Changes to backup S3 config (separate concern)
- Frontend/client changes

## Prerequisites

- None — all changes extend existing code

## Context for Implementer

> This section is critical for cross-session continuity. Write it for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Config loading: Follow the pattern in `internal/config/config_load.go:17` — `loadCommonFields()` reads env vars with `getEnvOrDefault()`, `getBoolEnv()`, `getIntEnv()` helpers
  - Config struct: Main struct at `internal/config/config.go:13` uses flat fields. We'll add sub-structs as embedded fields.
  - S3 backend: `internal/storage/s3_backend.go:32` — `S3Config` struct passed to `NewS3Backend()`
  - Security middleware: `internal/middleware/security.go:26` — `SecurityHeaders()` returns middleware closure
  - HLS handler: `internal/httpapi/handlers/video/hls_s3_handler.go:19` — serves HLS with S3 fallback
  - S3 migration: `internal/usecase/migration/s3_migration_service.go:57` — hardcodes `videos/{videoID}/hls/{relPath}` keys

- **Conventions:**
  - Env vars: SCREAMING_SNAKE_CASE (e.g., `S3_UPLOAD_ACL_PUBLIC`)
  - Config helpers: `getEnvOrDefault()`, `getBoolEnv()`, `getIntEnv()`, `getInt64Env()`, `getFloat64Env()`, `getStringSliceEnv()` in `config_helpers.go`
  - Error wrapping: `fmt.Errorf("context: %w", err)`
  - Context first parameter for all functions

- **Key files:**
  - `internal/config/config.go` (285 lines) — main Config struct, NEAR 300-LINE LIMIT
  - `internal/config/config_load.go` (281 lines) — env loading, NEAR 300-LINE LIMIT
  - `internal/config/config_helpers.go` — parsing helpers
  - `internal/storage/s3_backend.go` (286 lines) — S3 implementation, NEAR 300-LINE LIMIT
  - `internal/storage/paths.go` — local storage path helpers
  - `internal/middleware/security.go` (216 lines) — security headers including CSP
  - `internal/httpapi/handlers/video/hls_s3_handler.go` (132 lines) — HLS S3 serving
  - `internal/usecase/migration/s3_migration_service.go` (308 lines) — S3 migration, OVER LIMIT

- **Gotchas:**
  - config.go and config_load.go are both near the 300-line limit. New config must go in separate files.
  - s3_backend.go is at 286 lines. ACL/retry changes must be minimal or extracted.
  - s3_migration_service.go is already at 308 lines (over the 300-line limit).
  - The S3Backend `pathStyle` field exists in S3Config but is never loaded from env vars.
  - Upload part size is hardcoded to 10MB at s3_backend.go:76 — needs to become configurable.
  - ACL for Backblaze must support `null` (no ACL set at all) vs `"public-read"` vs `"private"`.
  - CSP is hardcoded with `media-src 'self' blob:` — doesn't allow CDN domains.

- **Domain context:**
  - PeerTube uses shared bucket + prefix model (one bucket with `streaming-playlists/`, `web-videos/`, etc.)
  - Backblaze B2 doesn't support object ACL — must use `null` for upload_acl
  - Private file proxy means server fetches from S3 and streams to user, instead of 302 redirect to S3 URL
  - CDN base_url replaces the S3 endpoint in generated URLs (e.g., `https://cdn.example.com/file/bucket` instead of `https://s3.region.backblazeb2.com/bucket`)

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Add ObjectStorageConfig and CSPConfig to config layer
- [x] Task 2: Update S3Backend with ACL, retry, and configurable part size
- [x] Task 3: Add per-category S3 key and URL generation
- [x] Task 4: Make CSP middleware configurable with CDN domain auto-inclusion
- [x] Task 5: Add S3 private file proxy for private content
- [x] Task 6: Wire config into app.go and update .env.example

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add ObjectStorageConfig and CSPConfig to config layer

**Objective:** Create new config sub-structs for object storage and CSP settings, loaded from environment variables. This provides the foundation for all subsequent tasks.

**Dependencies:** None

**Files:**

- Create: `internal/config/object_storage.go` (~90 lines)
- Create: `internal/config/csp_config.go` (~30 lines)
- Create: `internal/config/object_storage_test.go`
- Create: `internal/config/csp_config_test.go`
- Modify: `internal/config/config.go` (add 3 embedded struct fields)
- Modify: `internal/config/config_load.go` (call new loaders, ~5 lines added)

**Key Decisions / Notes:**

- `ObjectStorageConfig` struct contains:
  - `UploadACLPublic string` — ACL for public/unlisted videos (default: `"public-read"`, use `"null"` or empty for providers like Backblaze)
  - `UploadACLPrivate string` — ACL for private/internal videos (default: `"private"`, use `"null"` or empty for Backblaze)
  - `ProxifyPrivateFiles bool` — proxy private S3 objects through server (default: true)
  - `MaxUploadPart string` — max upload part size (default: `"100MB"`, parsed with ParseByteSize)
  - `MaxRequestAttempts int` — retry attempts for S3 requests (default: 3)
  - `PathStyle bool` — use path-style S3 URLs (default: false)
  - Per-category settings (prefix and base_url): `StreamingPlaylistsPrefix`, `StreamingPlaylistsBaseURL`, `WebVideosPrefix`, `WebVideosBaseURL`, `UserExportsPrefix`, `UserExportsBaseURL`, `OriginalVideoFilesPrefix`, `OriginalVideoFilesBaseURL`, `CaptionsPrefix`, `CaptionsBaseURL`
  - `StoreLiveStreams bool` — store live streams in object storage (default: false)
- `CSPConfig` struct contains: `Enabled bool` (default: true), `ReportOnly bool` (default: false), `ReportURI string`
- `StaticFilesPrivateAuth bool` added to main Config (default: true)
- Env var naming: `S3_UPLOAD_ACL_PUBLIC`, `S3_PROXIFY_PRIVATE_FILES`, `S3_STREAMING_PLAYLISTS_PREFIX`, `S3_STREAMING_PLAYLISTS_BASE_URL`, `CSP_ENABLED`, `CSP_REPORT_ONLY`, `CSP_REPORT_URI`, `STATIC_FILES_PRIVATE_AUTH`
- Default prefixes match PeerTube: `streaming-playlists/`, `web-videos/`, `user-exports/`, `original-video-files/`, `captions/`
- ACL `"null"` or empty string means: don't set ACL on upload at all (for Backblaze compatibility)
- Reuse existing helpers from config_helpers.go

**Definition of Done:**

- [ ] All new env vars loaded with correct defaults
- [ ] ObjectStorageConfig struct has all per-category prefix/base_url fields
- [ ] `"null"` or empty ACL string means no ACL is applied (tested)
- [ ] MaxUploadPart parsed correctly from human-readable format (e.g., "100MB")
- [ ] Config test validates all new fields load from env vars

**Verify:**

- `go test ./internal/config/... -run TestObjectStorage -v` — object storage config tests pass
- `go test ./internal/config/... -run TestCSP -v` — CSP config tests pass
- `go build ./...` — project compiles

---

### Task 2: Update S3Backend with ACL, retry, and configurable part size

**Objective:** Update the S3Backend to support configurable upload ACL, max upload part size, and retry logic with max request attempts.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/storage/s3_backend.go` (update S3Config struct, NewS3Backend, Upload method)
- Modify: `internal/storage/s3_backend_test.go`

**Key Decisions / Notes:**

- Add to `S3Config`: `UploadACLPublic string`, `UploadACLPrivate string`, `MaxUploadPart int64` (in bytes), `MaxRequestAttempts int`
- `Upload()` method gets a new `acl string` parameter or we add `UploadWithACL()` method — prefer adding ACL to the existing `Upload` call via an options pattern or a new field in S3Config that selects ACL
- For ACL: when value is empty or `"null"`, do NOT set ACL on the PutObjectInput. Otherwise set `ACL: types.ObjectCannedACL(acl)`.
- For retry: Use AWS SDK v2 `retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = maxAttempts })` in the aws.Config
- For part size: Replace hardcoded `10 * 1024 * 1024` with `cfg.MaxUploadPart` (default to 10MB if 0)
- Must keep s3_backend.go under 300 lines — currently at 286. Be surgical: modify existing functions, don't add new public methods.
- The `StorageBackend` interface doesn't change — ACL is an internal S3 detail.

**Definition of Done:**

- [ ] S3Config accepts MaxUploadPart, MaxRequestAttempts, UploadACLPublic, UploadACLPrivate
- [ ] Upload uses configurable part size instead of hardcoded 10MB
- [ ] AWS SDK retry configured with MaxRequestAttempts
- [ ] Empty/null ACL values result in no ACL header on upload
- [ ] s3_backend.go stays under 300 lines
- [ ] All existing tests continue to pass

**Verify:**

- `go test ./internal/storage/... -v` — all storage tests pass
- `go build ./...` — project compiles

---

### Task 3: Add per-category S3 key and URL generation

**Objective:** Create a CategoryStorage helper that generates S3 keys with category-specific prefixes and URLs with category-specific CDN base URLs. This replaces hardcoded `videos/{id}/hls/{path}` patterns.

**Dependencies:** Task 1

**Files:**

- Create: `internal/storage/s3_categories.go` (~100 lines)
- Create: `internal/storage/s3_categories_test.go`

**Key Decisions / Notes:**

- Define `StorageCategory` string type with constants: `CategoryStreamingPlaylists`, `CategoryWebVideos`, `CategoryUserExports`, `CategoryOriginalVideoFiles`, `CategoryCaptions`
- `CategoryConfig` struct: `Prefix string`, `BaseURL string`
- `CategoryStorage` struct holds a map of category → CategoryConfig, plus a reference to the S3Backend
- Key generation: `func (cs *CategoryStorage) Key(category StorageCategory, path string) string` — prepends the category prefix
- URL generation: `func (cs *CategoryStorage) URL(category StorageCategory, path string) string` — uses base_url if set, otherwise falls back to S3Backend.GetURL()
- Constructor: `NewCategoryStorage(s3Backend *S3Backend, configs map[StorageCategory]CategoryConfig)`
- Follow pattern in `paths.go` for consistent naming

**Definition of Done:**

- [ ] CategoryStorage generates correct S3 keys with prefixes (e.g., `web-videos/video123.mp4`)
- [ ] CategoryStorage generates correct URLs with CDN base_url override
- [ ] Falls back to S3Backend.GetURL() when no base_url configured
- [ ] All five categories have correct default prefixes matching PeerTube
- [ ] Table-driven tests cover all categories and edge cases (empty prefix, empty base_url, trailing slashes)

**Verify:**

- `go test ./internal/storage/... -run TestCategory -v` — category storage tests pass
- `go build ./...` — project compiles

---

### Task 4: Make CSP middleware configurable with CDN domain auto-inclusion

**Objective:** Update the SecurityHeaders middleware to accept configuration, making CSP togglable (enabled/report-only) and automatically including S3/CDN base_url domains in the appropriate CSP directives.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/middleware/security.go` (update SecurityHeaders signature, add CSP config logic)
- Modify: `internal/middleware/security_test.go`

**Key Decisions / Notes:**

- Change `SecurityHeaders()` to `SecurityHeaders(cfg SecurityConfig)` where `SecurityConfig` has: `CSPEnabled bool`, `CSPReportOnly bool`, `CSPReportURI string`, `CDNDomains []string`, `StaticFilesPrivateAuth bool`
- When CSP is disabled, skip setting the header entirely
- When CSP is report-only, use `Content-Security-Policy-Report-Only` header instead
- Auto-include CDN domains: extract hostnames from CDN base URLs, add to `media-src`, `img-src`, and `connect-src` directives
- If CSPReportURI is set, add `report-uri` directive
- Parse CDN domains from base_url values at startup (extract scheme+host), not per-request
- Example: if `S3_WEB_VIDEOS_BASE_URL=https://cdn.sizetube.com/file/sizetube`, add `https://cdn.sizetube.com` to media-src
- Keep the function at its current location in security.go — no new files needed
- Update all callers of `SecurityHeaders()` to pass config (check `app.go` or router setup)

**Definition of Done:**

- [ ] SecurityHeaders accepts config parameter
- [ ] CSP disabled = no CSP header set
- [ ] CSP report-only uses `Content-Security-Policy-Report-Only` header
- [ ] CDN domains automatically added to media-src, img-src, connect-src
- [ ] Report URI included when configured
- [ ] All callers updated to pass config
- [ ] Tests verify all three modes (enabled, report-only, disabled)

**Verify:**

- `go test ./internal/middleware/... -run TestSecurity -v` — security middleware tests pass
- `go build ./...` — project compiles

---

### Task 5: Add S3 private file proxy for private content

**Objective:** When proxify_private_files is enabled, proxy private S3 objects through the server instead of redirecting to S3 URLs. This ensures private content is never directly exposed via S3 URLs.

**Dependencies:** Task 1, Task 2

**Files:**

- Modify: `internal/httpapi/handlers/video/hls_s3_handler.go` (add proxy logic for private files)
- Modify: `internal/httpapi/handlers/video/hls_s3_handler_unit_test.go`

**Key Decisions / Notes:**

- Current behavior (hls_s3_handler.go:57-68): private videos get a signed URL redirect
- New behavior when `ProxifyPrivateFiles == true`: instead of redirect, download from S3 via `s3Backend.Download()` and stream to the response writer with `io.Copy()`
- When `ProxifyPrivateFiles == false`: keep current signed URL redirect behavior
- When `StaticFilesPrivateAuth == false`: skip the privacy check entirely (allow public access to all files)
- Set appropriate Content-Type headers based on file extension (reuse content type logic)
- Set Cache-Control headers: private files get `private, no-cache`, public files keep current caching
- Stream response — don't buffer the entire file in memory (use `io.Copy` from S3 ReadCloser to ResponseWriter)
- Close the S3 response body with defer
- The HLSHandlerWithS3 function signature needs the new config fields — pass ObjectStorageConfig or relevant booleans

**Definition of Done:**

- [ ] Private files proxied through server when proxify_private_files=true
- [ ] Private files get signed URL redirect when proxify_private_files=false
- [ ] Response streams from S3 without full buffering
- [ ] Correct Content-Type and Cache-Control headers for proxied responses
- [ ] StaticFilesPrivateAuth=false disables privacy check
- [ ] Test covers proxy path, redirect path, and auth-disabled path

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -run TestHLS -v` — HLS handler tests pass
- `go build ./...` — project compiles

---

### Task 6: Wire config into app.go and update .env.example

**Objective:** Wire all new config into the application startup, update the S3Backend construction to use new config fields, update security middleware calls, and add all new env vars to .env.example.

**Dependencies:** Task 1, Task 2, Task 3, Task 4, Task 5

**Files:**

- Modify: `internal/app/app.go` (update S3Backend construction, SecurityHeaders call, CategoryStorage creation)
- Modify: `.env.example` (add all new env vars with documentation)

**Key Decisions / Notes:**

- In app.go, where SecurityHeaders() is called, pass the new SecurityConfig with CDN domains extracted from ObjectStorageConfig base_url fields
- Create a helper to extract unique CDN domains from all base_url values: parse URL, keep scheme+host
- Where S3Backend is constructed (if it's in app.go), update S3Config with new fields from ObjectStorageConfig
- Create CategoryStorage instance and make it available to handlers
- The HLSHandlerWithS3 needs access to ProxifyPrivateFiles and StaticFilesPrivateAuth — pass via config or as explicit parameters
- .env.example should group new vars under a clear "# Object Storage (S3-compatible)" section with comments matching PeerTube's documentation style
- Include Backblaze B2 example values in comments

**Definition of Done:**

- [ ] SecurityHeaders called with full config including CDN domains
- [ ] S3Backend constructed with ACL, retry, and part size from config
- [ ] CategoryStorage created and available to handlers
- [ ] .env.example has all new vars with clear documentation
- [ ] `make build` succeeds
- [ ] `make test` passes (existing tests don't break)

**Verify:**

- `go build ./cmd/server` — binary builds successfully
- `go test ./internal/app/... -v` — app tests pass
- `go test ./... -short -count=1 2>&1 | tail -5` — all short tests pass
- `grep -c 'S3_' .env.example` — confirms new vars are present

---

## Testing Strategy

- **Unit tests**: Each new file gets table-driven tests (config loading, key generation, URL generation, CSP directive building)
- **Integration tests**: None needed — all changes are config/middleware layer, tested via unit tests
- **Manual verification**: After implementation, set env vars for a Backblaze B2 config and verify `go build` succeeds and config loads correctly

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Config files exceed 300-line limit | High | Med | New config goes in separate files (object_storage.go, csp_config.go), not in config.go |
| S3Backend changes break existing callers | Med | High | S3Config new fields have zero-value defaults that preserve existing behavior (0 = use existing hardcoded values) |
| CSP middleware signature change breaks callers | Med | High | Find all callers with Grep before changing; update all in same task |
| ACL null handling differs across S3 providers | Low | Med | Test with empty string and "null" string; document in .env.example |
| Private file proxy increases server load | Med | Med | Document that proxify can be disabled; streaming (io.Copy) avoids memory buffering |

## Open Questions

- None — all design decisions resolved.

### Deferred Ideas

- Multi-bucket support (different bucket names per category) — could be added later by extending ObjectStorageConfig
- Setup wizard UI for S3 object storage configuration
- S3 region per category for multi-region deployments
- CDN cache invalidation integration
- Video serving via dedicated VIDEOS_DOMAIN with separate nginx/reverse proxy config

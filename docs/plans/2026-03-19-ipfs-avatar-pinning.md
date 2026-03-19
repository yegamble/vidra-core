# IPFS Avatar Pinning Implementation Plan

Created: 2026-03-19
Status: COMPLETE
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Ensure uploaded avatars are properly pinned to IPFS for public retrieval, include gateway URLs (public + local) in the API response, add a proxy endpoint for local IPFS retrieval, and verify pinned content is retrievable after upload.

**Architecture:** Extend the existing avatar upload flow with post-pin verification, add computed gateway URL fields to the User JSON response, add `IPFS_LOCAL_GATEWAY_URL` config, and add a new `GET /api/v1/avatars/{cid}` proxy endpoint.

**Tech Stack:** Go (Chi router), IPFS Kubo HTTP API, existing `internal/ipfs` client

## Scope

### In Scope

- Add `IPFS_LOCAL_GATEWAY_URL` config field (default: `http://localhost:8080`)
- Add gateway URL fields (`gateway_url`, `local_gateway_url`) to avatar JSON response
- Add post-upload verification that confirms pinned content is retrievable from local IPFS node
- Add `GET /api/v1/avatars/{cid}` proxy endpoint to serve avatars from local IPFS/local storage
- Unit tests for all new functionality

### Out of Scope

- Public gateway verification (too slow for synchronous upload)
- IPFS Cluster status monitoring
- Avatar CDN caching layer
- WebP-specific proxy endpoint (clients use webp_ipfs_cid + same gateway URL pattern)

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Avatar upload handler: `internal/httpapi/handlers/auth/avatar.go:53-136`
  - IPFS client with Pin/Cat: `internal/ipfs/client.go:229-256` (Pin), `449-472` (Cat)
  - Config loading: `internal/config/config_load.go:17-37` (IPFS fields pattern)
  - Route registration: `internal/httpapi/routes.go:240-243` (avatar route)
  - User JSON serialization: `internal/domain/user.go:38-68` (custom MarshalJSON)
  - Test helpers: `internal/httpapi/handlers/auth/test_helpers_test.go:30-60` (NewServer for tests)
  - Test hooks: `internal/httpapi/handlers/auth/avatar.go:43-49` (testIPFSAdd, testIPFSPin, etc.)

- **Conventions:**
  - Config: env var → `GetEnvOrDefault()` or `GetBoolEnv()` in `loadCommonFields()`
  - Error handling: Use `domain.NewDomainError("CODE", "message")` for business errors
  - HTTP responses: Use `shared.WriteJSON()`, `shared.WriteError()` envelope pattern
  - Tests: Table-driven with testify assertions, test hooks for IPFS mocking

- **Key files:**
  - `internal/httpapi/handlers/auth/avatar.go` — Avatar upload, IPFS add/pin logic
  - `internal/httpapi/handlers/auth/handlers.go` — AuthHandlers struct definition
  - `internal/domain/user.go` — User model with Avatar struct and MarshalJSON
  - `internal/config/config.go` — Config struct (add new fields here)
  - `internal/config/config_load.go` — Config loading from env vars
  - `internal/ipfs/client.go` — IPFS client (Cat method for verification)
  - `internal/httpapi/routes.go` — Route registration
  - `internal/repository/user_repository.go` — SetAvatarFields, GetByID with avatar join

- **Gotchas:**
  - Avatar handlers in `auth` package use raw HTTP client for IPFS (not `internal/ipfs/client.go`). The `ipfsAPI` field is a plain string URL, not a Client instance.
  - The `AuthHandlers` struct holds `ipfsAPI` and `ipfsClusterAPI` as strings, plus `cfg *config.Config`.
  - `ipfsAdd()` already uses `pin=true` on add, then `pinToIPFS()` calls `pin/add` again — this is redundant but harmless (idempotent).
  - Test hooks (`testIPFSAdd`, `testIPFSPin`, etc.) are package-level vars — tests mock IPFS by setting these, not by injecting interfaces.
  - The `generated.User` type in `internal/generated/types.go` is auto-generated from OpenAPI — we should NOT edit it. The domain `User.MarshalJSON` controls the actual JSON output.

- **Domain context:**
  - Avatars are stored both locally (filesystem) and on IPFS (when configured).
  - The `user_avatars` table has: `id`, `user_id`, `file_id`, `ipfs_cid`, `webp_ipfs_cid`, `created_at`, `updated_at`.
  - IPFS gateway URLs: config has `IPFSGatewayURLs` (default: ipfs.io, dweb.link, cloudflare-ipfs.com).
  - The standard IPFS gateway pattern is `{gateway_url}/ipfs/{cid}`.
  - Kubo's local gateway default port is 8080 (separate from API port 5001).

## Runtime Environment

- **Start command:** `make run` or `go run cmd/server/main.go`
- **Port:** Configured via `PORT` env var (default varies)
- **Health check:** `GET /health`
- **IPFS dependency:** Local Kubo node at `IPFS_API` (default: `http://localhost:5001`)

## Assumptions

- The local IPFS Kubo node exposes a gateway on a separate port (default 8080) — supported by standard Kubo configuration — Tasks 1, 3, 4 depend on this
- `internal/ipfs/client.go`'s `Cat()` method can be used for verification (reads content by CID) — supported by `client.go:449-472` — Task 2 depends on this
- The `cfg *config.Config` is available in `AuthHandlers` for reading gateway URLs — supported by `handlers.go:24` — Tasks 2, 3 depend on this
- CID validation exists in `internal/ipfs/cid_validation.go` and can be used for the proxy endpoint — Task 4 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Post-upload verification adds latency | Medium | Low | Verify only via local node (fast, <100ms). Skip verification if IPFS unavailable. |
| Local gateway URL not reachable by external clients | Medium | Low | Document that `local_gateway_url` is for self-hosted instances. Public `gateway_url` is the default for external use. |
| IPFS Cat timeout during verification | Low | Medium | Set 5-second timeout on verification. Log warning and continue if verification fails (non-blocking). |
| CID injection in proxy endpoint | Low | High | Validate CID format using existing `ipfs.ValidateCID()` before making any IPFS API calls. |

## Goal Verification

### Truths

1. When an avatar is uploaded with IPFS configured, the API response includes `gateway_url` and `local_gateway_url` fields with properly constructed URLs
2. The CID returned in `gateway_url` can be used to retrieve the image from a public IPFS gateway (e.g., `https://ipfs.io/ipfs/{cid}`)
3. When the local IPFS node is running, `GET /api/v1/avatars/{cid}` returns the image data with correct content type
4. After upload, the pinned content is verified as retrievable from the local IPFS node before the response is sent
5. When IPFS is not configured, the avatar upload still works (local-only) without gateway URLs
6. The proxy endpoint rejects invalid CIDs and returns appropriate error codes

### Artifacts

1. `internal/config/config.go` — `IPFSLocalGatewayURL` field
2. `internal/config/config_load.go` — Loading `IPFS_LOCAL_GATEWAY_URL` env var
3. `internal/domain/user.go` — Updated `MarshalJSON` with gateway URL fields
4. `internal/httpapi/handlers/auth/avatar.go` — Post-upload verification + gateway URL computation
5. `internal/httpapi/handlers/auth/avatar_proxy.go` — New proxy endpoint
6. `internal/httpapi/routes.go` — New route registration
7. Test files with verification of all above behaviors

## Progress Tracking

- [x] Task 1: Add IPFS local gateway config
- [x] Task 2: Add post-upload IPFS verification
- [x] Task 3: Add gateway URLs to avatar response
- [x] Task 4: Add avatar proxy endpoint
      **Total Tasks:** 4 | **Completed:** 4 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add IPFS Local Gateway Config

**Objective:** Add `IPFS_LOCAL_GATEWAY_URL` config field so the system knows the local IPFS gateway address for constructing avatar URLs and proxying requests.

**Dependencies:** None

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/config_load.go`
- Modify: `.env.example`
- Test: `internal/config/config_load_test.go`

**Key Decisions / Notes:**

- Add `IPFSLocalGatewayURL string` to Config struct (follows pattern at `config.go:34-49`)
- Load via `GetEnvOrDefault("IPFS_LOCAL_GATEWAY_URL", "http://localhost:8080")` in `loadCommonFields()` (follows pattern at `config_load.go:19`)
- Default `http://localhost:8080` matches Kubo's default gateway port

**Definition of Done:**

- [ ] `IPFSLocalGatewayURL` field exists in Config struct
- [ ] Loaded from env var with correct default
- [ ] `.env.example` documents the new variable
- [ ] Config test verifies default value and custom override

**Verify:**

- `go test ./internal/config/... -run TestLoad -v`

---

### Task 2: Add Post-Upload IPFS Verification

**Objective:** After pinning an avatar to IPFS, verify the content is actually retrievable from the local node before returning success. This ensures the pin succeeded and the content is accessible.

**Dependencies:** None (uses existing IPFS API calls)

**Files:**

- Modify: `internal/httpapi/handlers/auth/avatar.go`
- Test: `internal/httpapi/handlers/auth/avatar_handler_test.go`

**Key Decisions / Notes:**

- Add a `verifyIPFSContent(cid string) error` method on `AuthHandlers`
- Use `context.WithTimeout(r.Context(), 5*time.Second)` derived from the request context, passed via `http.NewRequestWithContext`, so that if the parent request is cancelled the verification is also cancelled (matches codebase's context-first pattern)
- Use the IPFS `/api/v0/cat` endpoint (same pattern as `internal/ipfs/client.go:449-472`)
- Read first 1 byte to confirm content is accessible (don't download entire file)
- Add a test hook `testIPFSVerify` (follows pattern of existing `testIPFSAdd`, `testIPFSPin` at `avatar.go:44-48`)
- If verification fails: log warning but DON'T fail the upload (content was pinned, just might not be immediately retrievable)
- Call verification after `uploadAvatarToIPFS()` succeeds, before DB write

**Definition of Done:**

- [ ] Verification function exists and is called after successful IPFS upload
- [ ] Verification uses 5-second timeout, reads minimal data
- [ ] Verification failure logs warning but does not fail the upload
- [ ] Test confirms verification is called on successful upload
- [ ] Test confirms upload succeeds even if verification fails

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -run TestUploadAvatar -v`

---

### Task 3: Add Gateway URLs to Avatar Response

**Objective:** Include `gateway_url` and `local_gateway_url` fields in the avatar JSON response so clients can directly fetch avatars from IPFS gateways.

**Dependencies:** Task 1 (needs `IPFSLocalGatewayURL` config)

**Files:**

- Modify: `internal/domain/user.go`
- Modify: `internal/httpapi/handlers/auth/avatar.go`
- Test: `internal/httpapi/handlers/auth/avatar_handler_test.go`

**Key Decisions / Notes:**

- Add `GatewayURL` and `LocalGatewayURL` fields to the `Avatar` struct in `domain/user.go` (not DB-stored, computed at response time)
- These should be `string` fields (not `sql.NullString`) since they're computed, not from DB
- Update `User.MarshalJSON()` to include `gateway_url` and `local_gateway_url` in the avatar payload
- In the upload handler, after IPFS upload + verification, compute gateway URLs from config and set them on the user's avatar before returning
- Gateway URL format: `{first_IPFSGatewayURLs}/ipfs/{cid}` for public, `{IPFSLocalGatewayURL}/ipfs/{cid}` for local
- **Guard against empty `IPFSGatewayURLs` slice**: if `len(h.cfg.IPFSGatewayURLs) == 0`, omit `gateway_url` field entirely (prevents index-out-of-range panic)
- Only include gateway URLs when CID is present (not for local-only avatars)
- The `User.MarshalJSON()` needs the config to compute URLs — instead, compute the URLs in the handler and set them on the Avatar struct before serialization
- **Must update the anonymous struct literal inside MarshalJSON** (at `domain/user.go:41-68`) — adding fields to the Avatar struct alone is not sufficient; the inline struct that controls JSON output must also be extended with `gateway_url` and `local_gateway_url` json tags

**Definition of Done:**

- [ ] Avatar JSON includes `gateway_url` when IPFS CID is present
- [ ] Avatar JSON includes `local_gateway_url` when IPFS CID and local gateway are configured
- [ ] Gateway URLs are correctly constructed using config values
- [ ] `gateway_url` field is omitted when `IPFSGatewayURLs` slice is empty (no panic)
- [ ] When no IPFS CID, gateway URL fields are omitted
- [ ] MarshalJSON anonymous struct literal in `domain/user.go` is updated to include `gateway_url` and `local_gateway_url` json fields
- [ ] Test verifies gateway URLs appear in upload response
- [ ] Test directly checks JSON output of `User.MarshalJSON()` when Avatar has GatewayURL set

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -run TestUploadAvatar -v`

---

### Task 4: Add Avatar Proxy Endpoint

**Objective:** Add `GET /api/v1/avatars/{cid}` endpoint that serves avatar images by fetching from the local IPFS node, enabling retrieval without exposing the raw IPFS API.

**Dependencies:** Task 1 (needs config), Task 2 (uses similar IPFS fetch pattern)

**Files:**

- Create: `internal/httpapi/handlers/auth/avatar_proxy.go`
- Modify: `internal/httpapi/routes.go`
- Test: `internal/httpapi/handlers/auth/avatar_proxy_test.go`

**Key Decisions / Notes:**

- New handler `ServeAvatarFromIPFS(w http.ResponseWriter, r *http.Request)` on `AuthHandlers`
- Extract CID from URL param: `chi.URLParam(r, "cid")`
- Validate CID using `ipfs.ValidateCID()` from `internal/ipfs/cid_validation.go`
- Fetch via IPFS API `/api/v0/cat?arg={cid}` with 30-second timeout
- Detect content type from first 512 bytes using `http.DetectContentType()`
- Set `Content-Type`, `Cache-Control: public, max-age=31536000, immutable` (IPFS content is immutable)
- Stream response body (don't buffer entire file)
- Unauthenticated endpoint (avatars are public)
- Route: `r.Get("/api/v1/avatars/{cid}", authHandlers.ServeAvatarFromIPFS)` — registered outside auth middleware
- If IPFS is not configured or fetch fails, return 503 Service Unavailable

**Definition of Done:**

- [ ] `GET /api/v1/avatars/{valid-cid}` returns image data with correct Content-Type
- [ ] Invalid CIDs return 400 Bad Request
- [ ] When `ipfsAPI` config field is empty string (not configured), proxy returns 503 with error code `IPFS_NOT_CONFIGURED`
- [ ] When `ipfsAPI` is configured but the fetch request fails (network error/timeout), proxy returns 503 with error code `IPFS_UNAVAILABLE`
- [ ] Response includes immutable cache headers
- [ ] Route is registered and accessible without authentication
- [ ] Test verifies happy path, invalid CID, IPFS-not-configured, and IPFS-fetch-failure cases separately

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -run TestServeAvatar -v`

## Open Questions

_None — all design decisions resolved._

### Deferred Ideas

- Public gateway health check after avatar upload (async worker)
- Avatar CDN integration for high-traffic deployments
- Retry logic for IPFS uploads that fail verification

# PeerTube Video Pipeline Feature Parity Audit — Implementation Plan

Created: 2026-03-19
Status: PENDING
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Close critical gaps in Athena's video pipeline (upload → encode → store → serve) vs PeerTube, ensuring videos work end-to-end across local, S3, IPFS, and ATProto storage backends. Fix subtitle generation for remote storage.

**Architecture:** The encoding service currently writes HLS segments locally and optionally uploads to IPFS. S3 is only available via a separate post-hoc migration service. Stream handlers only read `OutputPaths` (local filesystem) and fall back to serving mock/fake playlists. This plan adds direct S3 upload to the encoding pipeline, fixes the stream handler to serve from S3URLs, removes the mock playlist fallback, and fixes caption generation for non-local videos.

**Tech Stack:** Go 1.24, Chi router, PostgreSQL/SQLX, AWS SDK v2 (S3), IPFS client, FFmpeg

## Scope

### In Scope

- Fix stream handler to serve S3-migrated videos (read `S3URLs`)
- Remove mock playlist fallback (return proper 404/processing status)
- Add direct S3 upload of HLS segments/playlists during encoding
- Fix HLS segment handler for S3-stored segments
- Fix caption generation for S3-migrated source videos
- Fix IPFS backend incomplete methods (`Copy`, `GetMetadata`)
- Fix 3 failing IPFS backend tests
- Add `LocalBackend` implementing `StorageBackend` interface

### Out of Scope

- PeerTube resumable upload protocol (Athena's chunked upload is functionally equivalent)
- Video studio editing (cut/intro/outro)
- Per-resolution file deletion endpoints (`DELETE /videos/{id}/files/{fileId}`)
- ATProto video blob upload (current external-embed approach is valid)
- WebTorrent integration changes
- Frontend changes

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Storage backends implement `storage.StorageBackend` interface (`internal/storage/backend.go:10`)
  - Encoding service is a worker loop in `internal/usecase/encoding/service.go:75`
  - Stream handler chain: `validateStreamRequest → fetchVideo → tryServeFromOutputPaths → tryServeFromLocalDirectory → serveMockPlaylist` in `internal/httpapi/handlers/video/stream_handlers.go:244`
  - S3 migration pattern in `internal/usecase/migration/s3_migration_service.go:57`

- **Conventions:**
  - Error wrapping: `fmt.Errorf("context: %w", err)`
  - Domain errors: `domain.NewDomainError("CODE", "message")`
  - Constructor DI, no globals
  - Table-driven tests with testify

- **Key files:**
  - `internal/storage/backend.go` — StorageBackend interface
  - `internal/storage/s3_backend.go` — S3 implementation
  - `internal/storage/ipfs_backend.go` — IPFS implementation (incomplete)
  - `internal/usecase/encoding/service.go` — Encoding pipeline
  - `internal/httpapi/handlers/video/stream_handlers.go` — Video streaming
  - `internal/usecase/captiongen/service.go` — Caption generation
  - `internal/usecase/migration/s3_migration_service.go` — S3 migration
  - `internal/domain/video.go` — Video model with `OutputPaths`, `S3URLs`, `ProcessedCIDs`

- **Gotchas:**
  - Encoding service has no reference to `StorageBackend` — it writes to filesystem directly and only has an `*ipfs.Client`
  - `video.S3URLs` is populated by S3 migration but NEVER read by any handler
  - `video.OutputPaths` contains local filesystem paths that become stale after S3 migration + local deletion
  - The mock playlist in `serveMockPlaylist()` returns fake HLS content for non-existent videos — clients think the video exists
  - IPFS `Copy()` silently succeeds without doing anything
  - Caption generation reads source video from local filesystem path computed from `storage.Paths` — fails when source was deleted after S3 migration

- **Domain context:**
  - Videos go through statuses: `uploading → queued → processing → completed → failed`
  - `OutputPaths` maps resolution labels ("master", "720p", etc.) to local file paths
  - `S3URLs` maps resolution labels to S3 URLs (set by migration service only)
  - `ProcessedCIDs` maps resolution labels to IPFS CIDs (set during encoding)
  - `StorageTier` is "hot" (local), "warm" (IPFS), or "cold" (S3)

## Assumptions

- S3 backend config exists and is wired in app.go — supported by `internal/storage/s3_backend.go` existing and `internal/config/config.go` having S3 fields — Tasks 3, 4, 6 depend on this
- The encoding service can accept an optional `StorageBackend` parameter without breaking existing callers (nil = local-only mode) — supported by the existing optional pattern with `ipfsClient` — Task 3 depends on this
- The IPFS client's `ObjectStat` method exists for implementing `GetMetadata` properly — supported by standard IPFS HTTP API — Task 7 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| S3 upload during encoding slows transcoding | Medium | Medium | Upload each resolution's HLS directory after that resolution completes (parallel with other resolutions), not sequentially after all encoding |
| Breaking existing local-only deployments | Medium | High | S3 upload is opt-in (nil s3Backend = skip S3 upload, keep current behavior) |
| Caption generation downloads large video files from S3 to temp | Low | Medium | Stream audio extraction directly from S3 read stream, or download to temp with cleanup |
| IPFS backend `GetMetadata` may not have all data available | Low | Low | Return what's available from `ObjectStat`, use sensible defaults for missing fields |

## Goal Verification

### Truths

1. A video encoded with S3 enabled has HLS segments accessible via S3 URLs
2. `StreamVideoHandler` returns real HLS playlist content from S3URLs for S3-migrated videos
3. `StreamVideoHandler` returns 404 (not mock playlist) when no HLS files exist
4. Caption generation succeeds for videos whose source file is on S3 (not local)
5. IPFS backend `Copy` and `GetMetadata` perform real operations
6. All IPFS backend tests pass (0 failures)
7. `HLSHandler` can redirect to S3 for segments stored remotely

### Artifacts

1. `internal/usecase/encoding/service.go` — S3 upload after each resolution
2. `internal/httpapi/handlers/video/stream_handlers.go` — S3URLs lookup + 404 fallback
3. `internal/usecase/captiongen/service.go` — S3 source download
4. `internal/storage/ipfs_backend.go` — Real Copy and GetMetadata
5. `internal/storage/local_backend.go` — New LocalBackend
6. Tests for all of the above

## Progress Tracking

- [x] Task 1: Fix stream handler S3URLs support + remove mock playlist
- [x] Task 2: Add LocalBackend implementing StorageBackend
- [x] Task 3: Add S3 upload to encoding pipeline
- [x] Task 4: Fix HLS handler for S3 serving
- [x] Task 5: Fix caption generation for S3-stored videos
- [x] Task 6: Fix IPFS backend incomplete methods
- [x] Task 7: Fix failing IPFS backend tests

**Total Tasks:** 7 | **Completed:** 7 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix stream handler S3URLs support + remove mock playlist

**Objective:** Make `StreamVideoHandler` serve videos from S3URLs when OutputPaths are unavailable, and return 404 instead of mock playlists.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/stream_handlers.go`
- Test: `internal/httpapi/handlers/video/stream_handlers_test.go`

**Key Decisions / Notes:**

- Add a new function `tryServeFromS3URLs` that checks `video.S3URLs` map for the requested quality, then redirects (307) to the S3 URL. **`S3URLs` is the authoritative source for S3-stored content** — `OutputPaths` remains local-only
- Insert `tryServeFromS3URLs` between `tryServeFromOutputPaths` and `tryServeFromLocalDirectory` in the handler chain
- For `OutputPaths` that are remote URLs (already handled by `isRemoteURL`), no change needed
- **Remove `serveMockPlaylist` entirely** — replace with 404 response
- When video exists but has no playable files: return 404 with `VIDEO_NOT_READY` error code if status is `processing`/`queued`, or `VIDEO_FILES_NOT_FOUND` if status is `completed` but files missing
- Follow existing pattern at `stream_handlers.go:131` for redirect behavior

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] `StreamVideoHandler` returns 307 redirect to S3 URL when `S3URLs` has the requested quality
- [ ] `StreamVideoHandler` returns 404 with appropriate error code when no HLS files exist (never mock playlist)
- [ ] Existing local-path and remote-URL serving still works

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestStream`

---

### Task 2: Add LocalBackend implementing StorageBackend

**Objective:** Create a `LocalBackend` that wraps filesystem operations behind the `StorageBackend` interface, enabling consistent storage abstraction.

**Dependencies:** None

**Files:**

- Create: `internal/storage/local_backend.go`
- Test: `internal/storage/local_backend_test.go`

**Key Decisions / Notes:**

- Implement all `StorageBackend` methods using `os` package operations
- `Upload`: write `io.Reader` to `basePath/key`
- `UploadFile`: copy file from `localPath` to `basePath/key`
- `Download`: open file at `basePath/key` and return `io.ReadCloser`
- `GetURL`: return `file:///basePath/key` or a configurable public URL prefix
- `GetSignedURL`: return same as `GetURL` (no signing needed for local)
- `Delete`: `os.Remove`
- `Exists`: `os.Stat`
- `Copy`: `io.Copy` from source to dest
- `GetMetadata`: `os.Stat` for size/time, detect content type from extension
- Use `filepath.Clean` and path traversal checks like `validateFilePath` in `upload/service.go:150`

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] `LocalBackend` implements all `StorageBackend` interface methods
- [ ] Path traversal protection on all file operations
- [ ] Table-driven tests covering upload, download, delete, exists, copy, metadata

**Verify:**

- `go test -short ./internal/storage/... -run TestLocalBackend`

---

### Task 3: Add S3 upload to encoding pipeline

**Objective:** After each resolution is encoded, upload its HLS directory (playlist + segments) to S3 when an S3 backend is configured. Update `OutputPaths` with S3 URLs.

**Dependencies:** Task 2

**Files:**

- Modify: `internal/usecase/encoding/service.go`
- Modify: `internal/usecase/encoding/service_unit_test.go`
- Modify: `internal/app/app.go` (wire S3 backend into encoding service)

**Key Decisions / Notes:**

- Add an optional `s3Backend storage.StorageBackend` field to the encoding `service` struct
- Add `WithS3Backend(backend storage.StorageBackend) *service` method following the existing `WithFederationEnqueuer` pattern at `service.go:65`
- In `processJob()` at `service.go:270`, after `encodeResolutions` succeeds (line 299-301), add `uploadVariantsToS3` that:
  - Walks each resolution directory (like `s3_migration_service.go:197`)
  - Uploads `.m3u8` and `.ts` files with correct content types
  - Returns a `map[string]string` of resolution → S3 URL for the playlist
- **Do NOT merge S3 URLs into `OutputPaths`** — `OutputPaths` stays local-only. Write S3 URLs exclusively to `video.S3URLs`. Task 1's `tryServeFromS3URLs` is the canonical handler for S3-stored content
- Also upload the original source video to S3 with key `videos/{videoID}/source{ext}` so caption generation (Task 5) can retrieve it later. Store this as `video.S3URLs["source"]`
- When `s3Backend` is nil, skip S3 upload entirely (current behavior preserved)
- Upload thumbnails and previews to S3 as well (same pattern as `s3_migration_service.go:241-258`)
- **Wiring:** Add `WithS3Backend` call in `internal/app/app.go` at the same location where the encoding service is constructed (same pattern as `WithFederationEnqueuer`). The `WithS3Backend` call must happen before the `*service` is stored as a `Service` interface

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] When S3 is configured, encoding pipeline uploads HLS segments and source video to S3 and sets `video.S3URLs` (NOT OutputPaths)
- [ ] When S3 is nil, encoding behaves exactly as before (local + IPFS only)
- [ ] `video.S3URLs["source"]` contains the S3 key for the original source video
- [ ] Thumbnails and previews uploaded to S3 when configured

**Verify:**

- `go test -short ./internal/usecase/encoding/... -run TestS3Upload`

---

### Task 4: Fix HLS handler for S3 serving

**Objective:** Make the `HLSHandler` redirect to S3 URLs for segments and playlists when local files don't exist but the video has S3-stored content.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/httpapi/handlers/video/stream_handlers.go`
- Test: `internal/httpapi/handlers/video/stream_handlers_test.go`

**Key Decisions / Notes:**

- `HLSHandler` at `stream_handlers.go:281` currently serves from local filesystem only
- After the path traversal check, before `http.ServeFile`, check if the local file exists
- If local file doesn't exist, look up the video by ID (first path segment), check `S3URLs` or `OutputPaths` for a matching S3 URL, and redirect
- S3 key pattern: `videos/{videoID}/hls/{relPath}` — match against the request path
- Alternative: construct the S3 URL from the video's S3 base URL + the relative path
- Must preserve privacy check for private videos (already exists at line 295-303)

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] HLS requests for S3-stored videos redirect to S3 URL
- [ ] Local HLS serving still works for non-migrated videos
- [ ] Privacy check still enforced before redirect

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestHLS`

---

### Task 5: Fix caption generation for S3-stored videos

**Objective:** Make caption generation work when the source video file is on S3 (not on local filesystem).

**Dependencies:** Task 3

**Files:**

- Modify: `internal/usecase/captiongen/service.go`
- Modify: `internal/usecase/captiongen/service_test.go`

**Key Decisions / Notes:**

- `processJob()` at `captiongen/service.go:264` gets the source video path from `storage.Paths.WebVideoFilePath()` — this is a local path
- When `os.Stat(sourceVideoPath)` fails (file not found), check if `video.S3URLs["source"]` exists (set by Task 3's encoding pipeline)
- If source is on S3: download via `s3Backend.Download(ctx, s3Key)` → write to temp file → pass temp path to whisper
- The S3 key for the source video is stored in `video.S3URLs["source"]` (e.g., `videos/{videoID}/source.mp4`)
- Add optional `s3Backend storage.StorageBackend` to captiongen service struct
- If neither local file nor S3URL exists, check `video.ProcessedCIDs` for IPFS fallback (download via IPFS gateway)
- Clean up temp source file in defer (same pattern as audio file cleanup at line 283)
- When S3 is not configured and local file missing, return descriptive error

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] Caption generation works for S3-stored videos (downloads source to temp)
- [ ] Caption generation still works for local videos (no change)
- [ ] Temp files cleaned up after processing

**Verify:**

- `go test -short ./internal/usecase/captiongen/... -run TestS3Source`

---

### Task 6: Fix IPFS backend incomplete methods

**Objective:** Implement real `Copy` and `GetMetadata` for the IPFS backend.

**Dependencies:** None

**Files:**

- Modify: `internal/storage/ipfs_backend.go`
- Modify: `internal/storage/ipfs_backend_test.go`
- Possibly modify: `internal/ipfs/client.go` (add `FileStat` method if not present)

**Key Decisions / Notes:**

- `Copy` at `ipfs_backend.go:84`: IPFS is content-addressed, so "copying" a CID just means pinning the same CID under a different alias. Since IPFS doesn't have key-based addressing like S3, the simplest correct implementation is to pin the source CID (if not already pinned). If `sourceKey == destKey`, no-op is correct. Otherwise, pin `destKey` (assuming it's a CID)
- `GetMetadata` at `ipfs_backend.go:88`: **Before implementing**, run `grep -n 'ObjectStat\|object/stat\|files/stat\|FileStat' internal/ipfs/client.go` to check if the method exists. If not, add a `FileStat(ctx, cid)` method using the `/api/v0/files/stat` endpoint (preferred over deprecated `object/stat`). Content type can't be determined from IPFS directly — keep `"application/octet-stream"` as default
- If adding `FileStat` to the IPFS client, also add corresponding unit tests in `internal/ipfs/client_test.go`

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] `Copy` pins the destination CID
- [ ] `GetMetadata` returns real file size from IPFS
- [ ] Tests verify both methods with mocked IPFS client

**Verify:**

- `go test -short ./internal/storage/... -run TestIPFSBackend`

---

### Task 7: Fix failing IPFS backend tests

**Objective:** Fix the 3 failing tests in `ipfs_backend_test.go` related to error handling when IPFS is not running.

**Dependencies:** Task 6

**Files:**

- Modify: `internal/storage/ipfs_backend_test.go`
- Possibly modify: `internal/storage/ipfs_backend.go`
- Possibly modify: `internal/ipfs/client.go`

**Key Decisions / Notes:**

- Failing tests: `TestIPFSBackend_Methods/Upload_error_from_client_when_not_running`, `Exists_error_from_client_when_not_running`
- These tests expect errors when IPFS is not running, but get nil
- **Diagnostic step (do first):** Read `ipfs_backend_test.go` to understand the mock setup. Then check whether `IPFSBackend` accepts a concrete `*ipfs.Client` or an interface — if concrete, the fix may require extracting an `ipfsClientInterface` to enable proper mocking
- Check the actual error paths in `Upload` and `Exists` — the IPFS client may silently succeed, or a nil-check may short-circuit before reaching the error path
- The fix should ensure the IPFS client returns errors when the daemon is unreachable

**Definition of Done:**

- [ ] All tests pass (0 failures in storage package)
- [ ] No diagnostics errors
- [ ] IPFS backend methods properly return errors when IPFS daemon is not available
- [ ] Error messages are descriptive

**Verify:**

- `go test -short ./internal/storage/... -run TestIPFSBackend`

---

## Open Questions

None — all design decisions resolved.

### Deferred Ideas

- **Resumable upload protocol** — PeerTube supports `tus` protocol for resumable uploads; Athena's chunked upload is functionally similar but not protocol-compatible
- **Per-resolution file management** — PeerTube lets admins delete specific resolution files; could save storage
- **Video studio editing** — PeerTube has cut/intro/outro editing via the studio API
- **ATProto video blob upload** — Currently uses external embed; could upload actual video bytes to Bluesky for native playback

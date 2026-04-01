# Batch Video Uploads Implementation Plan

Created: 2026-03-31
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 1
Worktree: No
Type: Feature

## Summary

**Goal:** Add a batch upload orchestration endpoint that accepts metadata for multiple videos, validates all upfront (quota, file types, limits), creates upload sessions for each, and returns a batch ID with session IDs. Frontend uploads files to each session using the existing chunked upload flow.

**Architecture:** Extends the existing upload service (`internal/usecase/upload/service.go`) with batch orchestration. A new `batch_uploads` table tracks batch-level metadata. The `upload_sessions` table gains a nullable `batch_id` foreign key. Two new HTTP endpoints under `/api/v1/uploads/batch`. Batch size limit is admin-configurable via `MAX_BATCH_UPLOAD_SIZE` env var (default: 10). A `MAX_USER_VIDEO_QUOTA` config field (default: 50GB) enables aggregate quota enforcement.

**Tech Stack:** Go, PostgreSQL (Goose migration), Chi router, SQLX

**Note:** PeerTube has NO batch upload API — this is a Vidra Core extension beyond PeerTube parity.

## Scope

### In Scope

- Batch initiation endpoint with per-video metadata (filename, file_size, title, description, privacy)
- Aggregate quota validation: total used + batch aggregate vs `MaxUserVideoQuota` config ceiling
- Per-video file extension and size validation
- Admin-configurable batch size limit (`MAX_BATCH_UPLOAD_SIZE` env var, default 10)
- Admin-configurable user video quota (`MAX_USER_VIDEO_QUOTA` env var, default 50GB)
- Batch status endpoint showing per-session progress with ownership check
- Database migration for `batch_uploads` table and `batch_id` column on `upload_sessions`
- Comprehensive regression tests for existing single-upload flow
- Unit + integration tests for batch functionality
- OpenAPI spec updates
- Postman collection updates

### Out of Scope

- Frontend implementation (backend API only)
- Batch cancellation endpoint (cancel individual sessions via existing flow)
- Batch retry/resume (use existing per-session resume)
- Notification on batch completion
- tus protocol support (existing deferred feature)
- Batch expiration/cleanup (follow-up — individual sessions still expire via existing scheduler)

## Approach

**Chosen:** Batch initiation endpoint — reuses existing chunked upload infrastructure

**Why:** Maximum backend value (atomic validation, quota checks, progress tracking) with minimal changes to existing upload flow. Frontend reuses existing chunked upload endpoints per-session.

**Alternatives considered:**
- Full multipart batch upload — rejected due to massive request body, no resumability, memory pressure
- Thin metadata-only batch — rejected as it provides no backend value beyond what frontend can do alone

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Upload service: `internal/usecase/upload/service.go` — existing `InitiateUpload` method shows the per-video creation flow. The batch method must NOT call `InitiateUpload` directly — see Gotchas below for why.
  - Handler pattern: `internal/httpapi/handlers/video/upload_handlers.go` — all handlers follow `func XxxHandler(deps) http.HandlerFunc` pattern
  - Response envelope: `shared.WriteJSON(w, status, data)` and `shared.WriteError(w, status, domainErr)` — always uses `{success, data, error, meta}` envelope
  - Repository pattern: `internal/repository/upload_repository.go` — SQLX with parameterized queries
  - Context-carried transactions: `repository.GetExecutor(ctx, r.db)` pattern from `internal/repository/transaction_manager.go:98` — repositories detect if a tx is in context and use it, otherwise use the db handle directly
  - Domain errors: `domain.NewDomainError("CODE", "message")` — see `internal/domain/errors.go`

- **Conventions:**
  - Config fields: add to `internal/config/config.go` struct, load in `config_load.go` via `GetInt64Env`/`GetIntEnv`
  - Routes: wired in `internal/httpapi/routes.go` function `registerVideoAPIRoutes`
  - Port interfaces: `internal/port/upload.go` defines the `UploadRepository` interface (aliased as `usecase.UploadRepository`)
  - Tests: table-driven with testify, integration tests guarded by `testing.Short()`
  - **Type alias chain:** `port.UploadRepository` → `usecase.UploadRepository` (alias in `internal/usecase/upload_service.go`). Both refer to the same interface. The `Service` interface in `internal/usecase/upload/service.go` is what handlers depend on via `HandlerDependencies.UploadService`.

- **Key files:**
  - `internal/usecase/upload/service.go` — upload service with `Service` interface
  - `internal/port/upload.go` — `UploadRepository` interface
  - `internal/repository/upload_repository.go` — SQLX implementation
  - `internal/repository/transaction_manager.go` — `GetExecutor`, `WithTx`, `TransactionManager`
  - `internal/domain/video.go` — `UploadSession`, `InitiateUploadRequest`, `InitiateUploadResponse` models
  - `internal/httpapi/routes.go` — route registration (upload routes in the `/uploads` group at lines ~663-672)
  - `internal/config/config.go` — config struct
  - `internal/config/config_load.go` — env var loading
  - `migrations/008_create_upload_sessions_table.sql` — existing upload_sessions schema

- **Gotchas:**
  - **Transaction support:** The existing `InitiateUpload` method calls `s.videoRepo.Create()` and `s.uploadRepo.CreateSession()` on non-transactional db handles. You CANNOT just call `InitiateUpload` in a loop inside a `TransactionManager.WithTransaction` callback — those calls won't participate in the transaction. Instead, extract the core per-video logic into an internal method that uses the context-carried transaction pattern (`GetExecutor(ctx, r.db)`). The repository methods `CreateSession` and `Create` must be updated to use `GetExecutor` so they automatically use a tx when one is in context.
  - **Filesystem side effects:** `InitiateUpload` creates temp directories via `os.MkdirAll`. These are NOT rolled back by a DB transaction. On batch failure after partial temp dir creation, clean up the created temp dirs explicitly in an error handler.
  - **BatchID type:** `batch_id` is `UUID` in PostgreSQL. An empty Go string is NOT a valid UUID. Use `*string` in Go so the zero value maps to SQL NULL. Never use empty string as default.
  - `UploadSession.UploadedChunks` is a `pq.Int32Array` in DB but `[]int` in Go — use `pq.Array()` for scans
  - `upload_sessions.uploaded_chunks` column uses PostgreSQL array type
  - Config is loaded once at startup — env var changes require restart
  - Default `ChunkSize` in the single-upload handler is `10*1024*1024` (10MB) when client sends 0. The batch handler must replicate this defaulting for each video.

- **Domain context:**
  - Each upload session creates a video record with `StatusUploading`, which transitions to `StatusQueued` on completion
  - Quota is tracked via `VideoRepository.GetVideoQuotaUsed()` which sums `file_size` from all user videos — returns bytes USED, not remaining
  - The `MaxUploadSize` config controls per-request body size limit, not per-file — batch metadata is small JSON so this is fine
  - No `MaxUserVideoQuota` config exists yet — Task 1 adds it

## Assumptions

- Repository methods can be updated to use `GetExecutor(ctx, r.db)` without breaking existing callers (non-tx context returns the db handle, same as before) — supported by `internal/repository/transaction_manager.go:98` — Tasks 3 depend on this
- Quota check uses `GetVideoQuotaUsed` which returns total bytes for a user — supported by `internal/repository/video_repository.go:390` — Task 3 depends on this
- The `upload_sessions` table can accept a nullable `batch_id` column without breaking existing queries — supported by all existing queries selecting explicit columns, not `SELECT *` — Tasks 2, 3 depend on this
- Config struct additions are backward-compatible (zero value = use default) — supported by existing `GetIntEnv` pattern in `config_load.go` — Task 1 depends on this
- A single mock file (`internal/usecase/upload/service_test.go`) defines the `mockUploadRepo` struct that needs batch method stubs — Task 5 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Batch creates partial DB records on mid-flight error | Medium | High | Use context-carried transaction via `TransactionManager.WithTransaction` + `GetExecutor(ctx, r.db)` in repositories; rollback all on any failure |
| Batch creates temp dirs that aren't cleaned up on DB rollback | Medium | Medium | Explicitly track created temp dirs in a slice; clean up all in a deferred error handler if the transaction fails |
| Large batches exhaust disk space for temp dirs | Low | High | Aggregate file size check against `MaxUserVideoQuota` ceiling before creating any sessions |
| Adding `batch_id` column breaks existing upload queries | Low | High | Column is nullable `*string` in Go (NULL in DB); existing queries use explicit column lists; `CreateSession` passes nil when no batch |
| Batch status leaks data to other users | Medium | High | Ownership check in `GetBatchStatusHandler`: verify `batch.UserID == requestingUserID`, return 404 if mismatch |

## Goal Verification

### Truths

1. `POST /api/v1/uploads/batch` accepts array of video metadata and returns batch_id + session IDs
2. Batch validates all files upfront — invalid file in batch causes entire batch to be rejected
3. Aggregate quota check prevents batch from exceeding user's `MaxUserVideoQuota` ceiling
4. `GET /api/v1/uploads/batch/{batchId}` returns per-session progress, only to the batch owner
5. Existing single-upload flow (`POST /api/v1/uploads/initiate`) continues to work identically
6. Batch size respects admin-configurable `MAX_BATCH_UPLOAD_SIZE` limit (default 10)

### Artifacts

1. `internal/domain/video.go` — `BatchUpload`, `BatchUploadRequest`, `BatchUploadResponse`, `BatchUploadStatus` models
2. `internal/config/config.go` — `MaxBatchUploadSize` and `MaxUserVideoQuota` fields
3. `migrations/086_add_batch_uploads.sql` — new table + column
4. `internal/port/upload.go` — extended interface with batch methods
5. `internal/repository/upload_repository.go` — batch repository methods with transaction support
6. `internal/usecase/upload/service.go` — `InitiateBatchUpload`, `GetBatchStatus` methods
7. `internal/httpapi/handlers/video/batch_upload_handlers.go` — HTTP handlers
8. `internal/httpapi/routes.go` — new routes under `/uploads/batch`

## Progress Tracking

- [x] Task 1: Domain models and config
- [x] Task 2: Database migration
- [x] Task 3: Repository and service extension
- [x] Task 4: HTTP handlers and route wiring
- [x] Task 5: Regression tests for existing upload flow
- [x] Task 6: Batch feature tests (unit + integration)
- [x] Task 7: OpenAPI and Postman updates

**Total Tasks:** 7 | **Completed:** 7 | **Remaining:** 0

## Implementation Tasks

### Task 1: Domain Models and Config

**Objective:** Add batch upload domain models, `MaxUserVideoQuota` config, and admin-configurable batch size limit.
**Dependencies:** None
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/domain/video.go` — add `BatchUpload`, `BatchUploadRequest`, `BatchUploadVideoItem`, `BatchUploadResponse`, `BatchUploadStatus` structs; add `BatchID *string` field to `UploadSession`
- Modify: `internal/config/config.go` — add `MaxBatchUploadSize int` and `MaxUserVideoQuota int64` fields
- Modify: `internal/config/config_load.go` — load `MAX_BATCH_UPLOAD_SIZE` (default 10) and `MAX_USER_VIDEO_QUOTA` (default 50GB) env vars

**Key Decisions / Notes:**

- `BatchUpload` struct: `ID string`, `UserID string`, `TotalVideos int`, `CreatedAt time.Time`, `UpdatedAt time.Time` — NO status column; status is computed dynamically from session states in `GetBatchStatus`
- `BatchUploadRequest`: `Videos []BatchUploadVideoItem`
- `BatchUploadVideoItem`: `FileName string`, `FileSize int64`, `ChunkSize int64`, `Title string`, `Description string`, `Privacy string`
- `BatchUploadResponse`: `BatchID string`, `Sessions []InitiateUploadResponse`
- `BatchUploadStatus`: `BatchID string`, `TotalVideos int`, `CompletedUploads int`, `ActiveUploads int`, `FailedUploads int`, `Sessions []UploadSession`
- Add `BatchID *string` field to `UploadSession` with `json:"batch_id,omitempty" db:"batch_id"` — pointer type so Go nil maps to SQL NULL
- Default `MaxBatchUploadSize` = 10, `MaxUserVideoQuota` = 50 * 1024 * 1024 * 1024 (50GB)

**Definition of Done:**

- [ ] All new structs compile
- [ ] `BatchID` on `UploadSession` is `*string` (not `string`)
- [ ] Config loads both env vars with correct defaults
- [ ] `go build ./...` succeeds
- [ ] No lint errors

**Verify:**

- `go build ./...`

---

### Task 2: Database Migration

**Objective:** Add `batch_uploads` table and `batch_id` column to `upload_sessions`.
**Dependencies:** Task 1 (domain models define the schema)
**Mapped Scenarios:** None

**Files:**

- Create: `migrations/086_add_batch_uploads.sql`

**Key Decisions / Notes:**

- `batch_uploads` table: `id UUID PK DEFAULT gen_random_uuid()`, `user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`, `total_videos INT NOT NULL CHECK (total_videos > 0)`, `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`, `updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`
- NO `status` column — status is computed dynamically from session states to avoid consistency issues
- `upload_sessions` table: `ALTER TABLE ADD COLUMN batch_id UUID REFERENCES batch_uploads(id) ON DELETE SET NULL` — nullable for backward compatibility
- Index on `upload_sessions(batch_id)` for batch status queries (WHERE batch_id = $1)
- Index on `batch_uploads(user_id)` for user-scoped queries
- Add `update_updated_at_column()` trigger on `batch_uploads`
- Down migration: DROP INDEX, DROP COLUMN `batch_id` from `upload_sessions`, then DROP TABLE `batch_uploads`

**Definition of Done:**

- [ ] Migration file has both `-- +goose Up` and `-- +goose Down` sections
- [ ] `batch_id` column is nullable (backward compatible)
- [ ] No `status` column on `batch_uploads` (computed dynamically)
- [ ] Proper indexes on `batch_id` and `user_id`
- [ ] Foreign key constraints with appropriate ON DELETE behavior

**Verify:**

- `go build ./...` (no code changes needed, just SQL)

---

### Task 3: Repository and Service Extension

**Objective:** Extend the upload repository interface and service with batch upload methods. Update existing repository methods to support context-carried transactions.
**Dependencies:** Task 1, Task 2
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/port/upload.go` — add `CreateBatch`, `GetBatch`, `GetSessionsByBatchID` to `UploadRepository` interface
- Modify: `internal/repository/upload_repository.go` — implement new interface methods; update `CreateSession` to include `batch_id` and use `GetExecutor(ctx, r.db)` for transaction support
- Modify: `internal/usecase/upload/service.go` — add `InitiateBatchUpload` and `GetBatchStatus` to `Service` interface; implement with extracted per-video logic

**Key Decisions / Notes:**

- **Transaction support (CRITICAL):** Update `uploadRepository.CreateSession` to use `GetExecutor(ctx, r.db)` instead of `r.db.ExecContext`. This makes it transaction-aware: when a tx is in context (from `TransactionManager.WithTransaction`), it uses the tx; otherwise falls back to the db handle (existing behavior preserved).
- Similarly update `videoRepository.Create` if it doesn't already use `GetExecutor`. Check first — `video_repository.go` may already use it (the `GetVideoQuotaUsed` method at line 392 does).
- `InitiateBatchUpload` flow:
  1. Validate batch size <= `cfg.MaxBatchUploadSize`
  2. Validate each video's file extension via `validUploadExt()` and size <= 10GB
  3. Apply default `ChunkSize` of `10*1024*1024` for any video with `ChunkSize == 0`
  4. Calculate aggregate file size; call `GetVideoQuotaUsed` to get current usage; verify `currentUsage + aggregateSize <= cfg.MaxUserVideoQuota`
  5. Use `TransactionManager.WithTransaction` to wrap all DB operations:
     a. Create `BatchUpload` record via `uploadRepo.CreateBatch`
     b. For each video: create Video record, create UploadSession with `BatchID` set, create temp dir
     c. Track created temp dirs in a `[]string` slice
  6. If transaction fails, clean up all created temp dirs in deferred error handler
  7. Return `BatchUploadResponse` with batch ID + all session responses
- `GetBatchStatus` flow:
  1. Get batch record via `uploadRepo.GetBatch`
  2. **Ownership check:** verify `batch.UserID == userID` (userID passed from handler)
  3. Get all sessions via `uploadRepo.GetSessionsByBatchID`
  4. Compute status counts from session states (active/completed/expired/failed)
  5. Return `BatchUploadStatus`

**Definition of Done:**

- [ ] `CreateSession` uses `GetExecutor(ctx, r.db)` for transaction support
- [ ] `InitiateBatchUpload` validates all files, checks quota against `MaxUserVideoQuota`, creates sessions atomically in a transaction
- [ ] Temp dir cleanup on transaction failure
- [ ] `GetBatchStatus` verifies batch ownership (returns error if user doesn't own batch)
- [ ] `GetBatchStatus` computes status dynamically from session states
- [ ] Existing `InitiateUpload` continues to work (`BatchID` defaults to nil)
- [ ] `go build ./...` succeeds

**Verify:**

- `go test -short ./internal/usecase/upload/... -count=1`
- `go build ./...`

---

### Task 4: HTTP Handlers and Route Wiring

**Objective:** Create HTTP handlers for batch upload endpoints and wire them into the router.
**Dependencies:** Task 3
**Mapped Scenarios:** None

**Files:**

- Create: `internal/httpapi/handlers/video/batch_upload_handlers.go`
- Modify: `internal/httpapi/routes.go` — add batch routes under `/uploads` group

**Key Decisions / Notes:**

- `BatchInitiateUploadHandler(uploadService, cfg)`:
  - Auth required (`middleware.Auth`)
  - Extract `userID` from context
  - Parse JSON body into `domain.BatchUploadRequest`
  - Validate `len(req.Videos) > 0` and `<= cfg.MaxBatchUploadSize`
  - Call `uploadService.InitiateBatchUpload(ctx, userID, req)`
  - Return 201 with `BatchUploadResponse`
  - Follow handler pattern from `upload_handlers.go` (`InitiateUploadHandler`)
- `GetBatchStatusHandler(uploadService)`:
  - Auth required
  - Extract `userID` from context
  - Parse `batchId` from URL param, validate UUID format
  - Call `uploadService.GetBatchStatus(ctx, batchId, userID)` — service handles ownership check
  - If not owned, service returns error → handler returns 404 (not 403, to avoid revealing batch existence)
  - Return 200 with `BatchUploadStatus`
- **Route placement:** Add inside the existing `/uploads` group in `registerVideoAPIRoutes` (lines ~663-672 in `routes.go`), co-located with existing upload routes:
  ```
  r.Route("/uploads", func(r chi.Router) {
      r.Use(middleware.Auth(cfg.JWTSecret))
      r.Post("/initiate", ...)          // existing
      r.Post("/batch", BatchInitiateUploadHandler(...))   // NEW
      r.Get("/batch/{batchId}", GetBatchStatusHandler(...)) // NEW
      r.Route("/{sessionId}", ...)      // existing
  })
  ```

**Definition of Done:**

- [ ] `POST /api/v1/uploads/batch` creates batch and returns sessions
- [ ] `GET /api/v1/uploads/batch/{batchId}` returns batch progress only to owner
- [ ] Non-owner gets 404 (not 403)
- [ ] Both endpoints require authentication
- [ ] Invalid batch size returns 400 with descriptive error
- [ ] Empty batch returns 400
- [ ] `go build ./...` succeeds

**Verify:**

- `go build ./...`

---

### Task 5: Regression Tests for Existing Upload Flow

**Objective:** Ensure no regressions in the existing single-upload flow after batch code changes. This is the primary testing priority.
**Dependencies:** Task 3, Task 4
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/upload_handlers_test.go` — add targeted regression tests
- Modify: `internal/usecase/upload/service_test.go` — update `mockUploadRepo` with new interface methods; add regression tests for `InitiateUpload`, `UploadChunk`, `CompleteUpload`

**Key Decisions / Notes:**

- **Mock files requiring updates** (add stub methods for `CreateBatch`, `GetBatch`, `GetSessionsByBatchID`):
  1. `internal/usecase/upload/service_test.go` — `mockUploadRepo` struct (line 17)
  - These stubs return nil/empty defaults so existing tests compile without changes
- Run the full existing test suite first to establish a green baseline
- Add regression tests specifically verifying:
  1. Single upload initiation still works with `BatchID` as nil
  2. Chunk upload to a non-batch session still works
  3. Complete upload for a non-batch session still triggers encoding job
  4. Upload status for a non-batch session works (UploadedChunks populated)
  5. Resume upload for a non-batch session works
  6. Concurrent single upload + batch upload for same user (quota interaction)
- Verify `CreateSession` still works without a transaction in context (non-batch path)

**Definition of Done:**

- [ ] All existing upload tests pass unchanged
- [ ] New regression tests pass verifying single-upload isolation from batch changes
- [ ] `mockUploadRepo` in `service_test.go` updated with batch method stubs
- [ ] `go test -short ./internal/httpapi/handlers/video/... -count=1` passes
- [ ] `go test -short ./internal/usecase/upload/... -count=1` passes

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1`
- `go test -short ./internal/usecase/upload/... -count=1`

---

### Task 6: Batch Feature Tests (Unit + Integration)

**Objective:** Comprehensive tests for the new batch upload functionality.
**Dependencies:** Task 4, Task 5
**Mapped Scenarios:** None

**Files:**

- Create: `internal/httpapi/handlers/video/batch_upload_handlers_test.go` — handler unit tests
- Modify: `internal/usecase/upload/service_test.go` — service unit tests for batch methods

**Key Decisions / Notes:**

- Handler tests (table-driven):
  1. Happy path: batch of 3 videos returns 201 with 3 session IDs and a batch_id
  2. Empty videos array returns 400
  3. Exceeds batch limit returns 400 with descriptive error mentioning the limit
  4. Unauthorized (no user ID) returns 401
  5. Invalid JSON body returns 400
  6. Single video in batch works (edge case, batch of 1)
  7. Invalid file extension in one video rejects entire batch with 400
  8. File too large in one video rejects entire batch with 400
- Service tests (table-driven):
  1. `InitiateBatchUpload` creates correct number of sessions with batch_id set
  2. `InitiateBatchUpload` rejects when aggregate size + current usage exceeds `MaxUserVideoQuota`
  3. `InitiateBatchUpload` applies default ChunkSize (10MB) when ChunkSize is 0
  4. `InitiateBatchUpload` transaction rollback on partial failure cleans up temp dirs
  5. `GetBatchStatus` returns correct counts (active/completed/failed)
  6. `GetBatchStatus` with unknown batch_id returns error
  7. `GetBatchStatus` with batch owned by different user returns error
  8. Partial completion reflected in status (some sessions completed, some active)
- Integration tests (guarded by `testing.Short()`):
  1. Full batch flow: initiate → check status → upload chunks to one session → complete → check status again
  2. Concurrent single upload + batch upload for same user — both succeed, quota accurate

**Definition of Done:**

- [ ] All handler edge cases tested (empty, oversized, unauthorized, invalid, ownership)
- [ ] Service logic tested (quota check, batch creation, status aggregation, ownership, transaction rollback)
- [ ] Integration test covers end-to-end batch flow
- [ ] `go test -short ./internal/httpapi/handlers/video/... -count=1` passes
- [ ] `go test -short ./internal/usecase/upload/... -count=1` passes

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1 -run Batch`
- `go test -short ./internal/usecase/upload/... -count=1 -run Batch`

---

### Task 7: OpenAPI and Postman Updates

**Objective:** Update API specifications and test collections for the new batch endpoints.
**Dependencies:** Task 4, Task 5, Task 6
**Mapped Scenarios:** None

**Files:**

- Modify: `api/openapi.yaml` (or appropriate spec file) — add batch upload endpoints
- Modify/Create: `postman/` collection — add batch upload requests and tests
- Modify: `.claude/rules/feature-parity-registry.md` — add batch upload as Vidra extension

**Key Decisions / Notes:**

- OpenAPI: add `POST /api/v1/uploads/batch` and `GET /api/v1/uploads/batch/{batchId}` with request/response schemas
- Request schema: `BatchUploadRequest` with `videos` array of `BatchUploadVideoItem`
- Response schemas: `BatchUploadResponse` (201) and `BatchUploadStatus` (200)
- Error responses: 400 (validation), 401 (unauthorized), 404 (batch not found / not owned)
- Postman: add batch upload collection with happy path, validation error, quota exceeded, and ownership test cases
- Feature registry: add under "Vidra Core Extensions" table with status "Done"
- Run `make verify-openapi` after spec changes
- Only mark "Done" in registry after Tasks 5 and 6 confirm no regressions

**Definition of Done:**

- [ ] OpenAPI spec includes both batch endpoints with schemas
- [ ] Postman collection has batch upload tests
- [ ] Feature parity registry updated
- [ ] `make verify-openapi` passes

**Verify:**

- `make verify-openapi`

## Open Questions

None — all design decisions resolved.

### Deferred Ideas

- Batch cancellation endpoint (cancel all remaining sessions in a batch)
- Batch completion webhook/notification
- Batch upload history endpoint (list user's past batches)
- Batch auto-cleanup for expired/abandoned batches (add `expires_at` to `batch_uploads`, extend `ExpireOldSessions` scheduler)

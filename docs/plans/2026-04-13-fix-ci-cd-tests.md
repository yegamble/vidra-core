# Fix GitHub CI/CD Tests Plan

Created: 2026-04-13
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Bugfix

## Summary

**Symptom:** 4 of 10 GitHub Actions workflows are failing on main: CI, E2E Tests, Registration API Edge Case Tests, and Security.

**Trigger:** Recent code changes introduced 4 distinct bugs that cause compilation errors, migration panics, and formatting failures across multiple workflows.

**Root Cause:** Four independent issues:

1. **Duplicate migration 088** — `migrations/088_add_wait_transcoding.sql` and `migrations/088_add_lower_email_unique_index.sql` share version 088. Goose panics on startup with `duplicate version 88 detected`. Affects: E2E Tests (server crash), Registration API (migration step), Database Migration Validation.

2. **`CompleteUploadHandler` test signature mismatch** — `internal/httpapi/handlers/video/upload_handlers_test.go:290,461` calls `CompleteUploadHandler(uploadService, encodingRepo)` with 2 args, but the production handler at `upload_handlers.go:138` now requires 3: `(uploadService, encodingRepo, videoRepo)`. Affects: CI Unit Tests (compilation failure), Security static-analysis/staticcheck.

3. **Missing `AppendOutputPath` in manual test mock** — `tests/manual/test_encoding_simple.go:45` uses a `mockVideoRepository` that doesn't implement `AppendOutputPath` from `port.VideoRepository`. Affects: Security dependency-scanning (govulncheck), Security static-analysis (staticcheck).

4. **Unformatted Go files** — 5 files fail `gofmt` check: `internal/domain/video.go`, `internal/domain/video_test.go`, `internal/httpapi/handlers/video/upload_handlers.go`, `internal/httpapi/handlers/video/video_files_response_test.go`, `internal/httpapi/handlers/video/videos.go`. Affects: CI Code Quality (Lint & Format).

## Investigation

- CI logs show `CompleteUploadHandler` compilation error: "not enough arguments in call... have (upload.Service, usecase.EncodingRepository) want (usecase.UploadService, usecase.EncodingRepository, usecase.VideoRepository)"
- E2E logs show `panic: goose: duplicate version 88 detected: migrations/088_add_wait_transcoding.sql migrations/088_add_lower_email_unique_index.sql`
- Registration API logs show same goose panic during migration step
- Security dependency-scanning shows: `tests/manual/test_encoding_simple.go:45:44: *mockVideoRepository does not implement port.VideoRepository (missing method AppendOutputPath)`
- Local `gofmt -l` confirms 5 unformatted files
- Migration 089 already exists (`089_create_channel_activities.sql`), so the duplicate 088 must be renumbered to 090

## Fix Approach

**Chosen:** Fix at source — address all 4 independent root causes directly.

**Why:** Each issue has a single obvious fix. No alternatives needed — these are clear bugs (duplicate version, stale test signature, missing interface method, unformatted code).

**Files to modify:**
- `migrations/088_add_wait_transcoding.sql` → rename to `migrations/090_add_wait_transcoding.sql`
- `internal/httpapi/handlers/video/upload_handlers_test.go` — add `videoRepo` arg to `CompleteUploadHandler` calls (lines 290, 461)
- `tests/manual/test_encoding_simple.go` — add `AppendOutputPath` method to `mockVideoRepository`
- `internal/domain/video.go` — run gofmt
- `internal/domain/video_test.go` — run gofmt
- `internal/httpapi/handlers/video/upload_handlers.go` — run gofmt
- `internal/httpapi/handlers/video/video_files_response_test.go` — run gofmt
- `internal/httpapi/handlers/video/videos.go` — run gofmt

**Tests:** Existing test suite — fixes restore compilation. No new test files needed.

## Progress

- [x] Task 1: Fix duplicate migration version (already fixed in commit e22964c)
- [x] Task 2: Fix CompleteUploadHandler test signature
- [x] Task 3: Fix mockVideoRepository in manual test
- [x] Task 4: Fix Go formatting
- [x] Task 5: Verify all fixes
      **Tasks:** 5 | **Done:** 5

## Tasks

### Task 1: Fix duplicate migration version

**Objective:** Rename `088_add_wait_transcoding.sql` to `090_add_wait_transcoding.sql` to resolve duplicate version 088.
**Files:** `migrations/088_add_wait_transcoding.sql` → `migrations/090_add_wait_transcoding.sql`
**Verify:** `ls migrations/088_* migrations/090_*` shows no duplicates

### Task 2: Fix CompleteUploadHandler test signature

**Objective:** Add missing `videoRepo` argument to `CompleteUploadHandler` calls in test file. The test already has mock video repo infrastructure — just need to pass it.
**Files:** `internal/httpapi/handlers/video/upload_handlers_test.go`
**TDD:** Code should compile and existing tests pass after fix.
**Verify:** `go build ./internal/httpapi/handlers/video/...`

### Task 3: Fix mockVideoRepository in manual test

**Objective:** Add `AppendOutputPath` method to `mockVideoRepository` in `tests/manual/test_encoding_simple.go` to satisfy `port.VideoRepository` interface.
**Files:** `tests/manual/test_encoding_simple.go`
**Verify:** `go build ./tests/manual/...`

### Task 4: Fix Go formatting

**Objective:** Run `gofmt` on the 5 unformatted files.
**Files:** `internal/domain/video.go`, `internal/domain/video_test.go`, `internal/httpapi/handlers/video/upload_handlers.go`, `internal/httpapi/handlers/video/video_files_response_test.go`, `internal/httpapi/handlers/video/videos.go`
**Verify:** `gofmt -l` returns no files

### Task 5: Verify all fixes

**Objective:** Full build, test, and lint pass locally.
**Verify:** `go build ./... && go test -short -count=1 ./internal/httpapi/handlers/video/... ./internal/domain/... && gofmt -l $(go list -f '{{.Dir}}' ./...) 2>/dev/null | head -5`

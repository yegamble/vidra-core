# Fix CI/CD Per-Package Coverage Threshold Failures

Created: 2026-04-13
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Bugfix

## Summary

**Symptom:** The CI workflow on `main` is failing. The "Unit Tests" job fails at "Enforce per-package coverage thresholds" step. 9/10 workflows pass; only CI fails.

**Trigger:** Recent feature commits added production code to `internal/domain` and `internal/usecase/encoding` without sufficient test coverage, causing those packages to drop below their configured thresholds in `scripts/coverage-thresholds.txt`.

**Root Cause:** Two packages below their configured coverage thresholds:
1. `internal/domain`: **93.3% < 94.0%** (0.7% gap) — Multiple functions at 0% coverage: `migration.go` (IsTerminal, Validate, GetStats, SetStats, CanTransition), `notification.go` (DefaultNotificationPreferences), `password_reset.go` (IsExpired), `caption.go` (PopulateFileURL), `channel.go` (PopulateFileURLs)
2. `internal/usecase/encoding`: **75.3% < 82.0%** (6.7% gap) — Post-encoding pipeline functions at 0%: `cleanupOriginalFile`, `finalizeVideoState`, `logPipelineCompletion`, `generateStoryboard`, `generateThumbnail`, `generatePreviewWebP`, `WithActivityPubPublisher`, `WithStoryboardRepo`

## Investigation

- Only the CI workflow fails on `main` — E2E Tests, Security, Registration API, Database Migration Validation all pass
- The failing step is `./scripts/check-per-package-coverage.sh coverage.out` which reads thresholds from `scripts/coverage-thresholds.txt`
- CI log: `FAIL internal/domain 93.3% < 94.0%` and `FAIL internal/usecase/encoding 75.3% < 82.0%`
- No test failures — all tests pass. The issue is purely insufficient coverage of new/existing code
- Local `go tool cover -func` confirms many functions at 0% in both packages
- No `migration_test.go` or `password_reset_test.go` files exist in `internal/domain/`
- CI uses Go 1.25.9 (ubuntu-latest); local is Go 1.26.2 — coverage numbers may differ slightly but both are below thresholds
- Domain 0% functions are all pure/simple (no external deps) — easy to test with table-driven tests
- Encoding 0% functions use internal service struct — existing mock patterns in `service_coverage_test.go` show how to construct testable service instances with mocks

## Fix Approach

**Chosen:** Add targeted tests for 0% coverage functions in both packages

**Why:** These are legitimate coverage gaps — production code without tests. Adding tests is the correct fix (vs lowering thresholds which hides risk). The domain functions are trivially testable; encoding functions have established mock patterns.

**Files to modify/create:**
- `internal/domain/migration_test.go` (NEW) — tests for IsTerminal, Validate, GetStats, SetStats, CanTransition
- `internal/domain/password_reset_test.go` (NEW) — test for IsExpired
- `internal/domain/notification_test.go` (MODIFY) — add DefaultNotificationPreferences test
- `internal/domain/caption_test.go` (MODIFY) — add PopulateFileURL test
- `internal/domain/channel_test.go` (MODIFY) — add PopulateFileURLs test
- `internal/usecase/encoding/service_coverage_test.go` (MODIFY) — add tests for cleanupOriginalFile, finalizeVideoState, logPipelineCompletion, WithActivityPubPublisher, WithStoryboardRepo, generateThumbnail, generatePreviewWebP

**Tests:** All new tests are unit tests using existing mock patterns. No new dependencies.

## Progress

- [x] Task 1: Add domain package tests for 0% coverage functions
- [x] Task 2: Add encoding package tests for 0% coverage functions
- [x] Task 3: Verify coverage thresholds pass locally
      **Tasks:** 3 | **Done:** 3

## Tasks

### Task 1: Add domain package tests for 0% coverage functions

**Objective:** Cover all 0% functions in `internal/domain` to push coverage from 93.3% to ≥94.0%
**Files:**
- `internal/domain/migration_test.go` (NEW) — table-driven tests for IsTerminal (terminal vs non-terminal statuses), Validate (all required fields + defaults), GetStats/SetStats (round-trip JSON), CanTransition (valid/invalid transitions)
- `internal/domain/password_reset_test.go` (NEW) — IsExpired with future and past times
- `internal/domain/notification_test.go` (MODIFY) — add TestDefaultNotificationPreferences
- `internal/domain/caption_test.go` (MODIFY) — add TestCaption_PopulateFileURL
- `internal/domain/channel_test.go` (MODIFY) — add TestChannel_PopulateFileURLs
**TDD:** Write tests → verify they pass → check coverage improvement
**Verify:** `go test -short -coverprofile=/tmp/d.out ./internal/domain/... && go tool cover -func=/tmp/d.out | tail -1`

### Task 2: Add encoding package tests for 0% coverage functions

**Objective:** Cover 0% functions in `internal/usecase/encoding` to push coverage from 75.3% to ≥82.0%
**Files:**
- `internal/usecase/encoding/service_coverage_test.go` (MODIFY) — add tests using existing mock patterns:
  - `TestWithActivityPubPublisher` — verify setter returns service with publisher set
  - `TestWithStoryboardRepo` — verify setter returns service with repo set
  - `TestCleanupOriginalFile_*` — test keepOriginal=true skips, S3 enabled but failed skips, successful removal, file not found
  - `TestFinalizeVideoState_*` — test waitTranscoding=true publishes, waitTranscoding=false skips, video not found
  - `TestLogPipelineCompletion` — verify it runs without panic with various flag combinations
  - `TestGenerateThumbnail_*` — test with mock ffmpeg (binary not found error path)
  - `TestGeneratePreviewWebP_*` — test with mock ffmpeg (binary not found error path)
**TDD:** Write tests → verify they pass → check coverage improvement
**Verify:** `go test -short -coverprofile=/tmp/e.out ./internal/usecase/encoding/... && go tool cover -func=/tmp/e.out | tail -1`

### Task 3: Verify coverage thresholds pass locally

**Objective:** Run the same coverage check that CI runs and confirm both packages pass their thresholds
**Verify:** `COVERAGE_OUT=coverage.out make test-unit && ./scripts/check-per-package-coverage.sh coverage.out`

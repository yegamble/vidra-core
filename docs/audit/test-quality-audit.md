# Test Quality & Strategy Audit

**Date:** 2026-02-15
**Total Tests:** 3,752 across 313 test files, 64 packages
**Status:** All passing in short mode

---

## Best Practices Reference (Go Testing)

### Why 100% Coverage Is Not a Good Goal
- **Diminishing returns:** Going from 80% to 100% typically means testing trivial code paths (getters, error returns) that provide near-zero confidence gain
- **False confidence:** 100% line coverage doesn't mean all behaviors are tested - you can hit every line without testing meaningful edge cases
- **Maintenance cost:** Tests for trivial code become maintenance burden during refactoring, slowing down development
- **Encourages bad tests:** Developers write tests to hit coverage numbers rather than to validate behavior

### What Matters More Than Coverage Percentage
- **Behavior-driven tests:** Test what the code does, not how it does it
- **Error path testing:** Verify error conditions, not just happy paths
- **Boundary testing:** Test edge cases, nil inputs, empty collections, off-by-one
- **Concurrency safety:** Use `-race` flag, test concurrent access patterns
- **Test independence:** Each test should run in isolation, no shared mutable state
- **Deterministic results:** No flakiness from timing, randomness, or environment

---

## Flaky Test Indicators: time.Sleep Usage

Found **100+ instances** of `time.Sleep` in test files. This is the #1 cause of flaky tests in Go.

### Worst Offenders

| File | Sleep Count | Max Duration | Flakiness Risk |
|------|-------------|-------------|----------------|
| `internal/database/pool_test.go` | 14 | 2 seconds | **Critical** |
| `internal/livestream/rtmp_integration_test.go` | 12 | 2 seconds | **Critical** |
| `internal/middleware/ratelimit_leak_test.go` | 8 | 500ms | **High** |
| `internal/chat/chat_integration_test.go` | 6 | 500ms | **High** |
| `internal/scheduler/federation_scheduler_test.go` | 5 | 200ms | **High** |
| `internal/scheduler/firehose_poller_test.go` | 6 | 200ms | **High** |
| `internal/usecase/circuit_breaker_service_test.go` | 4 | 150ms | **Medium** |
| `internal/livestream/analytics_collector_test.go` | 10 | 1ms | **Low** (very short) |

### Recommended Fixes
Replace `time.Sleep` with:
- **Channels/sync primitives:** Wait for actual events, not arbitrary durations
- **`require.Eventually`:** Poll with timeout instead of sleeping
- **Context with deadline:** Let context cancellation drive timing
- **Test-specific clock:** Inject a controllable clock for time-dependent tests

Example refactor:
```go
// BAD: Flaky
time.Sleep(200 * time.Millisecond)
assert.Equal(t, expected, getResult())

// GOOD: Deterministic
require.Eventually(t, func() bool {
    return getResult() == expected
}, 2*time.Second, 10*time.Millisecond)
```

---

## Placeholder/Useless Tests

### E2E Placeholder Tests (Should Be Removed or Implemented)

**`tests/e2e/workflows_test.go`** - 14 test functions, ALL are `t.Skip()` placeholders:
- `TestUserRegistrationAndAuthenticationWorkflow` (3 subtests, all skip)
- `TestVideoUploadAndProcessingWorkflow` (4 subtests, all skip)
- `TestVideoPlaybackAndStreamingWorkflow` (4 subtests, all skip)
- `TestFederationWorkflow` (4 subtests, all skip)
- `TestStorageTierWorkflow` (3 subtests, all skip)
- `TestErrorHandlingInWorkflows` (4 subtests, all skip)
- `TestWorkflowIntegration` (1 subtest, skip)
- `BenchmarkVideoUploadWorkflow` (skip)
- `BenchmarkStorageTierMigration` (skip)

**Recommendation:** Delete these entirely. They create noise in test output and give false impression of E2E coverage. Track the need for E2E tests in a GitHub issue instead.

### Tests with Skipped-for-Unimplemented-Features

| Test | Skip Reason | Action |
|------|-------------|--------|
| `iota_payment_worker_test.go:389` | `trackConfirmation method not implemented` | Delete until feature implemented |
| `iota_payment_worker_test.go:521` | `maxRetries mechanism not implemented` | Delete until feature implemented |
| `activitypub/comment_publisher_test.go:482` | `Parent comment author delivery not yet implemented` | Delete until feature implemented |
| `payments/payment_service_test.go:350` | `Requires proper seed decryption mocking` | Delete until refactored |

### Mock Interface Stubs Returning "not implemented"

Multiple test files define mock interfaces that return `errors.New("not implemented")` for methods not under test. While this is a valid Go testing pattern, some files have excessive numbers of these stubs:

- `internal/httpapi/handlers/video/hls_handlers_unit_test.go` - 13 stub methods
- `internal/httpapi/handlers/video/analytics_handlers_unit_test.go` - 7 stub methods
- `internal/httpapi/handlers/social/comments_unit_test.go` - 8 stub methods

**This is acceptable** - these are minimal mock implementations. No action needed unless the interfaces change.

---

## Oversized Test Files

| File | Lines | Recommendation |
|------|-------|----------------|
| `internal/usecase/activitypub/service_test.go` | 2,759 | Split by service method group |
| `internal/httpapi/handlers/auth/auth_handlers_unit_test.go` | 2,564 | Split by endpoint |
| `internal/httpapi/handlers/social/social_handlers_unit_test.go` | 2,158 | Split by feature |
| `internal/repository/redundancy_repository_unit_test.go` | 2,021 | Split by operation type |
| `internal/repository/activitypub_repository_unit_test.go` | 1,721 | Split by entity |

Test files are exempt from the 300-line rule, but files over 2,000 lines are hard to navigate. Consider splitting by logical grouping.

---

## Test Anti-Patterns Found

### 1. Integration Tests Without `testing.Short()` Guard

Most integration tests properly use `testing.Short()` but verify all do. Tests that depend on external services (DB, Redis, IPFS) must be guarded.

### 2. Timing-Dependent Assertions

Tests in `internal/database/pool_test.go` use `time.Sleep(2 * time.Second)` - these are the most likely to flake in CI under load:
- Line 341: 2-second sleep waiting for connection eviction
- Line 397: 2-second sleep waiting for idle timeout

### 3. Random Data Without Seeds

Check tests using `rand.Intn()` or similar without `rand.New(rand.NewSource(seed))` for reproducibility:
- `internal/httpapi/handlers/video/views_load_test.go:157` uses `rand.Intn(100)` - acceptable in load tests but adds non-determinism

---

## Test Coverage Quality Assessment

### Strong Areas
- **Domain models:** Well-tested with table-driven tests and edge cases
- **Repository layer:** Comprehensive sqlmock-based unit tests
- **Handler layer:** Good HTTP test coverage with request/response validation
- **Middleware:** Good coverage including edge cases and concurrency

### Coverage Gaps
- **IPFS backend:** Only tests error returns from "not implemented" methods
- **Worker package:** Payment worker has skipped tests for unimplemented features
- **E2E tests:** Entirely placeholder - no real end-to-end coverage exists
- **Federation delivery:** Complex HTTP signature verification undertested (noted in CHANGELOG as known gap at 72.2%)

---

## Recommendations Summary

| Priority | Action | Impact |
|----------|--------|--------|
| **High** | Replace time.Sleep with require.Eventually in top 5 files | Eliminate flakiness |
| **High** | Delete all placeholder E2E tests (workflows_test.go) | Remove noise |
| **Medium** | Delete tests for unimplemented features (4 tests) | Clean up |
| **Medium** | Add -race flag to CI pipeline if not present | Catch data races |
| **Low** | Split test files over 2,000 lines | Improve navigability |
| **Low** | Audit rand usage for deterministic seeds | Reproducibility |

---

## Overall Assessment

**Score: B+**

The test suite is solid with 3,752 tests passing, good table-driven patterns, and proper mocking. Main concerns are the time.Sleep flakiness risk (100+ instances), placeholder E2E tests creating false impressions, and a few tests for features that don't exist yet. The 62.3% average coverage is appropriate - pushing for higher would likely mean testing trivial code.

# Gateway Client Lock Contention Optimization

Created: 2026-02-18
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
> **Worktree:** No — working directly on current branch

## Summary

**Goal:** Eliminate unnecessary lock contention in `selectHealthyGateway()` by replacing the exclusive mutex lock with atomic operations for the round-robin index and a read lock for health status checks.

**Architecture:** Change `currentIndex` from `int` to `atomic.Uint64`, use `currentIndex.Add()`/`currentIndex.Load()` methods for round-robin counter advancement, and downgrade `mu.Lock()` to `mu.RLock()` in `selectHealthyGateway()`. All other methods that write to `gatewayStatus` continue using the exclusive write lock.

**Tech Stack:** Go 1.24, `sync/atomic`, `sync.RWMutex`

## Scope

### In Scope

- Convert `currentIndex` field from `int` to `atomic.Uint64`
- Replace `mu.Lock()` with `mu.RLock()` in `selectHealthyGateway()`
- Use `currentIndex.Add()`/`currentIndex.Load()` methods for round-robin index
- Add benchmark tests for `selectHealthyGateway` (baseline serial, parallel, and mixed read-write)
- Add concurrent correctness test validating fair distribution under contention
- Update all existing tests to work with new atomic field
- Create PR with benchmark comparison results

### Out of Scope

- Changes to `markGatewayUnhealthy`, `updateGatewayMetrics`, or other write-path methods
- Changes to health check logic
- Changes to `FetchCID` or `FetchCIDWithRange` logic beyond gateway selection
- Performance optimization of HTTP transport or connection pooling

## Prerequisites

- Go 1.24+ (already in go.mod)
- No external dependencies needed — `sync/atomic` is stdlib
- Install `benchstat` for reliable comparison: `go install golang.org/x/perf/cmd/benchstat@latest`

## Context for Implementer

> This section is critical for cross-session continuity.

- **Patterns to follow:** The file already uses `sync.RWMutex` with `RLock`/`RUnlock` in `GetGatewayStatus()` at `gateway_client.go:217`. Follow this same pattern for the read path in `selectHealthyGateway()`.
- **Conventions:** Package is `ipfs_streaming`. Tests use `testify/assert` and `testify/require`. Test names follow `TestFunctionName/scenario_description` pattern.
- **Key files:**
  - `internal/usecase/ipfs_streaming/gateway_client.go` — the file being optimized
  - `internal/usecase/ipfs_streaming/gateway_client_test.go` — existing test suite (must all pass)
- **Gotchas:**
  - `selectHealthyGateway()` currently does a write (`c.currentIndex = (index + 1) % len(c.gateways)`) inside the lock. This is the reason it uses a write lock. The atomic approach must handle this write without the exclusive lock.
  - The round-robin index wraps using modulo. With atomics, we let the counter grow monotonically and compute `counter % len(gateways)` on read, avoiding the need for CAS loops or write locks.
  - Tests directly access `client.currentIndex` — these must be updated to account for the atomic type.
  - **INVARIANT:** The `gatewayStatus` map keys are fixed after construction in `NewGatewayClient`. Only pointer-target fields (`Healthy`, `ResponseTimeMs`, etc.) are modified at runtime. Adding or removing map entries at runtime would require upgrading all `RLock` callers to `Lock`. This invariant must be documented with a comment at the field declaration.
  - Reading `status.Healthy` requires `RLock` to synchronize with `markGatewayUnhealthy` and `updateGatewayMetrics` which write under `Lock`.
  - **Behavioral change:** The current code advances `currentIndex` past the *found* gateway (set-to-next). The monotonic counter advances past the *starting position* regardless of which gateway is selected. This changes distribution when unhealthy gateways are present. See "Round-robin semantics" in Risks table. The test at line 260-276 ("skips unhealthy gateways") must be traced through the new logic and assertions updated if the sequence changes.
- **Domain context:** `GatewayClient` distributes IPFS content fetches across multiple gateway URLs using round-robin with health awareness. Under load, many goroutines call `FetchCID`/`FetchCIDWithRange` concurrently, all going through `selectHealthyGateway()`.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Add benchmark tests for baseline measurement
- [x] Task 2: Convert currentIndex to atomic and optimize selectHealthyGateway
- [x] Task 3: Run benchmarks, verify improvement, and create PR

**Total Tasks:** 3 | **Completed:** 3 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add benchmark tests for baseline measurement

**Objective:** Create benchmark tests that measure `selectHealthyGateway` performance under single-goroutine, concurrent read, and mixed read-write access patterns. These benchmarks establish the baseline before optimization. Save results to a file for reliable comparison.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/ipfs_streaming/gateway_client_test.go`

**Key Decisions / Notes:**

- Add `BenchmarkSelectHealthyGateway` with sub-benchmarks:
  - `Serial` — calls `selectHealthyGateway()` in a loop; measures per-call overhead
  - `Parallel` — uses `b.RunParallel()` with multiple goroutines; measures read-read lock contention
  - `Parallel_MixedHealth` — same as Parallel but with 1 of 3 gateways marked unhealthy; exercises the loop iteration path under contention
  - `Mixed_ReadWrite` — uses `b.RunParallel()` with ~90% `selectHealthyGateway` reads and ~10% `updateGatewayMetrics` writes (use modulo on a counter to decide); represents realistic production workload where health checks trigger periodic writes
- Use 3 gateways as the standard test configuration
- Follow existing test patterns: create client with `NewGatewayClient`, defer `Close()`
- Save baseline to file: `go test -bench=... -count=5 ... | tee /tmp/bench-baseline.txt`

**Definition of Done:**

- [ ] All 4 benchmark sub-cases run and produce ns/op
- [ ] No data races when run with `-race`
- [ ] Baseline results saved to `/tmp/bench-baseline.txt`

**Verify:**

- `cd /Users/yosefgamble/github/athena && go test -bench=BenchmarkSelectHealthyGateway -benchmem -count=5 ./internal/usecase/ipfs_streaming/ | tee /tmp/bench-baseline.txt` — benchmarks run and results saved
- `cd /Users/yosefgamble/github/athena && go test -race ./internal/usecase/ipfs_streaming/` — no race conditions

### Task 2: Convert currentIndex to atomic and optimize selectHealthyGateway

**Objective:** Replace the exclusive lock in `selectHealthyGateway()` with atomic operations for the round-robin counter and a read lock for health status checks. Add concurrent correctness test.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/usecase/ipfs_streaming/gateway_client.go`
- Modify: `internal/usecase/ipfs_streaming/gateway_client_test.go`

**Key Decisions / Notes:**

- Change `currentIndex int` to `currentIndex atomic.Uint64` in `GatewayClient` struct
- Add `sync/atomic` to imports
- Add INVARIANT comment at `gatewayStatus` field: `// INVARIANT: Map keys are fixed after construction in NewGatewayClient. Only pointer-target fields (Healthy, ResponseTimeMs, etc.) are modified at runtime. Adding/removing entries would require upgrading all RLock callers to Lock.`
- In `selectHealthyGateway()`:
  1. Atomically load and increment the counter: `startIndex := c.currentIndex.Add(1) - 1`
  2. Acquire `mu.RLock()` (not `mu.Lock()`) to read `gatewayStatus`
  3. Iterate gateways using `uint64(startIndex) % uint64(len(c.gateways))` as the starting offset
  4. If a healthy gateway is found, release `RLock` and return it
  5. If no healthy gateway found, fall back to `gateways[0]` (same as current behavior)
  6. Add a comment at the top of the function explaining the TOCTOU tradeoff (see Risks)
- In `NewGatewayClient()`: Remove explicit `currentIndex: 0` initialization (zero-value of `atomic.Uint64` is already 0)
- **Test updates:**
  - `TestNewGatewayClient`: Replace `assert.Equal(t, 0, client.currentIndex)` with `assert.Equal(t, uint64(0), client.currentIndex.Load())`
  - `TestSelectHealthyGateway/"skips unhealthy gateways"` (line 260-276): Trace through the new atomic logic. The monotonic counter changes the sequence: with gw1/gw2(unhealthy)/gw3, the current code alternates gw1→gw3→gw1; the new code may produce gw1→gw3→gw3→gw1. **Update test assertions to match the actual new behavior.** This is an intentional, acceptable tradeoff — distribution is still fair overall.
  - `TestSelectHealthyGateway/"skips unhealthy gateways"` test: Trace through carefully — call 1: counter 0→1, start=0, finds gw1(healthy). Call 2: counter 1→2, start=1, gw2 unhealthy, tries gw3(healthy). Call 3: counter 2→3, start=2, finds gw3(healthy). So sequence is gw1→gw3→gw3, not gw1→gw3→gw1. Update assertions accordingly.
- **Add concurrent correctness test:** `TestSelectHealthyGateway_ConcurrentDistribution` — spawn 100 goroutines each calling `selectHealthyGateway()` 1000 times, collect returned gateways into a thread-safe counter (sync.Map or mutex-protected map), assert each healthy gateway is selected within 25% of expected uniform distribution. This validates fairness under real concurrency.
- The monotonically increasing counter means we no longer "set" the index to a specific value; instead we just increment. The modulo operation handles wrap-around naturally. Over very long runtimes, `uint64` overflow is benign — modulo still produces valid indices.
- Counter advances unconditionally even on the all-unhealthy fallback path. This means on recovery after sustained failures, the starting position is effectively random. This is acceptable — the round-robin is for load distribution, not deterministic sequencing.

**Definition of Done:**

- [ ] `currentIndex` is `atomic.Uint64` in `GatewayClient` struct
- [ ] INVARIANT comment added at `gatewayStatus` field declaration
- [ ] `selectHealthyGateway()` uses `RLock()` instead of `Lock()`
- [ ] `selectHealthyGateway()` uses atomic increment for round-robin
- [ ] TOCTOU tradeoff documented in code comment
- [ ] All existing tests pass (updated assertions where needed)
- [ ] Concurrent distribution test passes
- [ ] No data races when run with `-race` flag
- [ ] `go vet ./internal/usecase/ipfs_streaming/` reports no issues

**Verify:**

- `cd /Users/yosefgamble/github/athena && go test -v ./internal/usecase/ipfs_streaming/` — all tests pass
- `cd /Users/yosefgamble/github/athena && go test -race ./internal/usecase/ipfs_streaming/` — no race conditions
- `cd /Users/yosefgamble/github/athena && go vet ./internal/usecase/ipfs_streaming/` — no issues

### Task 3: Run benchmarks, verify improvement, and create PR

**Objective:** Run benchmarks against the optimized code, compare with baseline using `benchstat`, document results, and create a PR.

**Dependencies:** Task 2

**Files:**

- No file changes — this is a measurement, verification, and PR creation task

**Key Decisions / Notes:**

- Run the same `BenchmarkSelectHealthyGateway` benchmarks from Task 1 with identical `-count=5`
- Save optimized results: `go test -bench=... -count=5 ... | tee /tmp/bench-optimized.txt`
- Compare with `benchstat /tmp/bench-baseline.txt /tmp/bench-optimized.txt`
- If `benchstat` not available: `go install golang.org/x/perf/cmd/benchstat@latest`
- The primary improvement should be visible in the Parallel and Mixed benchmarks where multiple goroutines previously serialized on the exclusive lock
- Serial performance may show marginal improvement or stay similar (atomic vs mutex single-threaded cost is comparable)
- Run the full test suite one final time with `-race` to confirm everything passes
- Create PR with `gh pr create` including benchmark comparison in the body

**Definition of Done:**

- [ ] Benchmark results captured with -count=5 (both serial and parallel)
- [ ] benchstat comparison run against baseline
- [ ] Full test suite passes with `-race` flag
- [ ] PR created with benchmark results in description

**Verify:**

- `cd /Users/yosefgamble/github/athena && go test -bench=BenchmarkSelectHealthyGateway -benchmem -count=5 ./internal/usecase/ipfs_streaming/ | tee /tmp/bench-optimized.txt` — benchmarks complete
- `benchstat /tmp/bench-baseline.txt /tmp/bench-optimized.txt` — comparison shows results
- `cd /Users/yosefgamble/github/athena && go test -race ./internal/usecase/ipfs_streaming/` — no race conditions

## Testing Strategy

- **Benchmarks:** `BenchmarkSelectHealthyGateway` with 4 sub-cases: `Serial`, `Parallel`, `Parallel_MixedHealth`, and `Mixed_ReadWrite` — measure single-threaded, concurrent read, mixed-health, and realistic read-write workload performance
- **Concurrent correctness:** `TestSelectHealthyGateway_ConcurrentDistribution` — validates fair round-robin distribution under 100-goroutine contention
- **Unit tests:** All existing tests in `gateway_client_test.go` must continue to pass (with updated assertions for behavioral changes)
- **Race detection:** Run with `-race` flag to ensure no data races introduced
- **Static analysis:** `go vet` must pass
- **Benchmark comparison:** `benchstat` for statistically reliable before/after comparison

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Round-robin semantics change with unhealthy gateways (monotonic counter advances past starting position, not found position) | Medium | Low | Distribution is still fair overall. Update test assertions for the new sequence. Add concurrent distribution test to validate fairness. The difference is only visible with unhealthy gateways and doesn't affect load balancing quality. |
| TOCTOU gap between atomic increment and RLock acquisition — under very high concurrency with simultaneous health status changes, two goroutines may briefly select the same gateway | Low | Low | The window is extremely small (nanoseconds between atomic op and RLock). Duplicate selection of a healthy gateway is not harmful — it just temporarily reduces distribution uniformity. The benefit of read parallelism far outweighs occasional duplicate selection. Document with code comment. |
| uint64 overflow after extremely long runtime | Very Low | None | Modulo of overflowed uint64 still produces valid index — Go unsigned integer overflow is well-defined |
| RLock insufficient for reading gatewayStatus fields | Low | High | gatewayStatus map keys never change after construction (INVARIANT documented at field). Only value fields change under write lock. RLock correctly synchronizes reads with those writes. |
| Future maintainer adds dynamic gateway discovery, modifying map keys under write lock while RLock readers iterate | Low | High | INVARIANT comment at field declaration makes assumption explicit. Any modification to map key set requires upgrading all RLock callers. |
| Counter advances during all-unhealthy fallback, causing non-deterministic starting position on recovery | Low | Low | Acceptable — round-robin is for load distribution, not deterministic sequencing. After recovery, distribution converges to uniform within a few calls. |

## Rollback

Rollback: `git revert <commit>`. Changes are contained to `gateway_client.go` and `gateway_client_test.go`. No database migrations, config changes, or API contract changes are involved.

## Open Questions

The atomic + RLock approach is well-established in Go concurrency patterns. Minor behavioral differences in round-robin distribution under mixed health states have been analyzed and are acceptable tradeoffs (see Risks table).

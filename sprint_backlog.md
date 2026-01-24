# Sprint Backlog: Operation Bedrock

## High Priority (Blockers)

### 1. Fix Test Infrastructure (Docker Rate Limits)
**Problem**: `docker compose up` fails due to unauthenticated pull rate limits from Docker Hub.
**Solution**:
*   Configure CI/Developer environment with authenticated Docker Hub credentials.
*   OR: Switch to a public mirror or cached registry.
*   **Workaround**: Developer instructions for `docker login`.

### 2. Implement "Fail Fast" in Test Helpers
**Problem**: `internal/repository` tests take >400s to fail when DB is missing because each test retries connection for 5s.
**Solution**:
*   Modify `internal/testutil/database.go` -> `SetupTestDB`.
*   Check for global "DB Unavailable" flag or check connection *once* at package init level.
*   If DB unavailable, `t.SkipNow()` immediately with 0 delay.

### 3. Verify `internal/repository` Tests
**Problem**: Tests have not been verified recently due to timeouts.
**Action**:
*   Once Task 1/2 is done, run `go test -v ./internal/repository`.
*   Fix any SQL errors, schema mismatches, or logic bugs.

### 4. Verify `internal/ipfs` Tests
**Problem**: Integration tests requiring IPFS node are skipped.
**Action**:
*   Ensure IPFS container starts.
*   Run tests and fix `CID` validation or connection logic if needed.

## Medium Priority

### 5. Update Documentation
**Problem**: `README.md` claims "Production Ready" but tests are not passing in fresh environments.
**Action**:
*   Add "Prerequisites" section (Docker with auth).
*   Clarify "Conditional Go" status.

### 6. Refactor `internal/database` Tests
**Problem**: `TestPool_StatsUnderLoad` was flaky (timeout).
**Action**:
*   Review `go-sqlmock` usage to ensure determinism.
*   Consider reducing `Sleep` times or using channels for synchronization.

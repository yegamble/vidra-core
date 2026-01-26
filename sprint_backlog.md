# Sprint Backlog: Operation Bedrock

## In Progress

### Task 1: Implement "Fail Fast" in Test Helpers
**Assignee**: Builder 🛠️
**Priority**: High (Blocker)
**Description**:
Currently, `SetupTestDB` retries connections for every test, leading to massive timeouts when the DB is missing.
**Requirements**:
- Modify `internal/testutil/database.go` to use `sync.Once` for the initial connection check.
- Store the result in a global variable (e.g., `dbAvailable`).
- If `dbAvailable` is false, `SetupTestDB` must `t.SkipNow()` immediately (0ms delay).
- Ensure this state persists across the entire test suite execution.

### Task 2: Verify "Fail Fast" Mechanism
**Assignee**: QA Guardian 🧪
**Priority**: High
**Description**:
Confirm that the fix in Task 1 actually works.
**Steps**:
- Stop all Docker containers (`docker compose down`).
- Run `go test -v ./internal/repository/...`.
- **Expected Result**: Tests should finish in < 5 seconds (all skipped), not 400s+.

### Task 3: Verify `internal/repository` Tests
**Assignee**: QA Guardian 🧪
**Priority**: High
**Description**:
Run the core repository tests against a real database to establish a baseline.
**Steps**:
- Start DB: `docker compose -f docker-compose.test.yml up -d postgres-test redis-test`.
- Run: `go test -v ./internal/repository/...`.
- **Output**: Create GitHub Issues for any failures. Do not fix complex bugs yet; just log them.

### Task 4: Verify `internal/ipfs` Tests
**Assignee**: QA Guardian 🧪
**Priority**: Medium
**Description**:
Run IPFS integration tests.
**Steps**:
- Start IPFS: `docker compose -f docker-compose.test.yml up -d ipfs-test`.
- Run: `go test -v ./internal/ipfs/...`.
- **Output**: Log failures.

### Task 5: Update Documentation
**Assignee**: Scribe 📝
**Priority**: Medium
**Description**:
Update `README.md` to reflect the current test environment requirements.
**Requirements**:
- Add "Prerequisites" section: "Docker with authenticated pull (or mirror) is required to run integration tests."
- Explain the "Fail Fast" behavior: "Tests will automatically skip if DB is not on port 5433."

## Todo (Next Sprint)
- Fix identified bugs from Task 3 & 4.
- Implement caching/mirroring for Docker in CI.

# Sprint Backlog: Operation Bedrock

## 1. Optimize "Fail Fast" in Test Helpers (Blocker)
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** In Progress

**Description:**
The current `verifyInfra` in `internal/testutil/database.go` (called by `SetupTestDB`) attempts to connect to the database with a 2-second timeout. Since Go executes tests for different packages in separate processes, this 2-second delay occurs for *every* package, causing massive slowdowns (minutes) when infrastructure is not running.

**Tasks:**
- [ ] Modify `verifyInfra` in `internal/testutil/database.go`.
- [ ] Replace the existing `connectWithRetry` call with a fast TCP check: `net.DialTimeout("tcp", dbHost+":"+dbPort, 100*time.Millisecond)`.
- [ ] Apply similar logic for Redis.
- [ ] Ensure `SetupTestDB` checks this result and calls `t.Skip` immediately if infra is missing.

**Acceptance Criteria:**
- Running `go test ./...` without Docker running completes (skips all) in < 1 second total.

---

## 2. Verify and Fix Repository Tests
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
Integration tests in `internal/repository` have not been verified recently. They likely contain SQL syntax errors or schema mismatches (e.g., `updated_at` triggers, new columns) compared to the current codebase state.

**Tasks:**
- [ ] Start test infrastructure (`docker compose up -d postgres redis`).
- [ ] Run `go test -v ./internal/repository/...`.
- [ ] Analyze failures (look for "column does not exist", "syntax error", or type mismatches).
- [ ] Fix SQL queries in `internal/repository` or schema definitions in `internal/testutil/database.go` to match.

**Acceptance Criteria:**
- All tests in `internal/repository` pass when a test DB is available.

---

## 3. Update Documentation
**Assignee:** Scribe 📝
**Priority:** Medium
**Status:** To Do

**Description:**
The `README.md` needs to reflect the current "Stabilization Phase" and provide critical setup information to avoid confusion.

**Tasks:**
- [ ] Add "Prerequisites" section to `README.md` explaining the need for `docker login` to avoid rate limits.
- [ ] Update "Project Status" to "Stabilization Phase".
- [ ] Add a "Troubleshooting" section for common test failures (e.g., "Postgres not ready").

**Acceptance Criteria:**
- `README.md` accurately guides a new developer and warns about known issues.

---

## 4. Verify and Fix IPFS Tests (Deferred)
**Assignee:** Builder 🛠️
**Priority:** Low
**Status:** Backlog

**Description:**
Integration tests involving IPFS (`internal/ipfs`) need verification. Deferred to next sprint to focus on Core DB reliability first.

# Sprint Backlog: Operation Bedrock

## 1. Implement "Fail Fast" in Test Helpers (Blocker)
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
The current `SetupTestDB` function in `internal/testutil/database.go` attempts to connect to the database for every single test package, often retrying for 5 seconds if the DB is missing. This makes running the full suite locally extremely slow if services aren't running.

**Tasks:**
- [ ] Modify `internal/testutil/database.go` to use `sync.Once` for checking Postgres and Redis availability.
- [ ] Store the availability status in a global variable (e.g., `infraAvailable bool`).
- [ ] Update `SetupTestDB` to check this flag immediately. If false, call `t.Skip("Skipping: Infra unavailable")` without attempting a connection.

**Acceptance Criteria:**
- Running `go test ./internal/repository/...` without Docker running completes (skips) in < 5 seconds.

---

## 2. Verify and Fix Repository Tests
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
Integration tests in `internal/repository` have not been verified recently due to the infrastructure issues. They likely contain SQL syntax errors or schema mismatches (e.g., `updated_at` triggers, new columns).

**Tasks:**
- [ ] Run `go test -v ./internal/repository/...` (requires running DB).
- [ ] Identify failures.
- [ ] Fix SQL queries, mock data, or schema definitions in `internal/testutil/database.go` to match the actual code.

**Acceptance Criteria:**
- All tests in `internal/repository` pass when a test DB is available.

---

## 3. Verify and Fix IPFS Tests
**Assignee:** Builder 🛠️
**Priority:** Medium
**Status:** To Do

**Description:**
Integration tests involving IPFS (`internal/ipfs`) need verification to ensure they handle connection failures gracefully and pass when IPFS is present.

**Tasks:**
- [ ] Run `go test -v ./internal/ipfs/...`.
- [ ] Ensure tests skip gracefully if IPFS is missing (check `SetupTestDB` logic or similar).
- [ ] Fix any logic errors in content addressing or gateway interaction.

**Acceptance Criteria:**
- IPFS tests pass when `ipfs-ci` container is running.
- IPFS tests skip gracefully when container is missing.

---

## 4. Update Documentation
**Assignee:** Scribe 📝
**Priority:** Medium
**Status:** To Do

**Description:**
The `README.md` claims "Production Ready" but lacks critical setup information for the current environment (Docker rate limits).

**Tasks:**
- [ ] Add "Prerequisites" section to `README.md` explaining the need for `docker login` to avoid rate limits.
- [ ] Update "Project Status" to "Stabilization Phase" (Operation Bedrock).
- [ ] Add a "Troubleshooting" section for common test failures (e.g., "Postgres not ready").

**Acceptance Criteria:**
- `README.md` is accurate and helpful for a new developer setting up the repo.

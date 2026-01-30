# Sprint Backlog: Operation Bedrock

## 1. Verify "Fail Fast" Logic
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Verify

**Description:**
The `SetupTestDB` function in `internal/testutil/database.go` has been updated to use `sync.Once` and check for infra availability. We need to verify this actually skips tests fast (< 1s) when no Docker containers are running.

**Tasks:**
- [ ] Run `go test ./internal/repository/...` without Docker.
- [ ] Confirm execution time is < 5 seconds.
- [ ] If slow, debug and fix `internal/testutil/database.go`.

**Acceptance Criteria:**
- Test suite skips immediately when infra is missing.

---

## 2. Verify and Fix Repository Tests
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
Integration tests in `internal/repository` need to be verified against a running Postgres instance. They likely contain stale SQL queries or schema mismatches.

**Tasks:**
- [ ] Start Postgres: `docker compose up -d postgres redis`.
- [ ] Run `go test -v ./internal/repository/...`.
- [ ] Fix any SQL errors, schema mismatches, or logic bugs.

**Acceptance Criteria:**
- All tests in `internal/repository` pass with a real DB.

---

## 3. Verify and Fix IPFS Tests
**Assignee:** Builder 🛠️
**Priority:** Medium
**Status:** To Do

**Description:**
Integration tests involving IPFS (`internal/ipfs`) need verification.

**Tasks:**
- [ ] Start IPFS: `docker compose up -d ipfs` (or use `tests/e2e/docker-compose.yml` if needed).
- [ ] Run `go test -v ./internal/ipfs/...`.
- [ ] Fix any connection/logic errors.

**Acceptance Criteria:**
- IPFS tests pass when `ipfs` container is running.
- IPFS tests skip gracefully when container is missing.

---

## 4. Update Documentation
**Assignee:** Scribe 📝
**Priority:** Medium
**Status:** To Do

**Description:**
Update `README.md` to reflect the current state and provide clear instructions for the verified workflow.

**Tasks:**
- [ ] Add "Prerequisites" section (Docker Login, Go 1.21+).
- [ ] Document the "Fail Fast" behavior.
- [ ] Update "Project Status" to "Stabilization Phase".

**Acceptance Criteria:**
- `README.md` is accurate and helpful.

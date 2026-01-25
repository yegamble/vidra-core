# Sprint Backlog: Operation Bedrock

## 🚨 Critical Path (Blockers)

### Issue #1: Fail-Fast Test Infrastructure
**Assignee**: 🛠️ Builder
**Priority**: Critical
**Status**: To Do
**Description**:
Currently, running `make test` without Docker services running causes a massive timeout (>400s) because every test package retries the DB connection for 5 seconds.
We need a global "check once" mechanism or a faster failure mode in `internal/testutil/database.go`.

**Acceptance Criteria**:
- [ ] `internal/testutil/database.go` checks for DB connectivity *once* with a short timeout (e.g., 1s) before attempting retries.
- [ ] If DB is unreachable, `SetupTestDB` calls `t.SkipNow()` immediately.
- [ ] `make test-unit` completes in < 1 minute even if Docker is down.
- [ ] CI integration tests still pass (services are available there).

### Issue #2: Verify User Repository Tests
**Assignee**: 🧪 QA Guardian
**Priority**: High
**Status**: Blocked by #1
**Description**:
User repository tests (`internal/repository/user_repository_test.go`) are the foundation of auth. They need to be verified against the current schema.

**Acceptance Criteria**:
- [ ] `go test -v ./internal/repository/user_repository_test.go` passes locally with Docker services up.
- [ ] All SQL queries in `user_repository.go` match the schema in `migrations/`.
- [ ] No regression in `CreateUser`, `GetUserByEmail`, or `UpdateUser`.

### Issue #3: Update Developer Documentation
**Assignee**: 📝 Scribe
**Priority**: High
**Status**: To Do
**Description**:
The `README.md` does not explicitly warn developers about Docker Hub rate limits or the need for `docker login`. It also claims "Production Ready" which is misleading given the current test state.

**Acceptance Criteria**:
- [ ] `README.md` includes a "Prerequisites" section listing Docker (authenticated), Go 1.24, and Make.
- [ ] `README.md` clarifies the "Stabilization Phase" status.
- [ ] `docs/development/TESTING_STRATEGY.md` is updated to mention the "Fail Fast" behavior.

## ⚠️ Medium Priority

### Issue #4: Verify IPFS Integration Tests
**Assignee**: 🧪 QA Guardian
**Priority**: Medium
**Status**: To Do
**Description**:
`internal/ipfs` tests are often skipped. We need to ensure they run when IPFS is available.

**Acceptance Criteria**:
- [ ] IPFS tests run and pass when `IPFS_API` is set.
- [ ] IPFS tests skip gracefully if `IPFS_API` is missing (covered by Issue #1 logic?).

### Issue #5: Refactor Flaky DB Tests
**Assignee**: 🛠️ Builder
**Priority**: Medium
**Status**: To Do
**Description**:
`TestPool_StatsUnderLoad` in `internal/database` is flaky.

**Acceptance Criteria**:
- [ ] Test is deterministic or removed if not adding value.

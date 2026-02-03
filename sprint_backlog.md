# Sprint Backlog: Operation Bedrock & Secure Launch

## 1. Optimize "Fail Fast" in Test Helpers (Blocker)
**Assignee:** Builder 🛠️
**Priority:** Blocker
**Status:** In Progress

**Description:**
The current `verifyInfra` in `internal/testutil/database.go` uses a slow connection retry loop (waiting up to 2 seconds per package). This makes running `go test ./...` extremely slow when infrastructure is not running.

**Tasks:**
- [ ] Modify `verifyInfra` in `internal/testutil/database.go`.
- [ ] Replace `connectWithRetry` calls with `net.DialTimeout("tcp", host:port, 100ms)`.
- [ ] Apply this fast check for both Postgres and Redis.
- [ ] Ensure `SetupTestDB` calls `t.Skip` immediately if the TCP check fails.

**Acceptance Criteria:**
- `go test ./...` (without Docker) completes in < 1 second total.

---

## 2. Security: Credential Rotation Scripts (Critical)
**Assignee:** Sentinel 🛡️
**Priority:** Critical
**Status:** To Do

**Description:**
As per `docs/security/SECURITY_ADVISORY.md`, credentials were exposed. We need scripts to facilitate the rotation process.

**Tasks:**
- [ ] Create `scripts/rotate-credentials.sh` to generate strong random secrets for JWT, DB passwords, etc.
- [ ] Create `scripts/setup-production-env.sh` (or update instructions) to help operators apply these new secrets.
- [ ] Verify the script output meets complexity requirements.

**Acceptance Criteria:**
- Scripts exist and function correctly.
- Documentation explains how to use them for rotation.

---

## 3. Security: Git History Cleanup Guide (Critical)
**Assignee:** Sentinel 🛡️
**Priority:** Critical
**Status:** To Do

**Description:**
The `.env` file must be purged from git history. Since this is destructive, we need a clear, tested guide or helper script.

**Tasks:**
- [ ] Create `docs/security/GIT_HISTORY_CLEANUP.md` or `scripts/clean-git-history.sh` (helper).
- [ ] Document the exact `git filter-branch` or `bfg` commands required.
- [ ] Add warnings about "Force Push" implications.

**Acceptance Criteria:**
- Clear, step-by-step guide for purging the specific exposed file.

---

## 4. Verify and Fix Repository Tests (High)
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
Integration tests in `internal/repository` need to be verified against the current schema.

**Tasks:**
- [ ] Start test infra (`docker compose up -d postgres redis`).
- [ ] Run `go test -v ./internal/repository/...`.
- [ ] Fix any SQL syntax errors or schema mismatches found.

**Acceptance Criteria:**
- All repository tests pass with a running DB.

---

## 5. Update Documentation (Medium)
**Assignee:** Scribe 📝
**Priority:** Medium
**Status:** To Do

**Description:**
Update `README.md` to reflect the "Stabilization Phase" and add "Prerequisites" (Docker Login). Update `CLAUDE.md` if necessary.

**Tasks:**
- [ ] Update `README.md` Project Status section.
- [ ] Add Docker Login prerequisite to `README.md`.
- [ ] Verify `docs/deployment/monitoring` exists and is referenced.

**Acceptance Criteria:**
- `README.md` is accurate and helpful for new devs.

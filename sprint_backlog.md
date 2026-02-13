# Sprint Backlog: Quality Programme - Sprint 15 (Stabilize & Integrate)

**Programme:** Athena Quality Programme (Sprints 15-20)
**Sprint Goal:** Merge/close/resolve the high-impact PR queue; stabilize mainline
**Sprint Duration:** Feb 16 - Mar 2, 2026
**Full Programme Details:** [docs/sprints/QUALITY_PROGRAMME.md](docs/sprints/QUALITY_PROGRAMME.md)

---

## Quick Reference: PR Queue Status

| PR # | Title | Priority | Status |
|------|-------|----------|--------|
| #229 | Fix hardcoded secrets and JWT configuration | P0 | Pending Review |
| #235 | Fix argument injection in yt-dlp wrapper | P0 | Pending Review |
| #242 | Enforce strict request size limits | P1 | Pending Review |
| #227 | Fix OpenAPI HLS wildcard path | P0 | Consolidate with #231 |
| #231 | Fix OpenAPI generation and build errors | P0 | Consolidate with #227 |
| #240 | Exclude ClamAV from integration jobs | P1 | Pending Review |
| #238 | Fix flaky DB pool tests | P1 | Pending Review |
| #234 | Fix lint config and Makefile | P1 | Pending Review |
| #244 | Add comment repository unit tests | P2 | Pending Review |
| #233 | WIP documentation inconsistencies | P3 | Close (empty) |

---

## P0: Critical Security (Merge ASAP)

### 1. Hardcoded Secrets + Secure JWT Defaults
**PR:** #229
**Assignee:** Sentinel
**Priority:** P0 - Critical
**Status:** Pending Review

**Description:**
Remove hardcoded/leaked secrets and enforce secure JWT secret rules. Production must refuse to start with insecure defaults.

**Tasks:**
- [ ] Review PR #229 for completeness
- [ ] Verify secrets removed from docs/compose
- [ ] Verify app refuses insecure secrets in production mode
- [ ] Verify unit tests cover secure default behavior
- [ ] Merge PR

**Acceptance Criteria:**
- Secrets removed from repository
- Production mode refuses insecure JWT secrets
- Unit tests validate behavior

**Post-Merge Actions:**
- Rotate all credentials per `docs/security/CREDENTIAL_ROTATION_GUIDE.md`
- Consider git history cleanup per `scripts/clean-git-history.sh`

---

### 2. yt-dlp Argument Injection Fix
**PR:** #235
**Assignee:** Sentinel
**Priority:** P0 - Critical
**Status:** Pending Review

**Description:**
Fix argument injection vulnerability in yt-dlp wrapper by inserting `--` delimiter before user-supplied URLs.

**Tasks:**
- [ ] Review PR #235 for completeness
- [ ] Verify `--` delimiter is inserted before URLs
- [ ] Verify security regression test exists
- [ ] Merge PR

**Acceptance Criteria:**
- CLI args cannot be interpreted as flags
- Regression test proves delimiter usage
- URL scheme validation in place

---

### 3. OpenAPI Generation Consolidation
**PRs:** #227, #231 (choose one, close duplicate)
**Assignee:** Builder
**Priority:** P0 - Build
**Status:** Needs Consolidation

**Description:**
Two PRs address OpenAPI wildcard path syntax and regeneration issues. Consolidate into one.

**Tasks:**
- [ ] Compare PR #227 and #231 approaches
- [ ] Choose canonical PR (recommend #231 as more comprehensive)
- [ ] Rebase chosen PR on main
- [ ] Verify `make generate-openapi` works on clean checkout
- [ ] Verify CI validates generated types
- [ ] Merge chosen PR
- [ ] Close duplicate PR

**Acceptance Criteria:**
- `make generate-openapi` works reproducibly
- CI catches generated code drift
- Single source of truth for OpenAPI spec

---

## P1: Security & CI Hardening

### 4. Request Size Limits (DoS Mitigation)
**PR:** #242
**Assignee:** Sentinel
**Priority:** P1 - Security
**Status:** Pending Review

**Description:**
Enforce strict request body size limits with route-specific overrides for upload endpoints.

**Tasks:**
- [ ] Review PR #242 for completeness
- [ ] Verify default cap is enforced
- [ ] Verify upload endpoints allow configured larger sizes
- [ ] Verify unit tests pass
- [ ] Merge PR
- [ ] Document limits in OpenAPI and upload docs

**Acceptance Criteria:**
- Default request size limit enforced
- Upload routes have appropriate overrides
- Documented in API specification

---

### 5. CI Integration Test Optimization (ClamAV)
**PR:** #240
**Assignee:** Gatekeeper
**Priority:** P1 - CI
**Status:** Pending Review

**Description:**
Speed up integration tests by excluding ClamAV from jobs that don't need it.

**Tasks:**
- [ ] Review PR #240 for completeness
- [ ] Verify integration jobs no longer wait for ClamAV
- [ ] Verify virus-scanner workflow still covers ClamAV paths
- [ ] Merge PR

**Acceptance Criteria:**
- Integration jobs faster (no ClamAV wait)
- Virus scanner tests remain in dedicated workflow

---

### 6. Flaky DB Pool Test Fixes
**PR:** #238
**Assignee:** QA
**Priority:** P1 - Test Stability
**Status:** Pending Review

**Description:**
Fix flakiness and deadlock risk in database pool tests by improving timeouts and relaxing ordered expectations.

**Tasks:**
- [ ] Review PR #238 for completeness
- [ ] Verify database pool tests do not hang
- [ ] Verify concurrency tests use timeouts
- [ ] Verify flaky asserts removed
- [ ] Merge PR

**Acceptance Criteria:**
- DB pool tests pass reliably
- No test hangs or deadlocks
- Deterministic assertions

---

### 7. Lint Config + Makefile Portability
**PR:** #234
**Assignee:** Gatekeeper
**Priority:** P1 - CI
**Status:** Pending Review

**Description:**
Fix broken lint configuration and Makefile portability issues.

**Tasks:**
- [ ] Review PR #234 for completeness
- [ ] Verify `make lint` works cross-platform
- [ ] Verify lint config updated (no deprecated options)
- [ ] Merge PR

**Acceptance Criteria:**
- `make lint` works on Linux/macOS
- Lint versions locked
- Developer setup documented

---

## P2: Coverage Uplift

### 8. Comment Repository Unit Tests
**PR:** #244
**Assignee:** Builder
**Priority:** P2 - Coverage
**Status:** Pending Review

**Description:**
Add repository-level unit tests for comments using sqlmock patterns.

**Tasks:**
- [ ] Review PR #244 for completeness
- [ ] Verify tests follow sqlmock pattern
- [ ] Verify coverage improvement
- [ ] Merge PR
- [ ] Document pattern for other repository packages

**Acceptance Criteria:**
- Comment repository has unit tests
- Pattern established for other repos
- Coverage baseline improved

---

## P3: Cleanup

### 9. Close Empty Documentation PR
**PR:** #233
**Assignee:** Maintainer
**Priority:** P3 - Hygiene
**Status:** To Close

**Description:**
Draft PR with 0 changed files. Close or convert to tracking issue.

**Tasks:**
- [ ] Review PR #233
- [ ] Close PR (or convert to issue if specific documentation gaps identified)

**Acceptance Criteria:**
- No-op PR removed from queue

---

## Sprint 15 Metrics

### Post-Sprint Targets

| Metric | Target |
|--------|--------|
| Open security PRs | 0 |
| Open CI/build PRs | 0 |
| CI passing on main | Yes |
| Coverage baseline documented | Yes |

---

## Legacy Items (From Operation Bedrock)

### Credential Rotation Scripts
**Assignee:** Sentinel
**Priority:** Medium
**Status:** Partially Done

**Description:**
As per `docs/security/SECURITY_ADVISORY.md`, scripts to facilitate credential rotation.

**Tasks:**
- [x] Create `scripts/rotate-credentials.sh` (Verified: script exists)
- [ ] Create `scripts/setup-production-env.sh`
- [ ] Verify script output meets complexity requirements
- [ ] Document usage in security guide

---

### Git History Cleanup Guide
**Assignee:** Sentinel
**Priority:** Medium
**Status:** Partially Done

**Description:**
Guide for purging exposed files from git history.

**Tasks:**
- [x] Create `scripts/clean-git-history.sh` (Verified: script exists)
- [ ] Create `docs/security/GIT_HISTORY_CLEANUP.md`
- [ ] Document `git filter-branch` or `bfg` commands
- [ ] Add warnings about force push implications

---

### Repository Tests Verification
**Assignee:** Builder
**Priority:** Medium
**Status:** Blocked

**Description:**
Verify integration tests against current schema.

**Tasks:**
- [x] Start test infra - Blocked by Docker rate limits
- [ ] Run `go test -v ./internal/repository/...`
- [ ] Fix any SQL syntax errors or schema mismatches

**Note:** Deferred to Sprint 16 after CI improvements merged.

---

## Upcoming Sprints (Preview)

| Sprint | Focus | Key Deliverables |
|--------|-------|------------------|
| **Sprint 16** | API Contract | OpenAPI CI enforcement, Postman smoke tests |
| **Sprint 17** | Coverage I | Core services at 100% unit coverage |
| **Sprint 18** | Coverage II | Handlers/repos at 90%+ coverage |
| **Sprint 19** | Documentation | Truth pass, runbook validation |
| **Sprint 20** | Release | Regression suite, rollback rehearsal |

See [QUALITY_PROGRAMME.md](docs/sprints/QUALITY_PROGRAMME.md) for full details.

---

## How to Use This Backlog

1. **Pick a task** from your assigned priority level
2. **Update status** in this file when starting work
3. **Create PR** following the checklist in task
4. **Update status** when PR is merged
5. **Run `make validate-all`** before claiming done

### Validation Command
```bash
make validate-all   # or ./scripts/validate-all.sh
```

This runs: `gofmt`, `goimports`, `golangci-lint` (with gosec), unit tests, and build verification.

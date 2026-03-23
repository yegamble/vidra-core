# Fix GitHub CI/CD Issues Plan

Created: 2026-03-02
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Bugfix

## Summary

**Goal:** Fix 5 distinct CI/CD failures on the main branch
**Root Causes:** Step ordering bug, env var override breaking
docker, stat command portability, shell function export
incompatibility, unreliable Go proxy
**Bug Condition (C):** CI workflows run on GitHub-hosted
Ubuntu runners (main branch push)
**Postcondition (P):** All 5 workflows pass: CI
(shell-tests), E2E Tests, Security Tests
**Symptom:** CI, E2E Tests, and Security workflows all fail on every push to main

## Behavior Contract

### Must Change (C ⟹ P)

1. **E2E: docker-cleanup before checkout** —
   WHEN e2e-tests job runs THEN docker-cleanup action
   can find its action.yml (checkout runs first)
2. **E2E: HOME=/root breaks docker compose** —
   WHEN e2e-tests job runs on GitHub-hosted runner
   THEN docker compose plugin loads correctly
   (no HOME override)
3. **CI: nginx Test 4 permissions** —
   WHEN nginx_test.sh runs on Linux THEN `stat`
   returns numeric permissions (not filesystem info)
4. **CI: nginx Test 13 HTTP fallback** —
   WHEN entrypoint test mocks missing openssl
   THEN child sh process cannot find openssl
5. **Security: goproxy.io 502** —
   WHEN go mod download runs THEN proxy chain
   uses reliable proxy first (proxy.golang.org)

### Must NOT Change (¬C ⟹ unchanged)

- Existing test suite covers preservation — changes are
  isolated to CI config, test scripts, and Go proxy config

## Scope

**Change:**

- `.github/workflows/e2e-tests.yml` — fix step ordering,
  remove HOME/GOCACHE/GOMODCACHE override
- `nginx/scripts/nginx_test.sh` — fix stat order, fix openssl mock propagation
- `.github/actions/setup-go-cached/action.yml` — fix proxy chain order
- `.github/workflows/security-tests.yml` — add explicit GOPROXY env

**Test:** Run nginx_test.sh locally, verify with `act` if available
**Out of scope:** Dependabot PRs (separate branch failures), test logic changes

## Context for Implementer

### Bug 1: E2E docker-cleanup before checkout

- **Root cause:** `e2e-tests.yml:48` —
  `uses: ./.github/actions/docker-cleanup` runs before
  `actions/checkout@v4` at line 52
- **Fix:** Move checkout before docker-cleanup, or inline the 3 docker cleanup commands

### Bug 2: E2E HOME=/root breaks docker compose

- **Root cause:** `e2e-tests.yml:43-45` — `HOME: /root`
  at job level causes docker to look for config/plugins
  in `/root/.docker/` which isn't accessible on
  GitHub-hosted runners (user is `runner`)
- **Fix:** Remove the `env:` block
  (HOME/GOCACHE/GOMODCACHE) — setup-go-cached already
  handles these with proper fallback logic
- **Evidence:** Error log shows
  `WARNING: Error loading config file:
  open /root/.docker/config.json: permission denied`
  then `unknown shorthand flag: 'f' in -f`

### Bug 3: nginx Test 4 — stat command portability

- **Root cause:** `nginx_test.sh:214` —
  `stat -f "%OLp"` on Linux means "filesystem status"
  (valid command, wrong output), so `||` fallback to
  `stat -c "%a"` never triggers
- **Fix:** Try Linux syntax first:
  `stat -c "%a" ... 2>/dev/null ||
  stat -f "%OLp" ... 2>/dev/null`

### Bug 4: nginx Test 13 — openssl mock not propagating

- **Root cause:** `nginx_test.sh:118-123` —
  `export -f openssl` doesn't work in dash
  (Ubuntu `/bin/sh`). The cert script `#!/bin/sh`
  runs as a child process and finds real openssl
- **Fix:** Use PATH manipulation: create a temp dir
  with a fake `openssl` script that exits 127,
  prepend to PATH

### Bug 5: Security — goproxy.io unreliable

- **Root cause:** `setup-go-cached/action.yml:270` —
  proxy chain is `goproxy.io,proxy.golang.org,direct`.
  Go only falls through on 404/410, NOT on 502.
  So goproxy.io 502 is terminal
- **Fix:** Change default proxy chain to
  `https://proxy.golang.org,direct`. Also add explicit
  `GOPROXY` in security-tests.yml for consistency

## Progress Tracking

- [x] Task 1: Fix E2E workflow (checkout ordering + HOME override)
- [x] Task 2: Fix nginx test portability (stat + openssl mock)
- [x] Task 3: Fix Go proxy chain reliability
- [x] Task 4: Verify all fixes
**Tasks:** 4 | **Done:** 4

## Implementation Tasks

### Task 1: Fix E2E workflow (checkout ordering + HOME override)

**Objective:** Fix the two E2E test bugs
**Files:** `.github/workflows/e2e-tests.yml`
**TDD Flow:**

1. Move `actions/checkout@v4` before `docker-cleanup`
   in both `e2e-tests` and `e2e-tests-race` jobs
2. Remove the job-level `env:` block
   (HOME, GOCACHE, GOMODCACHE) from both jobs —
   setup-go-cached handles these
3. Remove the duplicate "Generate test fixtures" step
   in e2e-tests-race (lines 189-196 and 204-211)
**Verify:** `act -j e2e-tests --dryrun` or manual review

### Task 2: Fix nginx test portability (stat + openssl mock)

**Objective:** Make nginx_test.sh pass on both macOS and Ubuntu
**Files:** `nginx/scripts/nginx_test.sh`
**TDD Flow:**

1. Fix Test 4: Swap stat order — `stat -c "%a"` first
   (Linux), `stat -f "%OLp"` fallback (macOS)
2. Fix Test 13: Replace `mock_openssl_missing` with
   PATH-based approach — create temp dir with fake
   openssl binary, prepend to PATH before running
   entrypoint
3. Run `bash nginx/scripts/nginx_test.sh` locally to verify
**Verify:** `bash nginx/scripts/nginx_test.sh`

### Task 3: Fix Go proxy chain reliability

**Objective:** Use reliable proxy (proxy.golang.org) as primary
**Files:** `.github/actions/setup-go-cached/action.yml`,
`.github/workflows/security-tests.yml`
**TDD Flow:**

1. Change proxy chain in setup-go-cached from
   `goproxy.io,proxy.golang.org,direct` to
   `https://proxy.golang.org,direct`
2. Add `GOPROXY: "https://proxy.golang.org,direct"`
   to security-tests.yml env block
**Verify:** Manual review — proxy reliability is
an external dependency

### Task 4: Verify all fixes

**Objective:** Run local validation and test
**Files:** N/A
**TDD Flow:**

1. Run `bash nginx/scripts/nginx_test.sh` —
   all 13 tests pass
2. Run `make validate-all` or at minimum
   `make build && make test-unit`
3. Review all changed workflow files for consistency
**Verify:** All local tests pass, workflow files are
valid YAML

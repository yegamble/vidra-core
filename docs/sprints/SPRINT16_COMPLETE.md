# Sprint 16: API Contract Reproducibility - Completion Report

**Sprint Duration:** Feb 13, 2026
**Status:** Complete
**Programme:** Quality Programme (Sprints 15-20)

---

## Sprint Goal

Make the API contract stable and reproducible with CI enforcement, Postman smoke tests on PR, and documented governance.

---

## Achievements

### 1. OpenAPI Generation Enforced in CI (5 pts)

**Problem:** Developers could change `api/openapi.yaml` without regenerating `internal/generated/types.go`, causing drift between spec and code.

**Solution:**

- Created `scripts/verify-openapi.sh` - Regenerates types and fails if the result differs from what is committed
- Added `verify-codegen` job to `openapi-ci.yml` workflow - Runs after spec validation, installs `oapi-codegen`, and calls `make verify-openapi`
- Added `make verify-openapi` Makefile target for local use
- Fixed duplicate shebang in `scripts/gen-openapi.sh`
- Extended path triggers to also watch `internal/generated/`, `scripts/gen-openapi.sh`, and `scripts/verify-openapi.sh`

**Files Changed:**

- `scripts/gen-openapi.sh` - Fixed duplicate shebang
- `scripts/verify-openapi.sh` - New: drift detection script
- `.github/workflows/openapi-ci.yml` - Added `verify-codegen` job and GO_VERSION env
- `Makefile` - Added `verify-openapi` target

### 2. Postman Smoke Workflow on PR (8 pts)

**Problem:** No automated API smoke testing on pull requests. Postman collections existed but were only runnable locally via `make postman-e2e`.

**Solution:**

- Created `.github/workflows/postman-smoke.yml` - Full CI workflow that:
  - Builds the Docker image
  - Starts test services (postgres-test, redis-test, app-test) via docker compose
  - Waits for all services to be healthy
  - Runs Newman against the auth collection
  - Uploads test results as artifacts
  - Collects and uploads app logs on failure
  - Cleans up all containers
- Triggers on PRs to main (excludes docs-only changes)
- 15-minute timeout to bound runtime

**Files Changed:**

- `.github/workflows/postman-smoke.yml` - New: Postman smoke test workflow

### 3. Federation Endpoints Documented (5 pts)

**Finding:** All federation well-known endpoints were **already documented** in `api/openapi_federation.yaml`:

| Endpoint | Line | Schema |
|----------|------|--------|
| `/.well-known/atproto-did` | 18 | DIDDocument |
| `/.well-known/webfinger` | 37 | WebFingerResponse |
| `/.well-known/nodeinfo` | 63 | NodeInfo links |
| `/.well-known/host-meta` | 88 | XRD+XML |
| `/nodeinfo/2.0` | 103 | NodeInfo |

Plus ActivityPub actor, inbox, outbox, followers, following, and shared inbox endpoints. No changes needed.

### 4. API Review Checklist in PR Template (2 pts)

**Problem:** No PR template existed, so contributors had no standard checklist for API changes.

**Solution:**

- Created `.github/PULL_REQUEST_TEMPLATE.md` with sections:
  - **Summary** - What changed and why
  - **General checklist** - Tests, lint, format, no secrets, build
  - **API Changes** - Spec updated, types regenerated, schemas documented, error codes, breaking changes
  - **Security** - Input validation, auth, SQL injection, command injection, rate limiting
  - **Database** - Migration reversibility, indexes, local testing
  - **Test Plan** - Steps to reproduce/verify

**Files Changed:**

- `.github/PULL_REQUEST_TEMPLATE.md` - New: PR template with API review checklist

### 5. API Contract Policy Document (3 pts)

**Problem:** No documented source of truth for how API contracts are managed and changed.

**Solution:**

- Created `docs/API_CONTRACT_POLICY.md` covering:
  - **Source of truth** - OpenAPI specs in `api/` with file-to-scope mapping
  - **Change process** - Spec-first workflow (update spec, regenerate, implement, test, verify)
  - **CI enforcement** - Table of automated checks and their triggers
  - **Breaking changes** - Definition, process, migration guidance, versioning strategy
  - **Non-breaking changes** - Safe modifications that don't require special handling
  - **Federation endpoints** - Stricter compatibility requirements for well-known endpoints
  - **Review checklist** - Cross-references PR template

**Files Changed:**

- `docs/API_CONTRACT_POLICY.md` - New: API contract governance document

---

## Acceptance Criteria

- [x] OpenAPI generation enforced in CI (verify-codegen job fails on drift)
- [x] Postman smoke tests run on PR (postman-smoke.yml workflow)
- [x] Federation endpoints documented (already in openapi_federation.yaml)
- [x] API change review process documented (PR template + policy doc)

---

## Files Changed in Sprint 16

| File | Action | Description |
|------|--------|-------------|
| `scripts/gen-openapi.sh` | Modified | Fixed duplicate shebang |
| `scripts/verify-openapi.sh` | New | Drift detection script |
| `.github/workflows/openapi-ci.yml` | Modified | Added verify-codegen job |
| `.github/workflows/postman-smoke.yml` | New | Postman smoke test workflow |
| `.github/PULL_REQUEST_TEMPLATE.md` | New | PR template with API review checklist |
| `docs/API_CONTRACT_POLICY.md` | New | API contract governance document |
| `Makefile` | Modified | Added verify-openapi target |

---

## Test Summary

| Area | Verification | Status |
|------|-------------|--------|
| OpenAPI spec validation | Existing CI (swagger-cli) | Passing |
| Codegen drift detection | New CI job (verify-codegen) | Configured |
| Postman smoke tests | New CI workflow (postman-smoke) | Configured |
| Federation docs | Verified in openapi_federation.yaml | Already complete |
| PR template | GitHub renders on PR creation | Configured |
| Policy doc | Reviewed for completeness | Complete |

---

## Next: Sprint 17 - Core Services 100% Coverage

Focus areas:

1. Establish per-package coverage thresholds in CI
2. Add missing domain model tests (target: 100%)
3. Add missing usecase service tests (target: 100%)
4. Add property-style tests for input validation
5. Add concurrency/race tests for job processing

---

**Sprint 16: COMPLETE**

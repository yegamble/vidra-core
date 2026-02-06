## 2026-01-24 - CI Workflow Refactoring & Test Fix
**Incident/Change:** Refactored `test.yml` to use composite actions (`setup-go-cached`) and fixed a broken scheduler test.
**Analysis:** The `test.yml` workflow had significant duplication of setup steps across jobs. Additionally, `TestStreamScheduler_sendReminders` was failing due to a mismatch between implementation (batch query) and test expectation (single query), which had been ignored.
**Resolution/Improvement:** Consolidated setup logic into reusable actions to reduce maintenance burden and enforce consistent caching. Fixed the test expectation to match the N+1 optimization in the code.
**Lesson:** Always verify that tests align with performance optimizations (like N+1 fixes). CI refactoring can expose underlying test rot.

## 2026-02-13 - Migration Workflow Hardening
**Incident/Change:** Added `pull_request` trigger to `goose-migrate.yml` and pinned Postgres to v15.
**Analysis:** The migration validation workflow was only running on push to main/develop, leaving a gap where broken migrations could be merged via PRs. Additionally, the workflow used Postgres 16 while other tests and dev env use 15, risking version-specific incompatibilities.
**Resolution/Improvement:** Enabled PR validation for migrations and aligned Postgres version to 15-alpine.
**Lesson:** Critical validation workflows (like DB migrations) must always run on PRs to prevent broken states from reaching shared branches. Environment consistency across CI jobs prevents "works in X but not Y" issues.

## 2026-02-14 - Optimized CI Integration Tests
**Incident/Change:** Modified `.github/workflows/test.yml` to only start `postgres-ci`, `redis-ci`, and `ipfs-ci` for integration tests, excluding `clamav-ci`.
**Analysis:** The `clamav-ci` service has a long startup period (180s) which was delaying the integration test job. Since the general integration tests (via `internal/security/virus_scanner_test.go`) are designed to skip virus scanning if ClamAV is unavailable, waiting for it was unnecessary overhead. Dedicated virus scanning tests remain in `virus-scanner-tests.yml`.
**Resolution/Improvement:** Explicitly specified required services in `docker compose up` command within `test.yml`.
**Lesson:** Review service dependencies for CI jobs; lazy-loading or excluding non-critical heavy services can significantly improve feedback loops.

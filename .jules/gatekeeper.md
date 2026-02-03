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

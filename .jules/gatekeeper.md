## 2026-01-24 - CI Workflow Refactoring & Test Fix
**Incident/Change:** Refactored `test.yml` to use composite actions (`setup-go-cached`) and fixed a broken scheduler test.
**Analysis:** The `test.yml` workflow had significant duplication of setup steps across jobs. Additionally, `TestStreamScheduler_sendReminders` was failing due to a mismatch between implementation (batch query) and test expectation (single query), which had been ignored.
**Resolution/Improvement:** Consolidated setup logic into reusable actions to reduce maintenance burden and enforce consistent caching. Fixed the test expectation to match the N+1 optimization in the code.
**Lesson:** Always verify that tests align with performance optimizations (like N+1 fixes). CI refactoring can expose underlying test rot.

## 2026-01-25 - CI Integration Tests Fix
**Incident/Change:** The CI pipeline was reporting green, but integration tests were silently skipping due to incorrect flags in `Makefile`.
**Analysis:** The `test-integration-ci` target included the `-short` flag, which caused `SetupTestDB` to skip tests. Additionally, the `-tags=integration` flag was missing, causing `tests/integration` package to be ignored.
**Resolution/Improvement:** Updated `Makefile` to remove `-short` and add `-tags=integration` for CI integration test targets.
**Lesson:** A green pipeline is not enough; verify that tests are actually running. Flags like `-short` in CI can hide entire test suites.

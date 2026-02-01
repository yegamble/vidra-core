## 2026-01-24 - CI Workflow Refactoring & Test Fix
**Incident/Change:** Refactored `test.yml` to use composite actions (`setup-go-cached`) and fixed a broken scheduler test.
**Analysis:** The `test.yml` workflow had significant duplication of setup steps across jobs. Additionally, `TestStreamScheduler_sendReminders` was failing due to a mismatch between implementation (batch query) and test expectation (single query), which had been ignored.
**Resolution/Improvement:** Consolidated setup logic into reusable actions to reduce maintenance burden and enforce consistent caching. Fixed the test expectation to match the N+1 optimization in the code.
**Lesson:** Always verify that tests align with performance optimizations (like N+1 fixes). CI refactoring can expose underlying test rot.

## 2026-02-01 - Optimization of Integration Tests Workflow
**Incident/Change:** Removed `clamav-ci` from the general integration test suite in `test.yml` and explicitly defined only required services (`postgres-ci`, `redis-ci`, `ipfs-ci`).
**Analysis:** The `clamav-ci` service has a long startup time (~3 minutes) due to signature database loading. General integration tests do not require virus scanning, which is covered by the dedicated `virus-scanner-tests.yml` workflow. Waiting for ClamAV in every CI run was unnecessary overhead.
**Resolution/Improvement:** Updated `test.yml` to only start necessary services for integration tests. This avoids the 3-minute wait time for unrelated PRs.
**Lesson:** CI workflows should be optimized to run only what is strictly necessary. Dedicated heavy tests (like virus scanning) should be kept separate from the main feedback loop.

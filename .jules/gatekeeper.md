## 2026-01-24 - CI Workflow Refactoring & Test Fix
**Incident/Change:** Refactored `test.yml` to use composite actions (`setup-go-cached`) and fixed a broken scheduler test.
**Analysis:** The `test.yml` workflow had significant duplication of setup steps across jobs. Additionally, `TestStreamScheduler_sendReminders` was failing due to a mismatch between implementation (batch query) and test expectation (single query), which had been ignored.
**Resolution/Improvement:** Consolidated setup logic into reusable actions to reduce maintenance burden and enforce consistent caching. Fixed the test expectation to match the N+1 optimization in the code.
**Lesson:** Always verify that tests align with performance optimizations (like N+1 fixes). CI refactoring can expose underlying test rot.

## 2026-01-24 - CI Workflow Refactoring & Test Fix
**Incident/Change:** Refactored `test.yml` to use composite actions (`setup-go-cached`) and fixed a broken scheduler test.
**Analysis:** The `test.yml` workflow had significant duplication of setup steps across jobs. Additionally, `TestStreamScheduler_sendReminders` was failing due to a mismatch between implementation (batch query) and test expectation (single query), which had been ignored.
**Resolution/Improvement:** Consolidated setup logic into reusable actions to reduce maintenance burden and enforce consistent caching. Fixed the test expectation to match the N+1 optimization in the code.
**Lesson:** Always verify that tests align with performance optimizations (like N+1 fixes). CI refactoring can expose underlying test rot.

## 2026-01-28 - [Optimization: Docker Compose Wait]
**Incident/Change:** Replaced manual bash loops for service readiness checks with `docker compose up -d --wait` in `test.yml` and `virus-scanner-tests.yml`.
**Analysis:** CI workflows were using fragile and verbose `timeout ... bash -c ...` loops to wait for services (Postgres, Redis, ClamAV) to be ready. This duplicated healthcheck logic already defined in `docker-compose.ci.yml`.
**Resolution/Improvement:** Simplified workflows by leveraging the native `--wait` flag in Docker Compose v2, which respects the defined `healthcheck` configurations. This reduces YAML lines and improves reliability by using Docker's internal state handling.
**Lesson:** Trust the tool's native capabilities. When `docker-compose.yml` has `healthcheck`s, use them instead of reinventing the wheel in CI scripts.

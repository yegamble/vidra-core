## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]
**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2026-01-26 - [Insight: Route-Specific Middleware Verification]
**Scenario:** Critical auth endpoints (`/auth/login`, `/auth/register`) had strict rate limits defined in `routes.go`, but no tests verified these specific limits were active.
**Analysis:** Unit tests for `RateLimiter` middleware only proved the logic worked in isolation. There was no guarantee that the middleware was correctly applied to the routes or that the specific burst limits (10/min, 5/min) were enforced.
**Resolution:** Created `internal/httpapi/routes_security_test.go` to simulate attacks on these endpoints and verify 429 responses at the exact configured thresholds.
**Lesson:** Middleware logic tests are not enough. Security controls applied at the route level (wiring) require integration tests to prevent accidental regression (e.g. removing the middleware wrapper).

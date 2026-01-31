## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]
**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2026-05-24 - [Fix: Incorrect Error Type Assertion in Admin Handlers]
**Scenario:** While writing unit tests for `GetInstanceConfig`, the test `TestGetInstanceConfig_NotFound` failed with 500 Internal Server Error instead of the expected 404 Not Found, despite the repository returning a "NOT_FOUND" domain error.
**Analysis:** The handler code was asserting the error type as a pointer `err.(*domain.DomainError)`. However, `domain.NewDomainError` returns a `domain.DomainError` struct (value type). As a result, the type assertion failed, causing the handler to treat the error as an unexpected internal error.
**Resolution:** Updated `internal/httpapi/handlers/admin/instance.go` to assert the error type as a value `err.(domain.DomainError)`. This allowed the handler to correctly identify the error code and return 404.
**Lesson:** Always verify whether custom error constructors return values or pointers. When testing error handling logic, ensure tests cover specific error types to catch type assertion bugs that compiler checks might miss (since both implement the `error` interface).

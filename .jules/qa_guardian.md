## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]
**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2026-05-23 - [Insight: UpdateVideoHandler Partial Category Support]
**Scenario:** While adding unit tests for `UpdateVideoHandler`, discovered that the handler ignores the `category` string field in the update request body, despite the frontend potentially sending it. It only respects `category_id`.
**Analysis:** The handler defines a custom struct to decode the request, reading both `category` and `category_id`. However, when mapping to the domain object, only `category_id` is used. The `category` string is only used to populate the response for backward compatibility.
**Resolution:** Added `TestUpdateVideoHandler/Category_String_Ignored` to explicitly document and verify this behavior. This ensures that if this behavior changes (e.g., to support lookup by slug), we have a baseline.
**Lesson:** When handlers use custom intermediate structs for decoding, verify that all decoded fields are actually propagated to the domain logic or document why they are ignored.

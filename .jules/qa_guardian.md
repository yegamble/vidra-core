## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]
**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2026-05-25 - [Edge Case: Video Category Slug Ignored in Updates]
**Scenario:** While adding unit tests for `UpdateVideoHandler`, discovered that passing a category slug (e.g., "music") in the JSON payload is ignored, resulting in the category being cleared or unchanged if `category_id` is missing.
**Analysis:** The handler parses the JSON into a struct that has both `Category` (string) and `CategoryID` (*UUID), but when converting to the domain model, only `CategoryID` is copied. The comment `// Accept category slug` suggests this is a missing feature or bug.
**Resolution:** Added a specific test case `Success (with Caveat): Category Slug Ignored` to document and assert this current behavior. This prevents accidental changes until the feature is properly implemented (likely requiring `CategoryRepository` injection).
**Lesson:** When testing handlers that accept multiple formats for a field (ID vs Slug), verify both paths explicitly. Comments in code can be misleading about implemented features.

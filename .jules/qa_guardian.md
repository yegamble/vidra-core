## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]

**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2025-11-17 - [Fix: Deadlocks and Flakiness in DB Pool Tests]

**Scenario:** `TestPool_StatsUnderLoad` timed out due to a deadlock, and `TestNewPool_Success`/`TestPool_Stats` failed intermittently with `InUse` count mismatches.
**Analysis:** `TestPool_StatsUnderLoad` launched concurrent queries without timeouts, leading to deadlocks when `sqlmock`'s strict ordering or connection limits were hit. `TestNewPool_Success` failed because the connection used for `Ping` wasn't immediately returned to idle state or stats weren't updated instantly in the mock environment.
**Resolution:** Refactored `TestPool_StatsUnderLoad` to use `context.WithTimeout` and disabled strict order matching. Added retry loops for stats checks in `TestNewPool_Success` and others to handle asynchronous state updates.
**Lesson:** When testing connection pools with `sqlmock` and concurrency, always use timeouts for queries to prevent deadlocks and allow for eventual consistency in stats checks, as mock behavior might not be instantaneous.

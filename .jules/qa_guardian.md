## 2026-01-24 - [Fix: SQL Mock Mismatch in Scheduler Test]
**Scenario:** `TestStreamScheduler_sendReminders` failed during `make test-unit` because the mocked SQL query expectation didn't match the actual implementation.
**Analysis:** The implementation of `sendReminders` was refactored to use `getChannelSubscribersForChannels` (batch fetch) to avoid N+1 queries, changing the SQL from `SELECT subscriber_id ...` to `SELECT channel_id, subscriber_id ... WHERE channel_id IN ...`. The test was not updated to reflect this optimization.
**Resolution:** Updated `internal/livestream/scheduler_test.go` to mock the correct SQL query and return rows including `channel_id`.
**Lesson:** When refactoring database access patterns (like N+1 fixes), always grep for `sqlmock` expectations in tests to ensure they are updated to match the new query structure.

## 2026-01-30 - [Bug: Unsupported Map Type in SQL Driver]
**Scenario:** While writing new unit tests for `PluginHandler` and `PluginRepository`, `EnablePlugin` and `UpdateConfig` tests failed with `sql: converting argument $5 type: unsupported type map[string]interface {}, a map`.
**Analysis:** The `PluginRepository` was passing `plugin.Config` (a `map[string]any`) directly to `db.ExecContext`. The `lib/pq` driver (and standard `database/sql`) does not support passing maps directly for JSON columns; they must be marshaled to `[]byte` or `string` first. This bug was latent because there were no existing unit tests covering the `Update` paths of `PluginRepository`.
**Resolution:** Modified `internal/repository/plugin_repository.go` to marshal the configuration map to JSON before passing it to the database query in `Create`, `Update`, and `UpdateConfig` methods.
**Lesson:** Always include unit tests for repository `Update` methods, especially when dealing with complex types like JSON maps, to catch driver incompatibility issues early. `go-sqlmock` tests effectively catch these type conversion errors.

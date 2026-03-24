# PostgreSQL Docker Assets

PostgreSQL bootstrap assets live here so database-specific Docker files do not clutter the repository root.

## Contents

- [`init/init-db.sql`](./init/init-db.sql) - Example bootstrap SQL for local and production-style Docker Postgres setups
- [`init/init-test-db.sql`](./init/init-test-db.sql) - Test bootstrap SQL used by documented E2E and smoke-test flows

Keep future Postgres container assets in this directory rather than adding new root-level SQL files.

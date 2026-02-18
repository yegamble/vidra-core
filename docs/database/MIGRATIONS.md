# Database Migrations with Goose

## Overview

Athena uses [Goose](https://github.com/pressly/goose) for database migration management. Goose is a robust, lightweight migration tool that supports both SQL and Go migrations.

- **Simple & Reliable**: No external dependencies or cloud accounts required.
- **SQL-First**: Write standard SQL for your migrations.
- **Versioning**: Sequential versioning ensures migrations run in the correct order.
- **Environment Aware**: Easily target development, test, or production environments.

## Installation

### Using Go (Recommended)

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

### Verify Installation

```bash
goose -version
```

## Creating Migrations

### Create a New SQL Migration

To create a new migration file in the `migrations/` directory:

```bash
make migrate-create NAME=add_user_notifications
```

This will generate a file like `migrations/20240101120000_add_user_notifications.sql`.

### Migration File Format

Goose migrations use a specific format with annotations for Up and Down steps:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE notifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE notifications;
-- +goose StatementEnd
```

> **Note:** The `-- +goose StatementBegin` and `-- +goose StatementEnd` annotations are crucial for handling complex SQL statements like function definitions or triggers.

## Applying Migrations

### Development Environment

To apply all pending migrations to your local development database (defined in `.env`):

```bash
make migrate-up
```

### Test Environment

To apply migrations to the test database:

```bash
make migrate-test
```

### Production Environment

For production, you can run `goose` directly against your production database URL:

```bash
export DATABASE_URL="postgres://user:pass@prod-host:5432/athena?sslmode=require"
goose -dir migrations postgres "$DATABASE_URL" up
```

### Rollback (Down)

To roll back the last applied migration:

```bash
make migrate-down
```

### Reset Database

To roll back all migrations and start fresh (Warning: Destructive!):

```bash
make migrate-reset
```

## Migration Status

To check which migrations have been applied:

```bash
make migrate-status
```

Example output:

```
$ make migrate-status
    Applied At                  Migration
    =======================================
    Tue Jan 01 12:00:00 2024 -- 20240101120000_initial_schema.sql
    Tue Jan 01 12:05:00 2024 -- 20240101120500_add_users.sql
    Pending                  -- 20240102100000_add_notifications.sql
```

## Troubleshooting

### Migration Lock

**Error:** `database is locked` or similar lock errors.

**Cause:** A previous migration might have failed or been interrupted, leaving a lock in the `goose_db_version` table (or similar lock mechanism).

**Solution:**
Ensure no other migration process is running. If stuck, you may need to manually check the database lock status or restart the database service if it's a local dev instance.

### Version Mismatch

**Problem:** You pulled new code but the database is missing tables.

**Solution:**
Run `make migrate-up` to apply the new migrations from the codebase.

### Dirty Database State

**Problem:** Development database is in an inconsistent state.

**Solution:**
Reset the database (Data will be lost!):

```bash
make migrate-reset
```

## Best Practices

1. **Always Provide Down Migrations**: Ensure every `+goose Up` has a corresponding `+goose Down` to allow rollbacks.
2. **Test Locally**: Always run `make migrate-up` and `make migrate-down` locally before pushing to ensure your SQL is correct.
3. **Don't Modify Applied Migrations**: Once a migration is merged and applied to production, never edit it. Create a new migration to fix or change things.
4. **Use Transactions**: Goose runs migrations in a transaction by default. Avoid `COMMIT` or `ROLLBACK` inside your migration unless you explicitly disable transactions (not recommended).
5. **Review Schema Changes**: For high-traffic tables, consider the impact of locking (e.g., `CREATE INDEX CONCURRENTLY` cannot run inside a transaction blocks, so it might need special handling or be run separately).

## Resources

- [Goose Documentation](https://github.com/pressly/goose)
- [PostgreSQL Documentation](https://www.postgresql.org/docs/)

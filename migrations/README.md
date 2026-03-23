# Database Migrations

This directory contains SQL migration files managed by [Goose](https://github.com/pressly/goose). See also `migrations/CLAUDE.md` for detailed patterns.

## Structure

```
migrations/
├── 001_initial_schema.sql      # Initial database setup
├── 002_add_users.sql           # User management
├── ...                         # 83 total migration files (001-085)
└── README.md                   # This file
```

## File Naming Convention

```
{version}_{description}.sql
```

Examples:

- `001_initial_schema.sql`
- `002_add_users_table.sql`
- `20240315120000_add_video_analytics.sql`

## Creating Migrations

### Quick Commands

```bash
# Create new migration
make migrate-create NAME=add_feature
# Or directly:
goose -dir migrations create add_feature sql
```

### Example Migration

```sql
-- +goose Up
CREATE TABLE user_preferences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notifications_enabled BOOLEAN DEFAULT TRUE,
    email_frequency VARCHAR(20) DEFAULT 'daily',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_preferences UNIQUE(user_id)
);

CREATE INDEX idx_user_preferences_user_id
ON user_preferences(user_id);

COMMENT ON TABLE user_preferences IS 'User notification and app preferences';
COMMENT ON COLUMN user_preferences.email_frequency IS 'Frequency of email notifications: immediate, daily, weekly, never';

-- +goose Down
DROP TABLE IF EXISTS user_preferences;
```

## Migration Guidelines

### ✅ Do

- **Test locally first** - Always test in dev environment
- **Use transactions** - Wrap DDL in BEGIN/COMMIT where supported
- **Add comments** - Document purpose and impact
- **Use indexes wisely** - Use `CONCURRENTLY` for large tables
- **Lint before commit** - Run `make lint`

### ❌ Don't

- **Never edit applied migrations** - Create new migration instead
- **Don't skip validation** - CI will fail anyway
- **Avoid destructive changes** - DROP operations require review
- **Don't commit broken SQL** - Test first
- **Never delete migrations** - Create rollback migration instead

## Best Practices

### For Large Tables

Use concurrent operations to avoid locks:

```sql
-- Good - Non-blocking
CREATE INDEX CONCURRENTLY idx_videos_created
ON videos(created_at);

-- Bad - Locks table
CREATE INDEX idx_videos_created
ON videos(created_at);
```

### For Data Migrations

Process in batches:

```sql
-- Batch update to avoid long locks
DO $$
DECLARE
    batch_size INT := 1000;
    rows_affected INT;
BEGIN
    LOOP
        UPDATE users
        SET legacy_field = new_field
        WHERE legacy_field IS NULL
        LIMIT batch_size;

        GET DIAGNOSTICS rows_affected = ROW_COUNT;
        EXIT WHEN rows_affected = 0;

        COMMIT;
        RAISE NOTICE 'Processed % rows', rows_affected;
    END LOOP;
END $$;
```

### For Destructive Changes

Document why it's safe:

```sql
-- DESTRUCTIVE CHANGE: Dropping deprecated_field
-- Justification: Field unused since v2.0 (deployed 2024-01-15)
-- Verified: No references in codebase (grep conducted 2024-03-15)
-- Backup: Production backup taken before deployment
ALTER TABLE users DROP COLUMN deprecated_field;
```

## Goose Integration

### Commands

```bash
make migrate-up        # Apply all pending migrations
make migrate-down      # Rollback last migration
make migrate-status    # Show migration status
make migrate-create NAME=add_feature  # Create new migration
```

### Migration Format

All migrations use `-- +goose Up` and `-- +goose Down` markers. Goose handles versioning via the `goose_db_version` table.

## Workflow

### Development

```bash
# 1. Create migration
make migrate-create NAME=my_feature

# 2. Edit migration file
vim migrations/NNN_my_feature.sql

# 3. Test locally
make migrate-up

# 4. Verify
make migrate-status

# 5. Commit
git add migrations/
git commit -m "Add migration for my_feature"
```

### CI/CD

GitHub Actions automatically:

1. Lints new migrations
2. Validates integrity
3. Test-applies to CI database
4. Comments on PR with results

See `.github/workflows/` for CI configuration

## Common Issues

### Migration Failed to Apply

**Error:** `SQL execution failed`

**Solution:**

1. Check SQL syntax
2. Verify dependencies (tables, columns exist)
3. Test in fresh database
4. Review error message carefully

## PostgreSQL Extensions

Vidra Core uses these extensions (enabled in migrations):

- `uuid-ossp` - UUID generation
- `pg_trgm` - Trigram text search
- `unaccent` - Accent-insensitive search
- `btree_gin` - GIN indexes for composite queries

Example:

```sql
-- Enable extensions (idempotent)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS btree_gin;
```

## Resources

- [Goose Documentation](https://github.com/pressly/goose) - Official docs
- [Goose Migration Guide](migrations/CLAUDE.md) - Project patterns
- [PostgreSQL Docs](https://www.postgresql.org/docs/) - SQL reference

## Getting Help

```bash
# Show all Makefile commands
make help

# Check migration status
make migrate-status
```

**Questions?** Check `#engineering` or refer to [migrations/CLAUDE.md](migrations/CLAUDE.md)

---

**Last Updated:** 2026-03-23

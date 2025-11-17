# Database Migrations

This directory contains SQL migration files managed by [Atlas](https://atlasgo.io/).

## Structure

```
migrations/
├── atlas.sum                    # Migration integrity checksums (auto-generated)
├── 001_initial_schema.sql      # Initial database setup
├── 002_add_users.sql           # User management
├── ...                         # Subsequent migrations
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
# Schema-based (recommended)
make migrate-diff NAME=add_feature

# Manual creation
make atlas-migrate-new NAME=add_custom_index

# Legacy (backward compatible)
# Create file manually following naming convention
```

### Example Migration

```sql
-- Migration: add_user_preferences
-- Description: Add user preferences table for notification settings
-- Author: engineering@example.com
-- Date: 2024-03-15

-- Create table
CREATE TABLE user_preferences (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notifications_enabled BOOLEAN DEFAULT TRUE,
    email_frequency VARCHAR(20) DEFAULT 'daily',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_preferences UNIQUE(user_id)
);

-- Create indexes
CREATE INDEX idx_user_preferences_user_id
ON user_preferences(user_id);

-- Add comments
COMMENT ON TABLE user_preferences IS 'User notification and app preferences';
COMMENT ON COLUMN user_preferences.email_frequency IS 'Frequency of email notifications: immediate, daily, weekly, never';
```

## Migration Guidelines

### ✅ Do

- **Test locally first** - Always test in dev environment
- **Use transactions** - Wrap DDL in BEGIN/COMMIT where supported
- **Add comments** - Document purpose and impact
- **Use indexes wisely** - Use `CONCURRENTLY` for large tables
- **Lint before commit** - Run `make atlas-migrate-lint`
- **Commit atlas.sum** - Always commit the checksum file

### ❌ Don't

- **Never edit applied migrations** - Create new migration instead
- **Don't skip linting** - CI will fail anyway
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

## Atlas Integration

### atlas.sum File

**Important:** Always commit `atlas.sum` with your migration files.

This file contains:
- SHA256 checksums of each migration
- Integrity verification data
- Atlas migration metadata

If you get checksum errors:
```bash
make atlas-migrate-hash
git add migrations/atlas.sum
```

### Migration Verification

Atlas automatically verifies:
- File integrity (checksums match)
- No modifications to applied migrations
- SQL syntax validity
- Destructive operation detection

## Workflow

### Development

```bash
# 1. Create migration
make atlas-migrate-new NAME=my_feature

# 2. Edit migration file
vim migrations/XXXXXX_my_feature.sql

# 3. Rehash (updates atlas.sum)
make atlas-migrate-hash

# 4. Test locally
make atlas-migrate-apply ENV=dev

# 5. Lint
make atlas-migrate-lint

# 6. Commit
git add migrations/
git commit -m "Add migration for my_feature"
```

### CI/CD

GitHub Actions automatically:
1. Lints new migrations
2. Validates integrity
3. Test-applies to CI database
4. Comments on PR with results

See `.github/workflows/atlas-lint.yml`

## Common Issues

### Hash Mismatch

**Error:** `migration hash mismatch`

**Solution:**
```bash
make atlas-migrate-hash
```

### Destructive Change Detected

**Error:** `destructive change blocked`

**Solution:** Review and document why it's safe, or remove the change

### Migration Failed to Apply

**Error:** `SQL execution failed`

**Solution:**
1. Check SQL syntax
2. Verify dependencies (tables, columns exist)
3. Test in fresh database
4. Review error message carefully

## PostgreSQL Extensions

Athena uses these extensions (enabled in migrations):

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

- [Atlas Quick Start](/docs/database/ATLAS_QUICKSTART.md) - 5-minute setup
- [Full Migration Guide](/docs/database/MIGRATIONS.md) - Comprehensive docs
- [Atlas Documentation](https://atlasgo.io/docs) - Official docs
- [PostgreSQL Docs](https://www.postgresql.org/docs/) - SQL reference

## Getting Help

```bash
# Show Atlas commands
make atlas-help

# Show all Makefile commands
make help

# Check migration status
make atlas-migrate-status
```

**Questions?** Check `#engineering` or refer to [MIGRATIONS.md](/docs/database/MIGRATIONS.md)

---

**Last Updated:** 2025-11-17

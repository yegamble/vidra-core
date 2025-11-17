# Database Migrations with Atlas

## Overview

Athena uses [Atlas](https://atlasgo.io/) for professional database migration management. Atlas provides:

- **Declarative schema management** - Define desired state, Atlas generates migrations
- **Migration diff generation** - Automatic SQL generation from schema changes
- **Shadow database testing** - Validate migrations before applying to production
- **Destructive operation prevention** - Block dangerous changes in production
- **CI/CD integration** - Automated migration validation in pull requests
- **Migration linting** - Detect issues before deployment

## Installation

### Using Makefile (Recommended)

```bash
make atlas-install
```

### Manual Installation

```bash
curl -sSf https://atlasgo.sh | sh
```

### Verify Installation

```bash
make atlas-version
# or
atlas version
```

## Creating Migrations

### Option 1: Schema-Based Diff (Recommended)

This is the preferred method for complex schema changes.

1. **Edit your desired schema** in `schema.sql` (create if it doesn't exist)
2. **Generate migration** from the diff:
   ```bash
   make migrate-diff NAME=add_user_notifications
   ```

Example workflow:
```bash
# 1. Inspect current schema to create baseline
make atlas-schema-inspect-file OUTPUT=schema.sql

# 2. Edit schema.sql with your changes
# Add new table, column, index, etc.

# 3. Generate migration
make migrate-diff NAME=add_notification_preferences

# 4. Review generated migration in migrations/ directory
# 5. Test the migration
make atlas-migrate-apply ENV=dev
```

### Option 2: Manual SQL Migration

For simple or custom changes:

1. **Create new migration file**:
   ```bash
   make atlas-migrate-new NAME=add_custom_index
   ```

2. **Edit the generated file** in `migrations/` directory
3. **Add your SQL**:
   ```sql
   -- Add custom index
   CREATE INDEX CONCURRENTLY idx_videos_user_created
   ON videos(user_id, created_at DESC);
   ```

4. **Rehash migrations** to update integrity checksums:
   ```bash
   make atlas-migrate-hash
   ```

### Option 3: Legacy Shell Script (Backward Compatibility)

```bash
# Still supported for existing workflows
make migrate-dev
make migrate-test
```

## Applying Migrations

### Development Environment

```bash
# Using Atlas
make atlas-migrate-apply ENV=dev

# Using legacy script
make migrate-dev
```

### Test Environment

```bash
make atlas-migrate-apply ENV=test
```

### Production Environment

```bash
# Atlas requires approval for production
make atlas-migrate-apply ENV=prod

# You'll be prompted to review and approve changes
```

## Migration Checks

### Lint Before Commit

Always lint migrations before creating a pull request:

```bash
make atlas-migrate-lint
```

This checks for:
- Destructive changes (DROP TABLE, DROP COLUMN, etc.)
- Data-dependent changes
- Incompatible schema modifications
- Missing reversibility

### Validate Migration Integrity

Ensure migration files haven't been tampered with:

```bash
make atlas-migrate-validate ENV=dev
```

### Check Migration Status

See which migrations are applied:

```bash
make atlas-migrate-status ENV=dev
```

Example output:
```
Migration Status: OK
  Current Version: 20240315120000
  Next Version:    20240316093000
  Executed:        42 migrations
  Pending:         1 migration
```

## Inspecting Database Schema

### View Current Schema

```bash
make atlas-schema-inspect ENV=dev
```

### Save Schema to File

```bash
make atlas-schema-inspect-file OUTPUT=schema_backup.sql
```

### Compare Schemas

```bash
# Inspect production
make atlas-schema-inspect ENV=prod > schema_prod.sql

# Inspect dev
make atlas-schema-inspect ENV=dev > schema_dev.sql

# Compare
diff schema_prod.sql schema_dev.sql
```

## Migration Best Practices

### 1. Always Lint Before Commit

```bash
make atlas-migrate-lint
```

- Run locally before pushing
- CI will fail if lint errors exist
- Fix issues before creating PR

### 2. Never Modify Applied Migrations

**Don't:**
```bash
# WRONG - Editing already-applied migration
vim migrations/20240101120000_initial_schema.sql
```

**Do:**
```bash
# RIGHT - Create new migration to fix issues
make atlas-migrate-new NAME=fix_initial_schema_issue
```

Why? Applied migrations are tracked by hash. Modifying them breaks integrity checks.

### 3. Test in Dev Environment First

```bash
# 1. Apply to dev
make atlas-migrate-apply ENV=dev

# 2. Verify application works
make dev

# 3. Run tests
make test

# 4. Only then apply to production
make atlas-migrate-apply ENV=prod
```

### 4. Review Atlas Plan Before Production

Atlas shows you exactly what will change:

```bash
make atlas-migrate-apply ENV=prod
```

Output:
```sql
-- Planned Changes:
-- Create "notifications" table
CREATE TABLE notifications (
  id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  ...
);

-- Create index
CREATE INDEX idx_notifications_user_unread
ON notifications(user_id, read);

Apply? [y/N]:
```

Review carefully, then confirm.

### 5. Backup Before Production Migrations

```bash
# Export schema before changes
make atlas-schema-inspect-file ENV=prod OUTPUT=schema_before_migration.sql

# Create database backup
pg_dump $DATABASE_URL > backup_$(date +%Y%m%d_%H%M%S).sql

# Then apply migration
make atlas-migrate-apply ENV=prod
```

### 6. Use Concurrent Index Creation

For large tables, use `CONCURRENTLY` to avoid locking:

```sql
-- Good - Non-blocking
CREATE INDEX CONCURRENTLY idx_videos_created
ON videos(created_at);

-- Bad - Locks table during creation
CREATE INDEX idx_videos_created
ON videos(created_at);
```

### 7. Make Migrations Reversible

When possible, provide down migrations:

```sql
-- Up migration
ALTER TABLE users ADD COLUMN preferences JSONB DEFAULT '{}'::jsonb;

-- Down migration (in separate file or comment)
-- ALTER TABLE users DROP COLUMN preferences;
```

### 8. Handle Data Migrations Carefully

For data transformations:

```sql
-- 1. Add new column
ALTER TABLE users ADD COLUMN email_verified BOOLEAN DEFAULT FALSE;

-- 2. Migrate data (in batches for large tables)
UPDATE users
SET email_verified = TRUE
WHERE verification_token IS NULL
LIMIT 1000;

-- 3. Add constraint after data migration
ALTER TABLE users ALTER COLUMN email_verified SET NOT NULL;
```

For large tables, consider:
- Batching updates
- Running during low-traffic periods
- Monitoring query performance

### 9. Version Control Everything

```bash
git add migrations/
git add atlas.hcl
git add schema.sql  # if using schema-based workflow
git commit -m "Add user notification preferences migration"
```

### 10. Document Complex Migrations

Add comments to explain non-obvious changes:

```sql
-- Migration: add_video_analytics_partitioning
-- Purpose: Partition video_views table by date for better query performance
-- Impact: ~500M rows, expect 10-15 minutes for production
-- Rollback: Run down migration to merge partitions back to single table

CREATE TABLE video_views_partitioned (
  LIKE video_views INCLUDING ALL
) PARTITION BY RANGE (created_at);

-- Create monthly partitions for past year
-- ...
```

## Troubleshooting

### Shadow Database Connection Failed

**Error:**
```
Error: failed to connect to shadow database
```

**Solution:**
Ensure shadow database exists and is accessible:

```bash
# For local development
createdb athena_shadow

# For Docker
docker exec -it athena-postgres psql -U athena_user -c "CREATE DATABASE athena_shadow;"

# Verify connection
psql "postgres://athena_user:athena_password@localhost:5433/athena_shadow"
```

### Lint Errors: Destructive Changes

**Error:**
```
Error: destructive changes detected:
  - DROP COLUMN users.deprecated_field
```

**Solutions:**

1. **If intentional** (you really want to drop the column):
   ```bash
   # Add justification comment in migration file
   -- DESTRUCTIVE: Dropping deprecated_field (unused since v2.0)
   ALTER TABLE users DROP COLUMN deprecated_field;

   # Apply with force (dev only)
   atlas migrate apply --env dev --force
   ```

2. **If unintentional** (you want to keep the column):
   - Remove the DROP statement
   - Rehash migrations: `make atlas-migrate-hash`

### Migration Conflicts

**Error:**
```
Error: migration 20240315120000 has been modified
```

**Cause:** Someone edited an already-applied migration.

**Solution:**
```bash
# 1. Revert the modified migration to original state
git checkout migrations/20240315120000_*.sql

# 2. Create new migration with the intended changes
make atlas-migrate-new NAME=fix_issue_from_20240315

# 3. Add your changes to the new migration

# 4. Rehash
make atlas-migrate-hash
```

### Hash Mismatch

**Error:**
```
Error: migration directory integrity check failed
```

**Solution:**
```bash
# Rehash migration directory
make atlas-migrate-hash

# This updates atlas.sum with correct checksums
git add migrations/atlas.sum
git commit -m "Update migration checksums"
```

### CI Validation Failing

**Error in GitHub Actions:**
```
Atlas migration lint failed
```

**Debug locally:**
```bash
# Run the same checks CI runs
make atlas-migrate-lint
make atlas-migrate-validate

# Fix issues, then test again
make atlas-migrate-lint
```

### Production Apply Hangs

**Symptom:** Migration stuck, no progress

**Likely cause:** Lock contention on table

**Solution:**
```sql
-- Check for locks (in separate psql session)
SELECT
  pid,
  usename,
  application_name,
  state,
  query
FROM pg_stat_activity
WHERE datname = 'athena'
  AND state != 'idle';

-- If safe, kill blocking query
SELECT pg_terminate_backend(pid);
```

**Prevention:**
- Run migrations during maintenance windows
- Use `CONCURRENTLY` for index creation
- Avoid long-running data migrations
- Test on production-like dataset first

## CI/CD Integration

### GitHub Actions Workflow

Migrations are automatically validated on every pull request. See `.github/workflows/atlas-lint.yml`.

The workflow:
1. Installs Atlas CLI
2. Sets up PostgreSQL with shadow database
3. Lints new migrations
4. Validates migration integrity
5. Test-applies migrations
6. Comments on PR with results

### Pull Request Process

1. **Create migration** locally:
   ```bash
   make migrate-diff NAME=add_feature
   ```

2. **Lint locally**:
   ```bash
   make atlas-migrate-lint
   ```

3. **Commit and push**:
   ```bash
   git add migrations/
   git commit -m "Add migration for feature X"
   git push
   ```

4. **CI validates** automatically

5. **Review PR** - check CI output and Atlas comments

6. **Merge** when CI passes and approved

## Environment Variables

Configure Atlas via environment variables:

### Required

```bash
# Primary database URL
DATABASE_URL=postgres://user:pass@host:5432/athena?sslmode=disable

# Shadow database for validation (can be same host, different db)
SHADOW_DATABASE_URL=postgres://user:pass@host:5432/athena_shadow?sslmode=disable
```

### Optional

```bash
# Test database (for test environment)
TEST_DATABASE_URL=postgres://user:pass@localhost:5433/athena_test?sslmode=disable
```

### Example .env

```bash
# Development
DATABASE_URL=postgres://athena_user:athena_password@localhost:5432/athena?sslmode=disable
SHADOW_DATABASE_URL=postgres://athena_user:athena_password@localhost:5433/athena_shadow?sslmode=disable

# Production (use secrets management)
DATABASE_URL=postgres://prod_user:${DB_PASS}@prod-db.example.com:5432/athena?sslmode=require
SHADOW_DATABASE_URL=postgres://prod_user:${DB_PASS}@prod-db.example.com:5432/athena_shadow?sslmode=require
```

## Migration Workflow Cheat Sheet

### Daily Development

```bash
# 1. Pull latest migrations
git pull

# 2. Apply to local dev DB
make atlas-migrate-apply ENV=dev

# 3. Make schema changes
# Edit schema.sql or create manual migration

# 4. Generate migration
make migrate-diff NAME=my_feature

# 5. Test migration
make atlas-migrate-apply ENV=dev

# 6. Run application tests
make test

# 7. Lint before commit
make atlas-migrate-lint

# 8. Commit
git add migrations/
git commit -m "Add migration for my_feature"
git push
```

### Production Deployment

```bash
# 1. Backup production database
pg_dump $PROD_DATABASE_URL > backup_$(date +%Y%m%d).sql

# 2. Check pending migrations
make atlas-migrate-status ENV=prod

# 3. Review migration plan
make atlas-migrate-apply ENV=prod
# (review output, don't confirm yet)

# 4. Apply during maintenance window
make atlas-migrate-apply ENV=prod
# (review and confirm)

# 5. Verify application health
curl https://api.example.com/health

# 6. Monitor logs for errors
tail -f /var/log/athena/app.log
```

### Emergency Rollback

```bash
# Option 1: Use down migration (if available)
make atlas-migrate-down ENV=prod

# Option 2: Restore from backup
psql $DATABASE_URL < backup_20240315.sql

# Option 3: Manual rollback
psql $DATABASE_URL
# Run reverse SQL commands manually
```

## Advanced Topics

### Multi-Environment Workflows

```bash
# Dev -> Staging -> Production pipeline

# 1. Test in dev
make atlas-migrate-apply ENV=dev

# 2. Deploy to staging
make atlas-migrate-apply ENV=staging

# 3. Verify staging
curl https://staging-api.example.com/health

# 4. Deploy to production
make atlas-migrate-apply ENV=prod
```

### Schema Drift Detection

Detect when database schema doesn't match migration history:

```bash
# Inspect live database
make atlas-schema-inspect ENV=prod > schema_actual.sql

# Compare to expected schema
diff schema.sql schema_actual.sql
```

If drift detected:
1. Identify source of manual change
2. Create migration to codify the change
3. Document in migration comments

### Custom Lint Rules

Edit `atlas.hcl` to customize lint behavior:

```hcl
env "prod" {
  lint {
    # Block all destructive changes
    destructive {
      error = true
    }

    # Require concurrent index creation
    concurrent_index {
      error = true
    }

    # Warn on large table modifications
    data_depend {
      error = true
    }
  }
}
```

## Resources

- [Atlas Documentation](https://atlasgo.io/docs)
- [Atlas CLI Reference](https://atlasgo.io/cli-reference)
- [Atlas GitHub](https://github.com/ariga/atlas)
- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
- [Athena Database Schema](/docs/database/SCHEMA.md)

## Getting Help

### Command Reference

```bash
make atlas-help        # Show all Atlas commands
make help              # Show all Makefile commands
atlas --help           # Atlas CLI help
```

### Debugging

Enable verbose output:

```bash
atlas migrate apply --env dev --verbose
```

### Support Channels

- **Internal:** Check `#engineering` Slack channel
- **Atlas:** [Discord community](https://discord.gg/atlas)
- **Bugs:** [GitHub Issues](https://github.com/yourusername/athena/issues)

---

**Last Updated:** 2025-11-17
**Maintainer:** DevOps Team

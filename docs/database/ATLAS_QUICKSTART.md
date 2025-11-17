# Atlas Quick Start Guide

## 5-Minute Setup

### 1. Install Atlas

```bash
make atlas-install
```

### 2. Configure Environment

Add to your `.env` file:

```bash
DATABASE_URL=postgres://athena_user:athena_password@localhost:5432/athena?sslmode=disable
SHADOW_DATABASE_URL=postgres://athena_user:athena_password@localhost:5433/athena_shadow?sslmode=disable
```

### 3. Create Shadow Database

```bash
# If using Docker
docker exec -it athena-postgres createdb -U athena_user athena_shadow

# If using local PostgreSQL
createdb athena_shadow
```

### 4. Verify Installation

```bash
make atlas-version
make atlas-migrate-status ENV=dev
```

## Common Commands

### Daily Development

```bash
# Create new migration
make atlas-migrate-new NAME=add_my_feature

# Apply migrations
make atlas-migrate-apply ENV=dev

# Check status
make atlas-migrate-status ENV=dev

# Lint before committing
make atlas-migrate-lint
```

### Before Each PR

```bash
# Lint migrations
make atlas-migrate-lint

# Validate integrity
make atlas-migrate-validate ENV=dev

# Check status
make atlas-migrate-status ENV=dev
```

### Production Deployment

```bash
# 1. Check pending migrations
make atlas-migrate-status ENV=prod

# 2. Review migration plan
make atlas-migrate-apply ENV=prod
# (review output, don't confirm yet)

# 3. Apply (will prompt for confirmation)
make atlas-migrate-apply ENV=prod
```

## Environment Configuration

Atlas uses environment-specific rules defined in `atlas.hcl`:

| Environment | Auto-approve | Destructive Changes | Use Case |
|-------------|--------------|---------------------|----------|
| `dev`       | ✅ Yes       | ⚠️ Warn            | Local development |
| `test`      | ✅ Yes       | ⚠️ Warn            | Testing |
| `ci`        | ✅ Yes       | ❌ Block           | GitHub Actions |
| `prod`      | ❌ No        | ❌ Block           | Production |

## Migration Workflow

### Schema-Based (Recommended)

```bash
# 1. Create baseline schema
make atlas-schema-inspect-file OUTPUT=schema.sql

# 2. Edit schema.sql with your changes

# 3. Generate migration
make migrate-diff NAME=my_feature

# 4. Review generated SQL in migrations/

# 5. Test locally
make atlas-migrate-apply ENV=dev

# 6. Commit
git add migrations/ schema.sql
git commit -m "Add migration for my_feature"
```

### Manual SQL

```bash
# 1. Create new migration file
make atlas-migrate-new NAME=add_custom_logic

# 2. Edit generated file in migrations/

# 3. Add your SQL

# 4. Rehash
make atlas-migrate-hash

# 5. Test
make atlas-migrate-apply ENV=dev
```

## Troubleshooting

### "Shadow database connection failed"

```bash
# Create shadow database
createdb athena_shadow

# Or in Docker
docker exec -it athena-postgres createdb -U athena_user athena_shadow
```

### "Migration hash mismatch"

```bash
# Rehash migrations
make atlas-migrate-hash
git add migrations/atlas.sum
```

### "Destructive change detected"

This is intentional protection. Options:

1. **Remove the destructive change** (recommended)
2. **Use separate deployment** for destructive changes
3. **Force in dev only**: `atlas migrate apply --env dev --force`

## Best Practices

✅ **Do:**
- Lint before every commit
- Test in dev before pushing
- Use schema-based workflow for complex changes
- Review Atlas plan before production apply
- Backup production DB before migrations

❌ **Don't:**
- Edit already-applied migrations
- Skip linting
- Apply untested migrations to production
- Ignore destructive change warnings

## Getting Help

```bash
# Show all Atlas commands
make atlas-help

# Show all Makefile commands
make help

# Atlas CLI help
atlas --help
```

## Resources

- [Full Migration Guide](/docs/database/MIGRATIONS.md)
- [Atlas Documentation](https://atlasgo.io/docs)
- [Atlas CLI Reference](https://atlasgo.io/cli-reference)

---

**Need Help?** Check `#engineering` or refer to [MIGRATIONS.md](/docs/database/MIGRATIONS.md)

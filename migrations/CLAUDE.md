# Database Migrations - Claude Guidelines

## Overview

SQL migrations managed by Goose v3. All schema changes MUST go through migrations.

## Tool: Goose

- **Repository**: https://github.com/pressly/goose
- **Directory**: `/migrations/`
- **Format**: Sequential numbered SQL files

## Commands

```bash
# Create new migration
make migrate-create NAME=add_feature
# Or directly:
goose -dir migrations create add_feature sql

# Apply all pending migrations
make migrate-up
# Or: goose -dir migrations postgres "$DATABASE_URL" up

# Rollback last migration
make migrate-down
# Or: goose -dir migrations postgres "$DATABASE_URL" down

# Check current status
make migrate-status
# Or: goose -dir migrations postgres "$DATABASE_URL" status
```

## Migration File Format

```sql
-- +goose Up
-- SQL in this section is executed when the migration is applied.

CREATE TABLE videos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(200) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_videos_created_at ON videos(created_at);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.

DROP TABLE IF EXISTS videos;
```

## Best Practices

### 1. Forward-Only Policy
- Prefer forward-only migrations in production
- Rollbacks require careful review (data loss risk)
- Use compensating migrations instead of rollbacks when possible

### 2. Atomic Changes
- Each migration should be a single logical change
- Don't combine unrelated schema changes
- Keep migrations small and focused

### 3. Index Considerations
- Add indexes in same migration as table creation
- For large tables, consider `CONCURRENTLY` (separate migration):
  ```sql
  -- +goose Up
  -- +goose StatementBegin
  CREATE INDEX CONCURRENTLY idx_videos_status ON videos(status);
  -- +goose StatementEnd
  ```

### 4. Backwards Compatibility
- Add columns as nullable or with defaults first
- Deploy code that handles both old/new schema
- Then add NOT NULL constraint in follow-up migration

### 5. Required Extensions

Ensure these are enabled (usually in migration 001):
```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "unaccent";
CREATE EXTENSION IF NOT EXISTS "btree_gin";
```

## Common Patterns

### Adding a Column Safely

```sql
-- Migration 1: Add nullable column
ALTER TABLE users ADD COLUMN email_verified BOOLEAN;

-- Deploy code that handles NULL
-- Migration 2: Backfill and add constraint
UPDATE users SET email_verified = false WHERE email_verified IS NULL;
ALTER TABLE users ALTER COLUMN email_verified SET NOT NULL;
ALTER TABLE users ALTER COLUMN email_verified SET DEFAULT false;
```

### Full-Text Search

```sql
-- Use GIN indexes with pg_trgm for partial matching
CREATE INDEX idx_videos_title_gin ON videos USING GIN (title gin_trgm_ops);

-- Or tsvector for full-text
ALTER TABLE videos ADD COLUMN search_vector tsvector;
CREATE INDEX idx_videos_search ON videos USING GIN (search_vector);
```

### Enum Types

```sql
-- Create enum
CREATE TYPE privacy_level AS ENUM ('public', 'private', 'unlisted');

-- Use in table
ALTER TABLE videos ADD COLUMN privacy privacy_level NOT NULL DEFAULT 'private';

-- Adding new value (safe, doesn't require rewrite)
ALTER TYPE privacy_level ADD VALUE 'followers_only';
```

## Key Tables Reference

| Table | Purpose | Key Indexes |
|-------|---------|-------------|
| `users` | User accounts | email, username |
| `videos` | Video metadata | processing_status, privacy, created_at |
| `notifications` | User notifications | user_id + read + created_at |
| `messages` | User messaging | sender_id, recipient_id, created_at |
| `ap_*` | ActivityPub federation | See activitypub/CLAUDE.md |
| `virus_scan_log` | Security audit trail | created_at, file_hash |

## Testing Migrations

```bash
# Test migrate up/down cycle
make migrate-up
make migrate-down
make migrate-up

# Verify schema matches expectations
psql $DATABASE_URL -c "\d+ videos"
```

## Troubleshooting

### Migration Stuck
```sql
-- Check goose version table
SELECT * FROM goose_db_version ORDER BY id DESC LIMIT 5;

-- If needed, manually mark as applied (use with caution)
INSERT INTO goose_db_version (version_id, is_applied) VALUES (42, true);
```

### Concurrent Index Creation Failed
```sql
-- Check for invalid indexes
SELECT * FROM pg_indexes WHERE indexdef LIKE '%INVALID%';

-- Drop invalid and recreate
DROP INDEX CONCURRENTLY IF EXISTS idx_name;
```

# Migration from Atlas to Goose

## Why Migrate to Goose?

1. **No Authentication Required**: Goose is a standalone tool that doesn't require cloud accounts or authentication
2. **Simplicity**: Straightforward SQL migrations without complex configuration
3. **Wide Adoption**: Well-established in the Go community with extensive documentation
4. **Version Control Friendly**: Simple SQL files that work well with git
5. **No Vendor Lock-in**: Open source tool with no cloud dependencies

## Goose Features

- **SQL and Go Migrations**: Support for both plain SQL and Go code migrations
- **Versioned Migrations**: Sequential or timestamp-based versioning
- **Up/Down Migrations**: Built-in rollback support
- **Transaction Support**: Migrations run in transactions by default
- **Custom Statements**: Support for non-transactional statements
- **Multiple Databases**: PostgreSQL, MySQL, SQLite, SQL Server, ClickHouse, Vertica

## Migration Strategy

### Step 1: Install Goose

```bash
# Install via go install
go install github.com/pressly/goose/v3/cmd/goose@latest

# Or via Homebrew (macOS)
brew install goose

# Verify installation
goose --version
```

### Step 2: Configuration

Goose uses environment variables or command-line flags:

```bash
# Database connection
export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING="postgres://vidra_user:vidra_password@localhost:5432/vidra?sslmode=disable"

# Or use DATABASE_URL (more common)
export DATABASE_URL="postgres://vidra_user:vidra_password@localhost:5432/vidra?sslmode=disable"
```

### Step 3: Migration File Format

Goose migrations need a specific header format:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd
```

### Step 4: Convert Existing Migrations

Our current migrations are already in sequential format (`001_`, `002_`, etc.), which Goose supports. We just need to add Goose directives.

### Step 5: Goose Schema Versioning Table

Goose creates its own `goose_db_version` table to track applied migrations:

```sql
CREATE TABLE goose_db_version (
    id SERIAL PRIMARY KEY,
    version_id BIGINT NOT NULL,
    is_applied BOOLEAN NOT NULL,
    tstamp TIMESTAMP DEFAULT now()
);
```

## Implementation Plan

### 1. Create Goose Configuration File

Create `goose.env`:

```bash
GOOSE_DRIVER=postgres
GOOSE_MIGRATION_DIR=./migrations
GOOSE_ENV=development
```

### 2. Update Makefile

Replace Atlas commands with Goose equivalents:

```makefile
# Goose migration commands
migrate-up:
 @goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down:
 @goose -dir migrations postgres "$(DATABASE_URL)" down

migrate-status:
 @goose -dir migrations postgres "$(DATABASE_URL)" status

migrate-create:
 @goose -dir migrations create $(name) sql

migrate-validate:
 @goose -dir migrations postgres "$(DATABASE_URL)" validate

migrate-reset:
 @goose -dir migrations postgres "$(DATABASE_URL)" reset
```

### 3. CI/CD Updates

Update GitHub Actions to use Goose:

```yaml
- name: Install Goose
  run: go install github.com/pressly/goose/v3/cmd/goose@latest

- name: Run Migrations
  env:
    DATABASE_URL: ${{ secrets.DATABASE_URL }}
  run: goose -dir migrations postgres "$DATABASE_URL" up
```

### 4. Docker Integration

Add Goose to Dockerfile:

```dockerfile
# Install goose
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

# Run migrations on startup (optional)
CMD goose -dir /app/migrations postgres "$DATABASE_URL" up && /app/server
```

## Benefits Over Atlas

1. **No Authentication**: No need for Atlas Cloud account
2. **Simpler Configuration**: No complex HCL files
3. **Faster CI/CD**: No external API calls
4. **Better Offline Support**: Works completely offline
5. **Transparent Versioning**: Simple version table in your database
6. **Community Support**: Large user base and extensive documentation

## Migration Checklist

- [ ] Install Goose locally
- [ ] Convert migration files to Goose format
- [ ] Test migrations on local database
- [ ] Update Makefile commands
- [ ] Update CI/CD workflows
- [ ] Update documentation
- [ ] Remove Atlas configuration files
- [ ] Test rollback functionality
- [ ] Update Docker configuration
- [ ] Document new developer setup

## Rollback Plan

If issues arise, we can:

1. Keep original migration files as backup
2. Atlas migrations don't modify the SQL files themselves
3. Can switch back to Atlas if needed (but unlikely)

## Timeline

- **Day 1**: Convert migrations and test locally
- **Day 2**: Update CI/CD and documentation
- **Day 3**: Deploy to staging and test
- **Day 4**: Production deployment

## Notes

- Existing databases won't be affected - Goose will recognize already applied migrations
- Migration history will be preserved in the new `goose_db_version` table
- All team members need to install Goose locally

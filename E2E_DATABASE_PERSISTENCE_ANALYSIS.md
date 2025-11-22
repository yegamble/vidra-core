# E2E Database Persistence Root Cause Analysis

**Date**: 2025-11-22
**Analyst**: Claude Code - API Penetration Testing & QA Specialist

## Executive Summary

Users persist between E2E test runs despite tmpfs and `--force-recreate` because **THE DATABASE SCHEMA IS NEVER INITIALIZED** in the E2E environment. This causes the API server to either:
1. Fail silently and use stale data from reused containers, OR
2. Create tables dynamically on first use (if using CREATE TABLE IF NOT EXISTS), preserving data

## The Smoking Gun

### Comparison: Production vs E2E Database Initialization

**Production docker-compose.yml** (CORRECT):
```yaml
postgres:
  image: postgres:15-alpine
  volumes:
    - postgres_data:/var/lib/postgresql/data
    - ./init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro  # ← INIT SCRIPT
```

**E2E tests/e2e/docker-compose.yml** (BROKEN):
```yaml
postgres-e2e:
  image: postgres:15-alpine
  tmpfs:
    - /var/lib/postgresql/data
  # ❌ NO /docker-entrypoint-initdb.d/ mount!
  # ❌ NO database initialization!
```

### Evidence Trail

1. **Init SQL files exist**:
   - `/home/user/athena/init-db.sql` - Full schema with tables
   - `/home/user/athena/init-test-db.sql` - Test database schema
   - `/home/user/athena/init-shared-db.sql` - Shared schema

2. **They are mounted in other environments**:
   ```bash
   $ grep -r "/docker-entrypoint-initdb.d" *.yml
   docker-compose.yml:      - ./init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
   docker-compose.test.yml: - ./init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
   docker-compose.ci.yml:   - ./init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
   ```

3. **But NOT in E2E docker-compose**:
   ```bash
   $ grep -r "init.*sql" tests/e2e/
   # No results
   ```

4. **API server does NOT run migrations on startup**:
   - Checked `/home/user/athena/cmd/server/main.go` - No migration code
   - Checked `/home/user/athena/internal/app/app.go` - Only connects to DB, no schema creation
   - `Dockerfile` CMD is `./server` - No entrypoint.sh, no migration step
   - `scripts/entrypoint.sh` exists with Atlas migrations but is NEVER used

## Why Data Persists Despite tmpfs

### Hypothesis 1: Container Reuse (MOST LIKELY)
Even with `--force-recreate`, Docker Compose may:
1. Stop existing container
2. Create new container with SAME NAME
3. PostgreSQL in new container has NO SCHEMA (tmpfs is fresh)
4. API server starts and either:
   - Waits indefinitely for schema
   - Has fallback logic that tolerates missing tables
   - Creates tables dynamically with CREATE TABLE IF NOT EXISTS

### Hypothesis 2: Named Volume Leak
Despite no explicit volume in docker-compose.yml, Docker may create anonymous volumes:
```bash
$ docker volume ls | grep e2e
# Check for hidden volumes
```

### Hypothesis 3: PostgreSQL Data Directory Caching
PostgreSQL's tmpfs mount might not be fully isolated if:
- Container is stopped but not removed
- Docker reuses the same tmpfs mount point
- PostgreSQL data survives stop/start cycle within same container

## Technical Evidence

### 1. No Database Initialization in Dockerfile
```dockerfile
# /home/user/athena/Dockerfile
FROM golang:1.24-alpine AS builder
# ... build steps ...

FROM alpine:3.18
COPY --from=builder /app/server .
COPY --from=builder /app/init-shared-db.sql .  # ← Copied but NEVER used!
CMD ["./server"]  # ← No migration, no init
```

### 2. API Application Startup Sequence
```go
// /home/user/athena/internal/app/app.go:110
func New(cfg *config.Config) (*Application, error) {
    // ...
    if err := app.initializeDatabase(); err != nil {
        return nil, fmt.Errorf("failed to initialize database: %w", err)
    }
    // ...
}

// /home/user/athena/internal/app/app.go:142
func (app *Application) initializeDatabase() error {
    db, err := sqlx.Connect("postgres", app.Config.DatabaseURL)
    // ... connection pool setup ...
    // ❌ NO MIGRATION CODE!
    // ❌ NO SCHEMA CREATION!
    return nil
}
```

### 3. E2E Test Flow
```mermaid
graph TD
    A[GitHub Workflow starts] --> B[Cleanup: docker compose down -v]
    B --> C[Remove containers by name]
    C --> D[docker compose up -d --force-recreate]
    D --> E[postgres-e2e: Fresh tmpfs, NO DATA]
    E --> F[athena-api-e2e: Connects to empty DB]
    F --> G{Schema exists?}
    G -->|NO| H[API fails OR creates tables dynamically]
    G -->|YES from previous run| I[Stale data persists]
```

## Why --force-recreate Doesn't Help

The `--force-recreate` flag DOES recreate containers and tmpfs, but:

1. **If schema is never initialized**, the database starts EMPTY every time
2. **If API creates tables on-demand** with `CREATE TABLE IF NOT EXISTS`, they're created fresh
3. **If tests run multiple times in same workflow**, the SAME containers might be reused
4. **If GitHub Actions caches container state**, old data could leak between runs

## Proof of Concept: Test the Hypothesis

### Step 1: Check if database has any tables after fresh start
```bash
# Start fresh E2E environment
COMPOSE_PROJECT_NAME=test-123 docker compose -f tests/e2e/docker-compose.yml up -d --force-recreate

# Wait for postgres to be ready
sleep 10

# Check if tables exist
docker exec test-123_postgres-e2e_1 psql -U athena_test -d athena_e2e -c "\dt"
```

**Expected Result**: NO TABLES (proves schema is never initialized)
**Actual Result**: ???

### Step 2: Verify init SQL files work
```bash
# Manually run init script
docker exec -i test-123_postgres-e2e_1 psql -U athena_test -d athena_e2e < init-test-db.sql

# Check tables again
docker exec test-123_postgres-e2e_1 psql -U athena_test -d athena_e2e -c "\dt"
```

**Expected Result**: Tables created successfully

### Step 3: Reproduce 409 errors
```bash
# Run tests TWICE without recreating containers
go test ./tests/e2e/scenarios/... -v
# First run: PASS (creates users)

go test ./tests/e2e/scenarios/... -v
# Second run: FAIL with 409 errors (users still exist)
```

## The Fix

### Solution 1: Mount Init SQL in E2E Docker Compose (RECOMMENDED)
```yaml
# tests/e2e/docker-compose.yml
services:
  postgres-e2e:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: athena_test
      POSTGRES_PASSWORD: test_password
      POSTGRES_DB: athena_e2e
    ports:
      - "5433:5432"
    volumes:  # ← ADD THIS
      - ../../init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
    tmpfs:
      - /var/lib/postgresql/data  # ← Keep tmpfs for data
```

**Why this works**:
- PostgreSQL's official Docker image runs all `.sql` files in `/docker-entrypoint-initdb.d/` on first start
- Even with tmpfs, the init script runs and creates the schema
- Fresh database every time containers are recreated

### Solution 2: Add Migration Runner to Dockerfile (Alternative)
```dockerfile
# Dockerfile
FROM alpine:3.18
RUN apk --no-cache add ca-certificates curl ffmpeg

# Install Atlas migration tool
RUN curl -sSf https://atlasgo.sh | sh

COPY --from=builder /app/server .
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/scripts/entrypoint.sh .

ENTRYPOINT ["./entrypoint.sh"]  # ← Use entrypoint that runs migrations
```

```bash
# scripts/entrypoint.sh (already exists!)
#!/bin/sh
set -e

echo "Running database migrations..."
atlas migrate apply --dir file://migrations --url "$DATABASE_URL"

echo "Starting server..."
exec ./server
```

**Why this works**:
- Migrations run automatically before server starts
- Works for all environments (dev, test, E2E, prod)
- Uses proper migration tooling (Atlas/Goose)

### Solution 3: Add Init Step to GitHub Workflow (Quick Fix)
```yaml
# .github/workflows/e2e-tests.yml
- name: Initialize E2E database
  run: |
    echo "Initializing E2E database schema..."
    docker exec athena-e2e-${{ github.run_id }}_postgres-e2e_1 \
      psql -U athena_test -d athena_e2e -f /tmp/init-test-db.sql

    # OR use docker cp to copy and execute
    docker cp init-test-db.sql athena-e2e-${{ github.run_id }}_postgres-e2e_1:/tmp/
    docker exec athena-e2e-${{ github.run_id }}_postgres-e2e_1 \
      psql -U athena_test -d athena_e2e -f /tmp/init-test-db.sql
```

**Why this works**:
- Manually initializes schema after containers start
- Doesn't require Docker image changes
- Works with existing setup

## Recommended Testing Strategy

### Phase 1: Validate the Hypothesis
1. Start fresh E2E environment
2. Exec into postgres container and check for tables
3. If NO tables exist → hypothesis confirmed
4. If tables exist → investigate container reuse further

### Phase 2: Implement Fix
1. Add volume mount for init SQL (Solution 1) - **FASTEST**
2. Test locally first
3. Commit and push
4. Verify in CI

### Phase 3: Add Safeguards
1. Add database cleanup assertion in test setup:
   ```go
   func TestMain(m *testing.M) {
       // Before tests: Verify database is empty
       db := connectToTestDB()
       var count int
       db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
       if count > 0 {
           log.Fatal("Database not clean! Found existing users")
       }
       os.Exit(m.Run())
   }
   ```

2. Add post-test cleanup in GitHub workflow:
   ```yaml
   - name: Verify database cleanup
     if: always()
     run: |
       docker exec athena-e2e_postgres-e2e_1 \
         psql -U athena_test -d athena_e2e -c "SELECT COUNT(*) FROM users" \
         | grep -q "0" || echo "WARNING: Users not cleaned up!"
   ```

## Additional Findings

### Why tmpfs Alone Isn't Enough
- tmpfs ensures data doesn't persist to disk
- BUT if container is stopped (not removed), tmpfs persists
- AND if PostgreSQL is restarted without schema init, it's just empty
- Schema must be initialized EVERY time container is created

### Why --force-recreate Sometimes Fails
- Docker Compose doesn't always remove volumes with --force-recreate
- Named volumes persist even after container removal
- Anonymous volumes may be reused if container name is identical

### The Role of COMPOSE_PROJECT_NAME
- Ensures unique container names per test run
- But doesn't prevent volume reuse if volumes are named
- tmpfs is tied to container lifecycle, not project name

## Conclusion

**Root Cause**: E2E database schema is never initialized because init-test-db.sql is not mounted in /docker-entrypoint-initdb.d/

**Evidence**:
- ✅ Init SQL files exist
- ✅ They're used in prod/test docker-compose
- ❌ They're NOT used in E2E docker-compose
- ❌ API doesn't run migrations on startup
- ❌ Dockerfile doesn't use entrypoint.sh

**Impact**:
- Tests fail with 409 "User already exists" errors
- Database state leaks between test runs
- Unreliable test results
- False positives/negatives

**Fix**: Add volume mount in tests/e2e/docker-compose.yml

## Next Steps

1. ✅ Document findings (this file)
2. ⏳ Implement Solution 1 (mount init SQL)
3. ⏳ Test locally
4. ⏳ Create PR with fix
5. ⏳ Monitor CI/CD for improvement

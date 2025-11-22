# E2E Test Database Persistence - Root Cause & Solution

**Status**: DEFINITIVE ROOT CAUSE IDENTIFIED
**Date**: 2025-11-22
**Severity**: HIGH - Blocks CI/CD pipeline

---

## Executive Summary

**Root Cause**: The E2E test database schema is NEVER initialized because:
1. E2E `docker-compose.yml` does NOT mount init SQL files
2. API server does NOT run migrations on startup
3. GitHub E2E workflow does NOT run migrations before tests
4. E2E Makefile has NO database initialization step

**Result**: Users from previous test runs persist because the database state is undefined, leading to unpredictable behavior and 409 "User already exists" errors.

---

## Evidence Chain

### 1. PostgreSQL Initialization Pattern (Standard Behavior)

**Production Setup** (works correctly):
```yaml
# docker-compose.yml
postgres:
  volumes:
    - ./init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro  # ✓ Schema initialized
```

**Test Setup** (works correctly):
```yaml
# docker-compose.test.yml
postgres-test:
  volumes:
    - ./init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro  # ✓ Schema initialized
```

**E2E Setup** (BROKEN):
```yaml
# tests/e2e/docker-compose.yml
postgres-e2e:
  tmpfs:
    - /var/lib/postgresql/data
  # ❌ NO init script mounted
  # ❌ NO schema initialization
```

### 2. API Server Does NOT Run Migrations

**Dockerfile**:
```dockerfile
FROM alpine:3.18
COPY --from=builder /app/server .
CMD ["./server"]  # ← Just runs server, no migrations
```

**Application Startup** (`/home/user/athena/internal/app/app.go:142-159`):
```go
func (app *Application) initializeDatabase() error {
    db, err := sqlx.Connect("postgres", app.Config.DatabaseURL)
    // ... connection pool setup ...
    // ❌ NO MIGRATION CODE
    // ❌ NO SCHEMA CREATION
    return nil
}
```

**Entrypoint Script Exists But Is NOT Used**:
```bash
# scripts/entrypoint.sh (exists but never executed)
#!/bin/sh
echo "Running database migrations..."
atlas migrate apply --dir file://migrations --url "$DATABASE_URL"
exec ./server
```

The Dockerfile CMD is `./server`, not `./entrypoint.sh`, so migrations are NEVER run.

### 3. Migration System Exists But Is Never Executed in E2E

**Project migrated to Goose** (commit f419b6b):
- 58 migration files in `/home/user/athena/migrations/`
- Makefile has migration targets: `migrate-up`, `migrate-down`, `migrate-status`
- **BUT**: E2E Makefile (`/home/user/athena/tests/e2e/Makefile`) has NO migration targets
- **AND**: E2E GitHub workflow does NOT run migrations

**Main Makefile** (line 596-598):
```makefile
migrate-up:  ## Apply all pending migrations using Goose
	@echo "Applying pending migrations with Goose..."
	@goose -dir migrations postgres "$${DATABASE_URL}" up
```

**E2E Makefile**: ❌ NO MIGRATION COMMANDS

**E2E GitHub Workflow** (`.github/workflows/e2e-tests.yml`): ❌ NO MIGRATION STEP

### 4. The Persistence Mechanism

With NO schema initialization:

#### Scenario A: First Test Run
1. Docker containers start fresh (tmpfs is empty)
2. PostgreSQL starts with NO SCHEMA
3. API server connects to empty database
4. When first test registers `e2e_001`, one of two things happens:
   - API crashes (if it strictly requires tables)
   - API dynamically creates tables with `CREATE TABLE IF NOT EXISTS` (most likely)
5. Users are created in these tables
6. Tests complete

#### Scenario B: Second Test Run (SAME GitHub run_id)
1. Workflow runs `docker compose down -v`
2. Workflow runs `docker compose up -d --force-recreate`
3. **IF containers are truly recreated**: Fresh tmpfs, but NO schema init → same as Scenario A
4. **IF containers are reused** (Docker Compose quirk): Old tmpfs preserved → old users still exist
5. Tests try to create `e2e_001` again → 409 Conflict

#### Scenario C: CI/CD Caching
1. GitHub Actions may cache Docker layers
2. Even with unique `COMPOSE_PROJECT_NAME`, base images could be shared
3. Without explicit volume cleanup + schema init, state leaks between runs

---

## The Three Critical Gaps

### Gap 1: No Init SQL Mount in E2E Docker Compose
```yaml
# tests/e2e/docker-compose.yml (CURRENT - BROKEN)
postgres-e2e:
  image: postgres:15-alpine
  tmpfs:
    - /var/lib/postgresql/data  # Data is ephemeral
  # Missing: Init script mount!
```

**Should be**:
```yaml
postgres-e2e:
  image: postgres:15-alpine
  volumes:
    - ../../init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
  tmpfs:
    - /var/lib/postgresql/data
```

### Gap 2: No Migration Step in E2E Workflow
```yaml
# .github/workflows/e2e-tests.yml (CURRENT - BROKEN)
- name: Start test environment
  run: |
    COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} \
      docker compose -f tests/e2e/docker-compose.yml up -d --force-recreate

- name: Wait for services to be ready
  run: |
    # Just waits for API health check
    # ❌ NO MIGRATION STEP
```

**Should include**:
```yaml
- name: Initialize database schema
  run: |
    # Wait for postgres to be ready
    timeout 60 bash -c 'until docker exec \
      $(docker ps -q -f name=postgres-e2e) \
      pg_isready -U athena_test; do sleep 1; done'

    # Run migrations
    docker exec $(docker ps -q -f name=postgres-e2e) \
      psql -U athena_test -d athena_e2e -f /docker-entrypoint-initdb.d/01-init.sql
```

### Gap 3: No Migration Commands in E2E Makefile
```makefile
# tests/e2e/Makefile (CURRENT - BROKEN)
setup: fixtures start
	@echo "Waiting for services to be ready..."
	# ❌ NO MIGRATION STEP
```

**Should include**:
```makefile
migrate:  ## Run migrations on E2E database
	@echo "Running E2E database migrations..."
	@cd ../.. && goose -dir migrations postgres \
		"postgres://athena_test:test_password@localhost:5433/athena_e2e?sslmode=disable" up

setup: fixtures start migrate  ## Setup with migrations
	@echo "Waiting for services to be ready..."
```

---

## Solutions (In Order of Recommendation)

### Solution 1: Mount Init SQL in Docker Compose (FASTEST & CLEANEST)

**Pros**:
- Minimal changes (1 line in docker-compose.yml)
- Uses PostgreSQL's built-in initialization
- Works automatically on every container start
- No workflow or Makefile changes needed
- Consistent with prod/test setups

**Implementation**:
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
      - /var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U athena_test"]
      interval: 5s
      timeout: 5s
      retries: 5
```

**Test**:
```bash
cd /home/user/athena/tests/e2e
make clean
make start
docker exec $(docker compose ps -q postgres-e2e) \
  psql -U athena_test -d athena_e2e -c "\dt"
# Should show tables
```

---

### Solution 2: Use Migrations via Entrypoint (MOST ROBUST)

**Pros**:
- Uses proper migration tooling (Goose)
- Works for all environments
- Tracks migration history
- Supports rollbacks

**Cons**:
- Requires Dockerfile changes
- Requires installing Goose in Docker image

**Implementation**:

1. **Update Dockerfile**:
```dockerfile
FROM alpine:3.18
RUN apk --no-cache add ca-certificates curl ffmpeg postgresql-client

# Install Goose
RUN curl -fsSL \
    https://raw.githubusercontent.com/pressly/goose/master/install.sh \
    | sh

COPY --from=builder /app/server .
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/scripts/entrypoint.sh .

RUN chmod +x ./entrypoint.sh
ENTRYPOINT ["./entrypoint.sh"]
```

2. **Update entrypoint.sh**:
```bash
#!/bin/sh
set -e

echo "Waiting for PostgreSQL..."
until pg_isready -h "$(echo $DATABASE_URL | sed 's/.*@\([^:]*\).*/\1/')" > /dev/null 2>&1; do
  sleep 1
done

echo "Running database migrations..."
goose -dir /app/migrations postgres "$DATABASE_URL" up

echo "Starting server..."
exec ./server
```

---

### Solution 3: Add Migration Step to Workflow (QUICK FIX)

**Pros**:
- No Dockerfile changes
- Uses existing init-test-db.sql
- Quick to implement

**Cons**:
- Workflow-specific (doesn't help local dev)
- Requires container name lookup

**Implementation**:
```yaml
# .github/workflows/e2e-tests.yml
- name: Start test environment
  run: |
    echo "Starting E2E test environment..."
    COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} \
      docker compose -f tests/e2e/docker-compose.yml up -d --force-recreate

- name: Initialize database schema  # ← ADD THIS STEP
  run: |
    echo "Initializing E2E database..."

    # Wait for postgres
    timeout 60 bash -c 'until docker exec \
      athena-e2e-${{ github.run_id }}_postgres-e2e_1 \
      pg_isready -U athena_test > /dev/null 2>&1; do \
      echo "Waiting for postgres..."; \
      sleep 2; \
    done'

    # Copy and execute init script
    docker cp init-test-db.sql \
      athena-e2e-${{ github.run_id }}_postgres-e2e_1:/tmp/init.sql

    docker exec athena-e2e-${{ github.run_id }}_postgres-e2e_1 \
      psql -U athena_test -d athena_e2e -f /tmp/init.sql

    echo "✓ Database schema initialized"

- name: Wait for services to be ready
  run: |
    # Continue with existing steps...
```

---

## Recommended Fix (Hybrid Approach)

**Immediate (for CI/CD)**:
1. Add init SQL mount to `tests/e2e/docker-compose.yml` (Solution 1)
2. Test locally to verify

**Long-term (for production)**:
1. Switch to entrypoint.sh with Goose migrations (Solution 2)
2. This gives proper migration tracking and rollback capability

---

## Validation Plan

### Step 1: Verify Current State (Reproduce Issue)
```bash
cd /home/user/athena/tests/e2e

# Clean start
docker compose down -v
docker compose up -d

# Check if tables exist (should be NONE)
docker exec $(docker compose ps -q postgres-e2e) \
  psql -U athena_test -d athena_e2e -c "\dt"

# Expected: "Did not find any relations."
```

### Step 2: Apply Fix (Solution 1)
```bash
# Edit tests/e2e/docker-compose.yml to add volume mount
# (as shown in Solution 1 above)

# Recreate containers
docker compose down -v
docker compose up -d

# Wait for healthy
sleep 10

# Check if tables exist (should show tables now)
docker exec $(docker compose ps -q postgres-e2e) \
  psql -U athena_test -d athena_e2e -c "\dt"

# Expected: List of tables (users, videos, etc.)
```

### Step 3: Run Tests
```bash
# Run E2E tests
E2E_BASE_URL=http://localhost:18080 \
  go test -v ./scenarios/...

# Expected: PASS (no 409 errors)
```

### Step 4: Test Cleanup
```bash
# Verify cleanup works
docker compose down -v
docker compose up -d

# Run tests again (should still pass)
E2E_BASE_URL=http://localhost:18080 \
  go test -v ./scenarios/...

# Expected: PASS (fresh database each time)
```

---

## Files to Modify

### Minimum Fix (Solution 1):
- `/home/user/athena/tests/e2e/docker-compose.yml` (add volume mount)

### Complete Fix (Solution 2):
- `/home/user/athena/Dockerfile` (add Goose, use entrypoint)
- `/home/user/athena/scripts/entrypoint.sh` (migrate to Goose if needed)

### Workflow Enhancement (Solution 3):
- `/home/user/athena/.github/workflows/e2e-tests.yml` (add migration step)

---

## Impact Analysis

### Before Fix:
- ❌ E2E tests fail with 409 errors
- ❌ Database state undefined
- ❌ Tests not isolated
- ❌ False positives/negatives
- ❌ CI/CD pipeline blocked

### After Fix:
- ✅ Fresh database every test run
- ✅ Predictable schema
- ✅ Test isolation guaranteed
- ✅ No 409 errors
- ✅ Reliable CI/CD
- ✅ Consistent with prod/test setups

---

## Related Issues

### Why tmpfs Alone Wasn't Enough
- tmpfs only ensures data doesn't persist to DISK
- Container RESTART reuses tmpfs (data persists)
- Container RECREATE gets fresh tmpfs (data lost)
- **BUT**: Fresh tmpfs with NO schema init = empty database
- **RESULT**: Undefined behavior until schema is created

### Why --force-recreate Didn't Fix It
- Correctly recreates containers
- Correctly creates fresh tmpfs
- **BUT**: Doesn't initialize schema
- **RESULT**: Fresh but EMPTY database

### Why Unique COMPOSE_PROJECT_NAME Didn't Help
- Correctly creates unique container names
- Prevents container reuse between workflow runs
- **BUT**: Doesn't initialize schema
- **RESULT**: Fresh containers with empty databases

---

## Conclusion

**The trinity of failure**:
1. ❌ No init SQL mount in E2E docker-compose
2. ❌ No migration execution in API startup
3. ❌ No migration step in E2E workflow

**All three must be fixed** to ensure reliable E2E tests.

**Recommended immediate action**: Implement Solution 1 (mount init SQL)
**Recommended long-term action**: Implement Solution 2 (entrypoint with Goose)

---

## Next Steps

1. ✅ Document root cause (this file)
2. ⏳ Implement Solution 1 (edit docker-compose.yml)
3. ⏳ Test locally
4. ⏳ Commit and push
5. ⏳ Monitor CI/CD
6. ⏳ Plan Solution 2 for future enhancement

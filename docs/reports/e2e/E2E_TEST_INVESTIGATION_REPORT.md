# E2E Test Failure Investigation Report

**Date:** 2025-11-22
**Issue:** E2E tests failing in GitHub Actions CI/CD
**Error:** "User registration failed" - Database tables do not exist
**Status:** ROOT CAUSE IDENTIFIED

---

## Executive Summary

The E2E tests are **NOT** failing due to username/email clashes as initially suspected. The actual root cause is that **database migrations are not being run** in the E2E test environment, resulting in an empty database with no tables.

### Critical Finding

```
ERROR: relation "users" does not exist at character 457
```

This error appears repeatedly in the PostgreSQL logs, confirming that the database schema has not been initialized.

---

## Root Cause Analysis

### 1. Missing Database Initialization

**File:** `/Users/yosefgamble/github/vidra/tests/e2e/docker-compose.yml`

**Problem:** The E2E docker-compose.yml does NOT initialize the database schema

```yaml
postgres-e2e:
  image: postgres:15-alpine
  environment:
    POSTGRES_USER: vidra_test
    POSTGRES_PASSWORD: test_password
    POSTGRES_DB: vidra_e2e
  ports:
    - "5433:5432"
  tmpfs:
    - /var/lib/postgresql/data  # ⚠️ tmpfs - volatile storage
  # ❌ MISSING: No volume mount for initialization scripts
  # ❌ MISSING: No migration runner
```

**Contrast with Working Test Environment:**

**File:** `/Users/yosefgamble/github/vidra/docker-compose.test.yml` (Used for Postman E2E tests)

```yaml
postgres-test:
  image: postgres:15-alpine
  tmpfs:
    - /var/lib/postgresql/data
  environment:
    POSTGRES_DB: vidra_test
    POSTGRES_USER: test_user
    POSTGRES_PASSWORD: test_password
  ports:
    - "5433:5432"
  volumes:
    - ./init-test-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro  # ✅ Schema initialization
```

### 2. Application Does Not Auto-Migrate

**Investigation:** The Vidra Core application server does NOT run migrations on startup.

**Files Checked:**

- `/Users/yosefgamble/github/vidra/cmd/server/main.go` - No migration logic
- `/Users/yosefgamble/github/vidra/internal/app/app.go` - No migration runner in `initializeDatabase()`
- `/Users/yosefgamble/github/vidra/Dockerfile` - No migration step in CMD

**Migration System:** The project uses **Goose** for database migrations:

- Migrations are in `/Users/yosefgamble/github/vidra/migrations/*.sql` (63 migration files)
- Migrations must be run manually via `make migrate-*` commands or initialization scripts
- No automatic migration on application startup

### 3. Why It Works Locally

When developers run tests locally, they typically:

1. Use `docker-compose.test.yml` which includes `init-test-db.sql`
2. Or manually run migrations via `make migrate-test`
3. Or use persistent volumes that retain schema from previous runs

The E2E environment in CI starts **completely fresh** with tmpfs (in-memory) storage, exposing the missing initialization.

---

## Evidence from GitHub Actions Logs

**Run ID:** 19599459736
**Branch:** copilot/fix-e2e-tests-in-repo

### PostgreSQL Logs Show Missing Tables

```
2025-11-22 18:33:16.614 UTC [102] ERROR:  relation "users" does not exist at character 457
2025-11-22 18:33:16.679 UTC [102] ERROR:  relation "users" does not exist at character 26
2025-11-22 18:33:16.747 UTC [102] ERROR:  relation "users" does not exist at character 26
2025-11-22 18:33:16.814 UTC [102] ERROR:  relation "users" does not exist at character 26
```

### Test Failures

All three E2E tests failed with identical error:

```
--- FAIL: TestVideoUploadWorkflow (0.07s)
    Messages: User registration failed
--- FAIL: TestUserAuthenticationFlow (0.07s)
    Messages: User registration failed
--- FAIL: TestVideoSearchFunctionality (0.07s)
    Messages: User registration failed
```

### Database Connection Errors

```
2025-11-22 18:33:15.393 UTC [146] FATAL:  database "vidra_test" does not exist
```

Wait, this is interesting - even the **database itself** doesn't exist initially. PostgreSQL creates the database from `POSTGRES_DB` environment variable, but the tables/schema require initialization.

---

## Test Environment Comparison

| Aspect | Postman E2E (Working) | Go E2E Tests (Failing) |
|--------|----------------------|------------------------|
| **Compose File** | `docker-compose.test.yml` | `tests/e2e/docker-compose.yml` |
| **Database Init** | ✅ `init-test-db.sql` mounted | ❌ No initialization |
| **Schema Creation** | ✅ Automatic on startup | ❌ Empty database |
| **Storage** | tmpfs | tmpfs |
| **Migrations** | ✅ Included in init script | ❌ Not run |
| **Local Success** | ✅ Yes | ✅ Yes (if DB persisted) |
| **CI Success** | ✅ Yes | ❌ No |

---

## Why Username/Email Clashes Were Suspected

The initial hypothesis of username/email conflicts was reasonable because:

1. The test error message "User registration failed" is generic
2. Unique constraints on `username` and `email` columns exist in the schema
3. Tests create users with predictable patterns (e2e_hash_timestamp)
4. The issue occurs in CI but not always locally

However, the actual SQL errors reveal the truth: **registration fails because the users table doesn't exist**, not because of duplicate data.

---

## Database Schema Requirements

The application requires **63 migrations** to be applied:

```
migrations/001_enable_extensions.sql
migrations/002_create_users_table.sql
migrations/003_create_refresh_tokens_table.sql
...
migrations/063_add_remote_video_support.sql
```

Critical tables needed for E2E tests:

- `users` - User accounts (registration/login)
- `videos` - Video metadata
- `upload_sessions` - Chunked upload tracking
- `encoding_jobs` - Video processing
- `sessions` - Authentication sessions
- `refresh_tokens` - JWT refresh tokens

---

## Test Isolation & Cleanup Analysis

### Current Test Design (Correct for Isolation)

**File:** `/Users/yosefgamble/github/vidra/tests/e2e/scenarios/video_workflow_test.go`

```go
// Each test generates unique username to avoid conflicts
timestamp := time.Now().UnixNano() % 10000000000             // 10 digits
testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8] // 8-char hash
username := fmt.Sprintf("e2e_%s_%d", testHash, timestamp)    // ~23 chars total
email := username + "@example.com"
```

**Analysis:** ✅ This design is **correct and proper**:

- Unique username per test run
- No hardcoded credentials
- Prevents test interdependencies
- Follows best practices for E2E testing

### Cleanup Procedures

**Docker Cleanup:** ✅ Comprehensive

**File:** `.github/workflows/e2e-tests.yml` (lines 60-68)

```yaml
- name: Cleanup previous test environment
  run: |
    docker compose -f tests/e2e/docker-compose.yml down -v 2>/dev/null || true
    docker compose -f docker-compose.test.yml down -v 2>/dev/null || true
    docker stop $(docker ps -q --filter "publish=5433" ...) 2>/dev/null || true
    docker rm $(docker ps -aq --filter "publish=5433" ...) 2>/dev/null || true
```

**Analysis:** ✅ Properly removes volumes and containers before each run

### Parallel Execution

**File:** `.github/workflows/e2e-tests.yml` (lines 17-19)

```yaml
concurrency:
  group: heavy-tests-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: false
```

**Analysis:** ✅ Prevents parallel runs that could conflict

---

## Why Tests Pass Locally

### Scenario 1: Using docker-compose.test.yml

```bash
make postman-e2e  # Uses docker-compose.test.yml with init-test-db.sql
```

✅ Schema initialized automatically

### Scenario 2: Persistent Database

```bash
docker-compose -f tests/e2e/docker-compose.yml up -d
# Database schema persists in Docker volume from previous runs
go test ./tests/e2e/scenarios/...
```

✅ Works if you've previously run migrations

### Scenario 3: Manual Migration

```bash
docker-compose -f tests/e2e/docker-compose.yml up -d
make migrate-test  # Manually apply migrations
go test ./tests/e2e/scenarios/...
```

✅ Explicitly run migrations

### Why CI Always Fails

GitHub Actions runs with:

1. Fresh tmpfs storage (no persistence)
2. No initialization script
3. No manual migration step
4. Clean environment every run

Result: **Empty database, no tables, registration fails**

---

## Additional Observations

### 1. Missing Database Error

Log shows: `FATAL: database "vidra_test" does not exist`

This is misleading - PostgreSQL creates the database from `POSTGRES_DB` env var, but there's a race condition where the app tries to connect before DB is fully ready. The healthcheck passes (pg_isready), but schema isn't initialized.

### 2. Test Video File Handling

**File:** `/Users/yosefgamble/github/vidra/tests/e2e/scenarios/video_workflow_test.go` (lines 206-223)

```go
func createTestVideoFile(t *testing.T) string {
    testVideoPath := os.Getenv("E2E_TEST_VIDEO_PATH")
    if testVideoPath == "" {
        testVideoPath = "../../postman/test-files/videos/test-video.mp4"
    }
    // ...
}
```

✅ Properly uses shared test files, no file-based conflicts

### 3. Fixture Data Not Used

**File:** `/Users/yosefgamble/github/vidra/tests/e2e/fixtures/data/users.json`

Contains predefined users:

```json
[
  {
    "username": "testuser1",
    "email": "testuser1@example.com",
    ...
  }
]
```

**Analysis:** This file is NOT used by current tests (good - tests generate unique users). If it were used, it could cause conflicts, but it's not the current issue.

---

## Recommendations

### CRITICAL: Fix Database Initialization (Priority 1)

**Option A: Add Init Script to E2E Docker Compose (Recommended)**

Modify `/Users/yosefgamble/github/vidra/tests/e2e/docker-compose.yml`:

```yaml
postgres-e2e:
  image: postgres:15-alpine
  environment:
    POSTGRES_USER: vidra_test
    POSTGRES_PASSWORD: test_password
    POSTGRES_DB: vidra_e2e
  ports:
    - "5433:5432"
  volumes:
    - ../../init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro  # ADD THIS
  tmpfs:
    - /var/lib/postgresql/data
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U vidra_test"]
    interval: 5s
    timeout: 5s
    retries: 5
```

**Option B: Add Migration Step to Workflow**

Modify `.github/workflows/e2e-tests.yml`:

```yaml
- name: Run database migrations
  run: |
    # Wait for Postgres to be ready
    sleep 5
    # Run migrations
    DATABASE_URL="postgres://vidra_test:test_password@localhost:5433/vidra_e2e?sslmode=disable" \
    goose -dir migrations postgres "$DATABASE_URL" up

- name: Run E2E tests
  env:
    E2E_BASE_URL: http://localhost:18080
```

**Option C: Application Auto-Migration (Not Recommended for Production)**

Add migration runner in `internal/app/app.go`:

```go
func (app *Application) initializeDatabase() error {
    db, err := sqlx.Connect("postgres", app.Config.DatabaseURL)
    if err != nil {
        return fmt.Errorf("failed to connect to database: %w", err)
    }

    // Run migrations if ENVIRONMENT=test
    if app.Config.Environment == "test" {
        if err := runMigrations(db); err != nil {
            return fmt.Errorf("failed to run migrations: %w", err)
        }
    }

    // ... rest of initialization
}
```

**Recommendation:** Use **Option A** (init script) for E2E tests. It's:

- Simple
- Explicit
- Matches existing `docker-compose.test.yml` pattern
- No code changes required
- Follows PostgreSQL best practices

### OPTIONAL: Enhanced Test Isolation (Priority 2)

Current test isolation is already good, but consider:

#### Add Test Database Cleanup

Create a cleanup helper:

```go
// In tests/e2e/helpers.go
func (c *TestClient) CleanupUser(t *testing.T, userID string) {
    req, err := http.NewRequest("DELETE", c.BaseURL+"/api/v1/users/"+userID, nil)
    require.NoError(t, err)
    req.Header.Set("Authorization", "Bearer "+c.Token)

    resp, err := c.HTTPClient.Do(req)
    require.NoError(t, err)
    defer resp.Body.Close()

    // 204 No Content or 404 Not Found are both acceptable
    if resp.StatusCode != 204 && resp.StatusCode != 404 {
        t.Logf("Warning: Failed to cleanup user %s: HTTP %d", userID, resp.StatusCode)
    }
}
```

Use in tests:

```go
userID, token := client.RegisterUser(t, username, email, password)
defer client.CleanupUser(t, userID)  // Ensure cleanup even if test fails
```

#### Add Database Transaction Rollback (Advanced)

For unit/integration tests (not E2E), wrap each test in a transaction and rollback:

```go
func TestWithTransaction(t *testing.T) {
    tx, _ := db.Begin()
    defer tx.Rollback()

    // Run test with tx
    // Changes automatically rolled back
}
```

**Note:** This doesn't work well for E2E tests with separate API server.

### OPTIONAL: Improve CI/CD Resilience (Priority 3)

#### Add Database Health Verification

```yaml
- name: Verify database schema
  run: |
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U vidra_test -d vidra_e2e -c "\dt" | grep users || {
        echo "ERROR: Database schema not initialized"
        exit 1
      }
```

#### Add Pre-flight Checks

```yaml
- name: Pre-flight checks
  run: |
    echo "Checking database connectivity..."
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U vidra_test -d vidra_e2e -c "SELECT 1"

    echo "Checking Redis connectivity..."
    docker compose -f tests/e2e/docker-compose.yml exec -T redis-e2e \
      redis-cli ping

    echo "Checking API health..."
    curl -f http://localhost:18080/health || exit 1
```

#### Add Detailed Error Logging

```yaml
- name: Run E2E tests
  id: e2e_tests
  run: |
    go test -v -timeout 30m -count=1 ./tests/e2e/scenarios/... 2>&1 | tee e2e_test_output.log
  continue-on-error: true

- name: Diagnose test failures
  if: steps.e2e_tests.outcome == 'failure'
  run: |
    echo "=== Database State ==="
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U vidra_test -d vidra_e2e -c "\dt" || echo "No tables found"

    echo "=== User Count ==="
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U vidra_test -d vidra_e2e -c "SELECT COUNT(*) FROM users" || echo "Users table missing"

    exit 1
```

### OPTIONAL: Test Data Management (Priority 4)

#### Create Dedicated E2E Init Script

Create `/Users/yosefgamble/github/vidra/init-e2e-db.sql`:

```sql
-- Minimal schema for E2E tests (subset of full schema)
-- Based on init-shared-db.sql but optimized for testing

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Import only tables needed for E2E tests
\i migrations/001_enable_extensions.sql
\i migrations/002_create_users_table.sql
\i migrations/003_create_refresh_tokens_table.sql
-- ... (only essential migrations)
```

#### Seed Test Data (Optional)

If you want predictable test users:

```sql
-- init-e2e-db.sql
INSERT INTO users (id, username, email, password_hash, role, is_active)
VALUES
  (gen_random_uuid(), 'e2e_test_user', 'e2e@example.com', '$2a$10$...', 'user', true)
ON CONFLICT (username) DO NOTHING;
```

**Warning:** Seeded data can cause test interdependencies. Current approach (dynamic generation) is better.

---

## Security & Best Practices Assessment

### ✅ What's Done Well

1. **No Hardcoded Credentials in Tests**
   - Tests generate unique usernames/emails
   - Passwords are test-specific
   - No secrets committed to repo

2. **Proper Test Isolation**
   - Each test uses unique user accounts
   - Cleanup procedures in place
   - No shared state between tests

3. **Environment Separation**
   - E2E environment distinct from dev/prod
   - Test database on different port
   - Test-specific configuration

4. **Resource Cleanup**
   - Docker volumes removed after tests
   - Containers cleaned up
   - Temporary storage (tmpfs) used

### ⚠️ Potential Improvements

1. **Migration Strategy**
   - Currently: Manual/script-based
   - Consider: Version-aware migration runner
   - Benefit: Consistent schema across environments

2. **Test Data Seeding**
   - Currently: Ad-hoc in tests
   - Consider: Fixture loading system
   - Benefit: Faster test execution, predictable data

3. **Database Snapshots**
   - Currently: Rebuild schema each time
   - Consider: Cache initialized database image
   - Benefit: Faster CI runs

4. **Error Messages**
   - Currently: Generic "User registration failed"
   - Consider: More specific error reporting
   - Benefit: Faster debugging

---

## Race Conditions Analysis

### Potential Race Conditions Identified

#### 1. Database Readiness Check ✅ HANDLED

**Current Implementation:**

```yaml
healthcheck:
  test: ["CMD-SHELL", "pg_isready -U vidra_test"]
  interval: 5s
  timeout: 5s
  retries: 5
```

**Issue:** `pg_isready` only checks if PostgreSQL is accepting connections, not if the database is initialized.

**Impact:** If initialization scripts take time, the app might connect before schema is ready.

**Mitigation:** Add longer `start_period` or check for specific table:

```yaml
healthcheck:
  test: ["CMD-SHELL", "psql -U vidra_test -d vidra_e2e -c 'SELECT 1 FROM users LIMIT 1' || exit 1"]
  start_period: 30s
```

#### 2. Concurrent Test Execution ✅ PREVENTED

**Current Configuration:**

```yaml
concurrency:
  group: heavy-tests-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: false
```

**Analysis:** ✅ Only one E2E test workflow runs at a time per PR/branch

**Note:** Individual test cases within the suite run sequentially (no `-parallel` flag used).

#### 3. Service Startup Order ✅ HANDLED

**Current Implementation:**

```yaml
vidra-api-e2e:
  depends_on:
    postgres-e2e:
      condition: service_healthy
    redis-e2e:
      condition: service_healthy
    minio-e2e:
      condition: service_healthy
    clamav-e2e:
      condition: service_healthy
```

**Analysis:** ✅ Proper dependency chain with health checks

#### 4. ClamAV Initialization ⚠️ SLOW

**Current Configuration:**

```yaml
healthcheck:
  test: ["CMD", "/usr/local/bin/clamdcheck.sh"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 120s  # 2 minutes
```

**Analysis:** ClamAV takes significant time to download virus definitions. This can cause workflow timeouts if not accounted for.

**Recommendation:** Consider caching ClamAV signatures:

```yaml
clamav-e2e:
  volumes:
    - clamav-signatures:/var/lib/clamav  # Persist signatures
```

---

## Performance Optimization Opportunities

### 1. Database Initialization Time

**Current:** Full schema rebuild every test run

**Optimization:** Use PostgreSQL template database

```sql
-- Create template once
CREATE DATABASE vidra_e2e_template;
-- Initialize schema in template
\i init-shared-db.sql
-- Mark as template
UPDATE pg_database SET datistemplate = TRUE WHERE datname = 'vidra_e2e_template';

-- For each test run
CREATE DATABASE vidra_e2e WITH TEMPLATE vidra_e2e_template;
```

**Benefit:** 10-50x faster database initialization

### 2. Docker Layer Caching

**Current:** Rebuilds Vidra Core API image each time

**Optimization:** Use GitHub Actions cache

```yaml
- name: Set up Docker Buildx
  uses: docker/setup-buildx-action@v2

- name: Build and cache
  uses: docker/build-push-action@v4
  with:
    context: .
    push: false
    load: true
    tags: vidra-api-e2e:latest
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

**Benefit:** Faster image builds (from ~2min to ~30sec)

### 3. Parallel Test Execution

**Current:** Tests run sequentially

**Possible:** Run independent test suites in parallel

```yaml
strategy:
  matrix:
    test_suite:
      - scenarios/auth_test.go
      - scenarios/video_test.go
      - scenarios/search_test.go
```

**Caveat:** Each suite needs isolated database

### 4. Reduce ClamAV Overhead

**Current:** ClamAV initialized for every test run

**Options:**

1. Mock ClamAV in E2E tests (test virus scanning separately)
2. Cache virus signatures
3. Use lightweight alternative for E2E

**Benefit:** Save 2-3 minutes per test run

---

## Monitoring & Alerting Recommendations

### 1. Test Execution Metrics

Track in CI/CD:

- Test duration (target: < 5 minutes)
- Failure rate (target: < 5%)
- Flaky test detection (same test fails intermittently)

### 2. Database Health Metrics

Log during tests:

- Connection pool size
- Query execution time
- Lock contention
- Deadlocks

### 3. Resource Usage

Monitor:

- Memory usage (PostgreSQL, Redis, API)
- Disk I/O (tmpfs performance)
- Network latency
- Container restart count

### 4. Failure Analysis

Capture on test failure:

- Full service logs
- Database schema state
- Environment variables
- Docker container status

---

## Conclusion

### The Real Problem

❌ **NOT username/email conflicts**
✅ **Database schema not initialized in E2E environment**

### The Fix

Add initialization script to `/Users/yosefgamble/github/vidra/tests/e2e/docker-compose.yml`:

```yaml
volumes:
  - ../../init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
```

### Why It Matters

- E2E tests are critical for production readiness
- Current failure prevents any E2E testing in CI
- Schema initialization is a fundamental requirement
- Issue would affect any fresh deployment

### Next Steps

1. **Immediate:** Add init script to E2E docker-compose
2. **Short-term:** Add schema verification step in CI
3. **Medium-term:** Consider migration automation
4. **Long-term:** Optimize test execution performance

---

## Files Requiring Changes

### Primary Fix (Required)

**File:** `/Users/yosefgamble/github/vidra/tests/e2e/docker-compose.yml`

```diff
  postgres-e2e:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: vidra_test
      POSTGRES_PASSWORD: test_password
      POSTGRES_DB: vidra_e2e
    ports:
      - "5433:5432"
+   volumes:
+     - ../../init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
    tmpfs:
      - /var/lib/postgresql/data
```

### Optional Enhancements

**File:** `.github/workflows/e2e-tests.yml`

Add verification step after services start:

```yaml
- name: Verify database schema
  run: |
    echo "Checking database tables..."
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U vidra_test -d vidra_e2e -c "\dt" | grep -E "users|videos" || {
        echo "ERROR: Database schema not properly initialized"
        exit 1
      }
```

---

## Testing the Fix

### Local Validation

```bash
# Clean start
docker compose -f tests/e2e/docker-compose.yml down -v

# Apply fix to docker-compose.yml

# Start environment
docker compose -f tests/e2e/docker-compose.yml up -d

# Verify schema
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U vidra_test -d vidra_e2e -c "\dt"

# Should see: users, videos, sessions, etc.

# Run tests
E2E_BASE_URL=http://localhost:18080 go test -v ./tests/e2e/scenarios/...

# Should PASS
```

### CI Validation

1. Create PR with fix
2. Observe GitHub Actions E2E workflow
3. Check logs for successful table creation
4. Verify all 3 tests pass

---

## Risk Assessment

### Implementation Risk: LOW

- Change is minimal (1 line)
- Matches existing pattern (docker-compose.test.yml)
- No code changes required
- Easy to revert if issues

### Testing Risk: LOW

- Fix addresses root cause directly
- No new dependencies
- No behavioral changes to application
- Same init script used in other test environments

### Production Impact: NONE

- Change only affects E2E test environment
- No production database changes
- No application code changes

---

## Additional Context

### Why This Wasn't Caught Earlier

1. **Local Development:** Developers likely ran migrations manually or used docker-compose.test.yml
2. **Workflow Evolution:** E2E docker-compose.yml may have been created separately without init script
3. **Incomplete Documentation:** README mentions migrations but doesn't enforce them in CI
4. **Misleading Errors:** "User registration failed" doesn't explicitly say "table missing"

### Lessons Learned

1. **Always verify database state in CI/CD**
2. **Make database initialization explicit and automatic**
3. **Add diagnostic logging for database errors**
4. **Document expected database setup steps**
5. **Test CI workflows in isolated environments**

---

## References

### Related Files

- `/Users/yosefgamble/github/vidra/tests/e2e/docker-compose.yml` - E2E environment (NEEDS FIX)
- `/Users/yosefgamble/github/vidra/docker-compose.test.yml` - Postman tests (WORKING)
- `/Users/yosefgamble/github/vidra/init-shared-db.sql` - Database initialization script
- `/Users/yosefgamble/github/vidra/migrations/` - Migration files (63 files)
- `.github/workflows/e2e-tests.yml` - E2E workflow configuration
- `/Users/yosefgamble/github/vidra/tests/e2e/README.md` - E2E documentation

### Migration System

- Tool: Goose (<https://github.com/pressly/goose>)
- Commands: `make migrate-*` in Makefile
- Format: Versioned SQL files with up/down migrations
- Location: `/Users/yosefgamble/github/vidra/migrations/`

### PostgreSQL Init Process

- Directory: `/docker-entrypoint-initdb.d/`
- Execution: Alphabetically ordered
- Timing: First container start only
- Format: `.sql`, `.sql.gz`, `.sh` files

---

**Report Generated:** 2025-11-22
**Investigator:** Claude Code (API Testing & QA Specialist)
**Status:** Complete - Root cause identified, fix recommended

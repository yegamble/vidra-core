# E2E Database Cleanup and tmpfs Configuration Analysis

**Analysis Date:** 2025-11-21
**Problem:** Tests receiving 409 errors suggesting users already exist despite expected ephemeral database

## Executive Summary

**CRITICAL FINDINGS:**

1. **tmpfs IS properly configured** for PostgreSQL data directory
2. **Container names are HARDCODED**, breaking `COMPOSE_PROJECT_NAME` isolation
3. **Database migrations are NEVER run** during E2E container startup
4. **Database state is truly ephemeral** (when containers are properly removed)
5. **Username collision prevention is implemented** but 409 errors suggest container reuse

## Detailed Analysis

### 1. tmpfs Configuration Status ✓

**File:** `/home/user/athena/tests/e2e/docker-compose.yml` (Lines 16-17)

```yaml
postgres-e2e:
  tmpfs:
    - /var/lib/postgresql/data
```

**Verdict:** tmpfs is CORRECTLY configured. PostgreSQL data is stored in-memory and will be completely lost when the container is removed.

**Verification:**

- No persistent volume mounts on `/var/lib/postgresql/data`
- No named volumes in the docker-compose.yml
- tmpfs directive properly targets PostgreSQL data directory

### 2. COMPOSE_PROJECT_NAME Isolation - BROKEN ⚠️

**File:** `/home/user/athena/tests/e2e/docker-compose.yml`

**Problem:** Hardcoded `container_name` directives override project-based naming:

```yaml
Line 9:   container_name: athena-e2e-postgres    # HARDCODED
Line 27:  container_name: athena-e2e-redis       # HARDCODED
Line 39:  container_name: athena-e2e-minio       # HARDCODED
Line 56:  container_name: athena-e2e-clamav      # HARDCODED
Line 75:  container_name: athena-e2e-api         # HARDCODED
Line 127: name: athena-e2e-network               # HARDCODED NETWORK
```

**Workflow Usage:** `/home/user/athena/.github/workflows/e2e-tests.yml`

```bash
Line 78:  COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose up -d
Line 91:  COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose ps
Line 134: COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose down -v
```

**Impact:**

- `COMPOSE_PROJECT_NAME` is used but **INEFFECTIVE** due to hardcoded names
- All test runs share the same container names regardless of run_id
- All test runs share the same network name (athena-e2e-network)
- If cleanup fails, containers persist and can be reused by subsequent runs
- Network reuse means containers can discover and connect to wrong instances
- This explains the 409 user collision errors!

### 3. Database Migration Status - NEVER RUN ⚠️

**Dockerfile Analysis:** `/home/user/athena/Dockerfile` (Line 60)

```dockerfile
CMD ["./server"]
```

**Finding:** No migration runner is executed during container startup.

**Migration Files Examined:**

- `/home/user/athena/migrations/002_create_users_table.sql` - No seed data
- All 63 migration files checked - No INSERT statements found
- Migrations only contain table definitions, indexes, and schema changes

**Migration Runners Found:**

1. `scripts/entrypoint.sh` - Uses Atlas migrate but NOT used in Dockerfile
2. `internal/testutil/helpers.go::RunMigrations()` - Only for unit tests
3. `scripts/run_test_migrations.sh` - Manual script, not automated

**Conclusion:** The application startup does NOT run migrations. Database schema must either:

- Be pre-applied to the PostgreSQL image
- Run automatically on first connection (not implemented)
- Be run manually before tests (not documented)

**THIS IS LIKELY THE ROOT CAUSE OF 409 ERRORS:** If the application starts without a schema, it cannot create users. If containers are reused from a previous run (due to failed cleanup), old users persist.

### 4. Database Ephemeral Nature - VERIFIED ✓

**When cleanup works correctly:**

- tmpfs ensures data is in-memory only
- `docker compose down -v` removes all volumes
- Database is completely fresh for next run

**Current State Check:**

```bash
$ docker volume ls --filter "name=athena"
# Result: No volumes found

$ docker ps -a --filter "name=athena-e2e"
# Result: No containers running

$ docker network ls --filter "name=athena"
# Result: No networks found
```

**Verdict:** When containers are properly removed, state is truly ephemeral.

### 5. Username Collision Prevention - IMPLEMENTED ✓

**File:** `/home/user/athena/tests/e2e/scenarios/video_workflow_test.go`

Lines 36-40:

```go
timestamp := time.Now().UnixNano()
username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)
email := username + "@example.com"
password := "SecurePass123!"
```

**Verdict:** Tests correctly generate unique usernames using:

- Test name (e.g., "TestVideoUploadWorkflow")
- Nanosecond timestamp
- This ensures uniqueness even within milliseconds

**Similar pattern in all test functions:**

- Line 114: `authtest_{testname}_{timestamp}`
- Line 164: `searchtest_{testname}_{timestamp}`

### 6. Cleanup Process Analysis

**File:** `/home/user/athena/.github/workflows/e2e-tests.yml`

**Cleanup Steps:**

```bash
# Pre-test cleanup (lines 66-73)
docker compose -f tests/e2e/docker-compose.yml down -v 2>/dev/null || true
docker compose -f docker-compose.test.yml down -v 2>/dev/null || true
docker stop $(docker ps -q --filter "publish=5433" ...) 2>/dev/null || true
docker rm $(docker ps -aq --filter "publish=5433" ...) 2>/dev/null || true

# Post-test cleanup (lines 132-134)
COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose down -v
```

**Issues:**

1. Pre-test cleanup does NOT use `COMPOSE_PROJECT_NAME` (line 68)
2. This is actually GOOD - it cleans up ALL E2E containers regardless of project name
3. However, race condition jobs might start while cleanup is running

### 7. Potential Race Conditions

**Workflow Configuration:** Lines 15-17

```yaml
concurrency:
  group: heavy-tests-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: false
```

**Two concurrent jobs:**

1. `e2e-tests` (line 24)
2. `e2e-tests-race` (line 147)

**Both jobs:**

- Run on `self-hosted` runner
- May run concurrently if conditions met
- Use same hardcoded container names
- Would conflict for ports 5433, 6380, 18080

## Root Cause Analysis

The 409 "user already exists" errors occur due to a **perfect storm** of issues:

### Primary Cause: Container Name Collision

1. Hardcoded container names prevent proper isolation
2. If cleanup fails or is incomplete, containers persist
3. Next test run reuses existing containers with old data
4. Old users still exist in the in-memory database
5. New test attempts to register same username → 409 error

### Secondary Cause: Missing Migrations

1. Containers start without running migrations
2. If this is the first run, database has no schema
3. User registration would fail (different error)
4. Suggests migrations are being cached from previous runs

### Tertiary Cause: Concurrent Job Potential

1. Two test jobs might run simultaneously
2. Both try to use same hardcoded container names
3. Port conflicts or container conflicts occur
4. One job might see the other's database state

## Evidence Supporting Root Cause

1. **tmpfs works:** No volumes found, containers not running currently
2. **Unique usernames:** Tests use nanosecond timestamps
3. **Container reuse:** Hardcoded names mean containers can be reused
4. **Migration persistence:** If containers aren't removed, migrated schema persists

## Recommended Solutions (Prioritized)

### CRITICAL - Fix Container Name and Network Collision

**Problem:** Hardcoded container names AND network name break isolation
**Solution:** Remove all `container_name` directives AND network name from docker-compose.yml

```yaml
# BEFORE (BROKEN):
postgres-e2e:
  container_name: athena-e2e-postgres  # REMOVE THIS

networks:
  default:
    name: athena-e2e-network  # REMOVE THIS

# AFTER (FIXED):
postgres-e2e:
  # Let Docker Compose generate names based on COMPOSE_PROJECT_NAME

networks:
  default:
    # Let Docker Compose generate network name based on COMPOSE_PROJECT_NAME
```

**Impact:**

- Each CI run gets unique container names: `athena-e2e-{run_id}_postgres-e2e_1`
- Each CI run gets unique network: `athena-e2e-{run_id}_default`
- No container reuse between runs
- No network reuse between runs
- True isolation achieved
- Cleanup failures only affect that specific run

### HIGH PRIORITY - Add Migration Runner

**Problem:** Migrations never run on container startup
**Solution:** Update Dockerfile to use entrypoint.sh

```dockerfile
# Copy migrations
COPY --from=builder /app/migrations ./migrations

# Copy and set entrypoint
COPY --from=builder /app/scripts/entrypoint.sh .
RUN chmod +x entrypoint.sh

CMD ["./entrypoint.sh"]
```

**Alternative:** Add goose binary to container and run migrations in entrypoint

### MEDIUM PRIORITY - Improve Cleanup Robustness

**Problem:** Pre-test cleanup doesn't use COMPOSE_PROJECT_NAME
**Solution:** Already correct - global cleanup is safer

**Additional Safety:**

```bash
# Add after existing cleanup
docker system prune -f --volumes --filter "label=com.docker.compose.project=athena-e2e*"
```

### LOW PRIORITY - Add Explicit Synchronization

**Problem:** Concurrent jobs might conflict
**Solution:** Ensure only one heavy test runs at a time

```yaml
concurrency:
  group: heavy-tests
  cancel-in-progress: true
```

## Testing Verification

To verify the fix works:

1. **Check container names are dynamic:**

   ```bash
   COMPOSE_PROJECT_NAME=test-123 docker compose up -d
   docker ps --format "{{.Names}}"
   # Should show: test-123_postgres-e2e_1, test-123_redis-e2e_1, etc.
   ```

2. **Verify tmpfs is mounted:**

   ```bash
   docker inspect test-123_postgres-e2e_1 | grep -A 5 Tmpfs
   # Should show: /var/lib/postgresql/data
   ```

3. **Confirm migrations run:**

   ```bash
   docker logs test-123_athena-api-e2e_1 | grep -i migrate
   # Should show: "Running database migrations..."
   ```

4. **Test cleanup:**

   ```bash
   COMPOSE_PROJECT_NAME=test-123 docker compose down -v
   docker ps -a | grep test-123
   # Should show: nothing
   ```

## Costs and Risks

### Removing container_name

- **Risk:** LOW - Standard Docker Compose practice
- **Cost:** Container names become longer but more explicit
- **Benefit:** Complete isolation, no state leakage

### Adding migrations to entrypoint

- **Risk:** MEDIUM - Startup time increases slightly
- **Cost:** Additional dependencies (goose or atlas) in container
- **Benefit:** Guaranteed schema consistency

### Cleanup improvements

- **Risk:** LOW - More aggressive cleanup
- **Cost:** Negligible
- **Benefit:** More reliable test environments

## Key Metrics for Success

1. **Zero 409 errors** in test runs
2. **Clean container state** verified before each run
3. **Migration logs** visible in container startup
4. **Unique container names** for each CI run
5. **No manual intervention** required for cleanup

## Files Requiring Changes

1. `/home/user/athena/tests/e2e/docker-compose.yml` - Remove all container_name directives
2. `/home/user/athena/Dockerfile` - Add migration runner and entrypoint
3. `/home/user/athena/.github/workflows/e2e-tests.yml` - (Optional) Improve cleanup commands

## Conclusion

The database IS ephemeral when the system works correctly. The 409 errors are caused by **container reuse due to hardcoded container names** combined with **incomplete cleanup**. The tmpfs configuration is correct, but Docker Compose's `COMPOSE_PROJECT_NAME` isolation is being defeated by explicit container naming.

**Priority Fix:** Remove all `container_name` directives from docker-compose.yml. This single change will restore proper isolation and eliminate the 409 errors.

**Secondary Fix:** Add migration runner to ensure database schema is always initialized, preventing errors from empty databases.

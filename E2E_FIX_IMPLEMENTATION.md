# E2E Test Fix - Implementation Guide

## TL;DR

**Problem:** E2E tests fail because the database has no schema (no tables exist)
**Root Cause:** PostgreSQL container starts empty - no migrations run
**Fix:** Add database initialization script to docker-compose.yml
**Effort:** 1-line change
**Risk:** LOW

---

## The Fix

### File: `tests/e2e/docker-compose.yml`

**Add this one line:**

```yaml
postgres-e2e:
  image: postgres:15-alpine
  environment:
    POSTGRES_USER: athena_test
    POSTGRES_PASSWORD: test_password
    POSTGRES_DB: athena_e2e
  ports:
    - "5433:5432"
  volumes:                                                                    # ADD THIS LINE
    - ../../init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro    # ADD THIS LINE
  tmpfs:
    - /var/lib/postgresql/data
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U athena_test"]
    interval: 5s
    timeout: 5s
    retries: 5
```

**That's it!** This mounts the initialization script that creates all database tables.

---

## Why This Works

1. PostgreSQL automatically runs scripts in `/docker-entrypoint-initdb.d/` on first start
2. `init-shared-db.sql` contains the full database schema (63 migrations worth)
3. This is the same pattern used in `docker-compose.test.yml` (which works)
4. The script runs before the API server starts (thanks to health checks)

---

## Verification Steps

### 1. Apply the fix

Edit `tests/e2e/docker-compose.yml` and add the volumes section.

### 2. Test locally

```bash
# Clean slate
docker compose -f tests/e2e/docker-compose.yml down -v

# Start with fix
docker compose -f tests/e2e/docker-compose.yml up -d

# Wait for services
sleep 30

# Verify schema exists
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U athena_test -d athena_e2e -c "\dt" | grep users

# Should show the users table (and many others)
```

### 3. Run E2E tests

```bash
E2E_BASE_URL=http://localhost:18080 \
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
go test -v -timeout 30m ./tests/e2e/scenarios/...
```

Expected output:
```
=== RUN   TestVideoUploadWorkflow
--- PASS: TestVideoUploadWorkflow (5.23s)
=== RUN   TestUserAuthenticationFlow
--- PASS: TestUserAuthenticationFlow (0.45s)
=== RUN   TestVideoSearchFunctionality
--- PASS: TestVideoSearchFunctionality (3.12s)
PASS
ok      athena/tests/e2e/scenarios      8.801s
```

### 4. Verify in CI

Push the change and watch GitHub Actions workflow:

```bash
git add tests/e2e/docker-compose.yml
git commit -m "fix(e2e): Add database schema initialization to E2E tests

The E2E test environment was starting with an empty database (no tables),
causing all tests to fail with 'User registration failed' because the
users table didn't exist.

This adds the init-shared-db.sql script to the PostgreSQL container
startup, matching the pattern used in docker-compose.test.yml."
git push
```

Check the workflow logs for:
```
✓ API is ready
Running E2E tests...
PASS
```

---

## Optional: Enhanced Verification

Add a pre-flight check to the GitHub Actions workflow to catch this in the future.

### File: `.github/workflows/e2e-tests.yml`

Add after the "Wait for services to be ready" step:

```yaml
- name: Verify database schema
  run: |
    echo "Verifying database initialization..."

    # Check for essential tables
    TABLES=$(docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      psql -U athena_test -d athena_e2e -t -c "\dt" | grep -E "users|videos|sessions" | wc -l)

    if [ "$TABLES" -lt 3 ]; then
      echo "ERROR: Database schema not properly initialized"
      echo "Expected at least users, videos, and sessions tables"
      docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
        psql -U athena_test -d athena_e2e -c "\dt"
      exit 1
    fi

    echo "✓ Database schema verified ($TABLES tables found)"
```

---

## What Was Happening Before

### The Error Chain

1. PostgreSQL container starts with empty database
2. API server starts and connects to database
3. E2E test tries to register a user
4. Registration handler queries: `SELECT * FROM users WHERE email = ?`
5. PostgreSQL returns: `ERROR: relation "users" does not exist`
6. API returns 500 Internal Server Error
7. Test sees: "User registration failed"

### Why It Worked Locally

Developers were either:
- Using `docker-compose.test.yml` (has init script)
- Running migrations manually with `make migrate-test`
- Reusing persistent Docker volumes from previous runs

CI always started fresh with tmpfs (in-memory) storage, exposing the issue.

---

## Alternative Solutions (Not Recommended)

### Option B: Run migrations in workflow

```yaml
- name: Run database migrations
  run: |
    # Wait for Postgres
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \
      pg_isready -U athena_test

    # Install goose
    go install github.com/pressly/goose/v3/cmd/goose@latest

    # Run migrations
    DATABASE_URL="postgres://athena_test:test_password@localhost:5433/athena_e2e?sslmode=disable" \
      goose -dir migrations postgres "$DATABASE_URL" up
```

**Why not recommended:**
- Adds dependency on goose in CI
- Slower than init script
- Doesn't match other test environments
- More complex

### Option C: Application auto-migration

Make the app run migrations on startup when `ENVIRONMENT=test`.

**Why not recommended:**
- Changes application behavior
- Risk of running migrations in wrong environment
- Production anti-pattern
- Not how the app currently works

---

## Impact Assessment

### What Changes
✅ E2E test environment now initializes database schema
✅ E2E tests can actually run successfully
✅ CI/CD pipeline can validate E2E workflows

### What Doesn't Change
❌ No production code changes
❌ No API behavior changes
❌ No migration files modified
❌ No test logic changes

### Risks
- **Very Low:** This is a test environment configuration change only
- Same script used successfully in `docker-compose.test.yml`
- Easy to rollback if any issues arise

---

## Related Issues

### Not Actually Username/Email Conflicts

The initial hypothesis was that tests were failing due to duplicate usernames/emails, but investigation revealed:

1. Tests properly generate unique usernames using timestamp + hash
2. Database cleanup happens between CI runs (volumes removed)
3. Actual error was "relation 'users' does not exist" (no table)
4. "User registration failed" was a symptom, not the root cause

### No Test Isolation Issues

Current test design is **correct**:
- Each test creates unique users
- Tests don't share state
- Proper cleanup procedures exist
- No race conditions found

---

## Success Criteria

After applying this fix:

✅ All E2E tests pass in CI
✅ Database schema is automatically initialized
✅ No manual migration steps required
✅ Tests run reliably and consistently
✅ CI workflow succeeds on every run

---

## Rollback Plan

If any issues arise:

```bash
# Remove the volumes section
git revert HEAD
git push

# Or manually edit docker-compose.yml to remove:
# volumes:
#   - ../../init-shared-db.sql:/docker-entrypoint-initdb.d/01-init.sql:ro
```

---

## Questions & Answers

**Q: Why not use migrations instead of init-shared-db.sql?**
A: We could, but init-shared-db.sql is a consolidated schema that's faster to apply and already used in other test environments.

**Q: Will this affect production?**
A: No, this only changes the E2E test environment configuration.

**Q: What if init-shared-db.sql is outdated?**
A: It should be kept in sync with migrations. Consider adding a CI check to validate this.

**Q: Why tmpfs for database storage?**
A: Faster I/O, automatic cleanup, no disk space consumption. Perfect for CI tests.

**Q: Could we cache the database state?**
A: Yes, but it adds complexity. The init script is fast enough (< 5 seconds).

---

## Timeline

- **Investigation:** Complete
- **Fix Identified:** Complete
- **Implementation:** 5 minutes (edit 1 file, add 2 lines)
- **Testing:** 10 minutes (local validation)
- **Deployment:** Automatic (merge to main)
- **Verification:** Next CI run

**Total estimated time:** 30 minutes from start to verified working in CI

---

## Follow-up Tasks

### Immediate (Post-Fix)
- [ ] Monitor first successful CI run
- [ ] Verify all 3 E2E tests pass
- [ ] Check execution time (should be < 10 minutes total)

### Short-term
- [ ] Add database schema verification step to CI (shown above)
- [ ] Document E2E setup in README more clearly
- [ ] Consider adding more E2E test scenarios

### Long-term
- [ ] Keep init-shared-db.sql in sync with migrations
- [ ] Consider automating schema file generation from migrations
- [ ] Expand E2E test coverage (federation, encoding, etc.)

---

## Contact

For questions about this fix:
- See full investigation: `E2E_TEST_INVESTIGATION_REPORT.md`
- Check E2E documentation: `tests/e2e/README.md`
- Review migration system: `Makefile` (search for "migrate")

---

**Status:** Ready to implement
**Confidence:** High (matches working pattern in docker-compose.test.yml)
**Urgency:** High (blocks all E2E testing in CI)

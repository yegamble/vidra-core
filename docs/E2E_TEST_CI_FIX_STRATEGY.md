# E2E Test CI/CD Failure Fix Strategy

## Executive Summary

This document outlines the comprehensive fix strategy for E2E test failures in GitHub Actions CI/CD environment. The primary issues were:

1. **409 Conflict Errors**: Username collisions during user registration
2. **429 Rate Limit Errors**: Exhausted rate limits due to shared IP addresses and rapid test execution

## Root Cause Analysis

### Issue 1: 409 "CREATE_FAILED: Failed to create user"

**Primary Causes:**
- **Insufficient entropy in username generation**: Even with nanosecond timestamps, parallel or rapid sequential execution could cause collisions
- **Database state pollution**: Users from previous test runs not being cleaned up
- **Email uniqueness constraints**: Emails derived from usernames inherit collision risks

**Secondary Causes:**
- Race conditions in parallel test execution
- Modulo operation on timestamps reducing entropy: `time.Now().UnixNano() % 10000000000`
- No atomic counter or UUID for guaranteed uniqueness

### Issue 2: 429 "Rate limit exceeded"

**Primary Causes:**
- **Shared IP address**: All E2E tests run from `127.0.0.1`, sharing the same rate limit bucket
- **Strict production-level rate limits**:
  - Registration: 5 requests/minute (routes.go:37)
  - Login: 10 requests/minute (routes.go:38)
  - General: 100 requests/minute (routes.go:32)
- **No delays between tests**: Tests execute in rapid succession
- **High request volume**: Each test makes 5-15+ API calls (register, upload, get, list, search, delete)

**Example Rate Limit Calculation:**
```
3 tests × (1 registration + 5-15 API calls) = ~50 requests in < 5 seconds
This can easily exceed the 100 req/min general limit when rate limiter state persists
```

**Secondary Causes:**
- No retry logic with exponential backoff
- No test-specific rate limit configuration
- Rate limiter state persists across sequential tests

## Comprehensive Fix Strategy

### Fix 1: Enhanced Username Generation with Multi-Layer Entropy

**Location**: `/home/user/athena/tests/e2e/helpers.go`

**Implementation:**
```go
// GenerateUniqueUsername uses 4 layers of entropy:
// 1. Atomic counter (guaranteed unique across goroutines)
// 2. Nanosecond timestamp (time-based uniqueness)
// 3. Random hex string (cryptographic randomness)
// 4. Test name (implicit via test context)
func GenerateUniqueUsername(t *testing.T) string {
    counter := usernameCounter.Add(1)  // Atomic increment
    timestamp := time.Now().UnixNano() % 100000000
    randomBytes := make([]byte, 4)
    rand.Read(randomBytes)
    randomHex := hex.EncodeToString(randomBytes)

    return fmt.Sprintf("e2e_%03d_%d_%s", counter, timestamp, randomHex)
}
```

**Collision Probability:**
- Atomic counter: 0% collision (guaranteed unique)
- Even if counter resets: timestamp (100M values) × random (4.3B values) = effectively zero collision probability

### Fix 2: Retry Logic with Exponential Backoff

**Location**: `/home/user/athena/tests/e2e/helpers.go`

**Implementation:**
```go
func (c *TestClient) RegisterUser(t *testing.T, username, email, password string) (userID, token string) {
    maxRetries := 5
    baseDelay := 2 * time.Second

    for attempt := 0; attempt < maxRetries; attempt++ {
        if attempt > 0 {
            delay := baseDelay * time.Duration(1<<uint(attempt-1))
            time.Sleep(delay)  // 2s, 4s, 8s, 16s, 32s
        }

        resp, err := c.Post("/auth/register", "application/json", body)

        if resp.StatusCode == http.StatusCreated {
            break  // Success
        }

        if resp.StatusCode == http.StatusTooManyRequests {
            continue  // Retry with backoff
        }

        if resp.StatusCode == http.StatusConflict {
            t.Fatalf("Terminal error: user exists")  // Don't retry
        }
    }
}
```

**Retry Strategy:**
- **429 Rate Limit**: Retry with exponential backoff (max 5 attempts, up to 62s total wait)
- **409 Conflict**: Terminal error, fail immediately (indicates username collision)
- **Other errors**: Retry with backoff
- **Success (201)**: Break immediately

### Fix 3: Relaxed Rate Limits for Test Environment

**Location**: `/home/user/athena/tests/e2e/docker-compose.yml`

**Implementation:**
```yaml
environment:
  # Rate Limiting - Relaxed for E2E testing
  RATE_LIMIT_REQUESTS: "1000"  # 10x production (100 → 1000)
  RATE_LIMIT_WINDOW: "60"       # 60 second window
```

**Impact:**
- General rate limit: 1000 req/min (vs 100 in production)
- Allows 3 tests × ~15 requests = 45 requests without hitting limits
- Provides headroom for test retries
- Note: Stricter auth limits (5 reg/min, 10 login/min) are hardcoded in routes.go and still apply

### Fix 4: Sequential Test Execution

**Location**: `/home/user/athena/.github/workflows/e2e-tests.yml`

**Implementation:**
```bash
go test -v -timeout 30m -count=1 -p=1 -parallel=1 ./tests/e2e/scenarios/...
```

**Flags Explained:**
- `-p=1`: Run only 1 package at a time (sequential package execution)
- `-parallel=1`: Run only 1 test at a time within a package (sequential test execution)
- `-count=1`: Disable test caching (ensure fresh runs)

**Benefits:**
- Prevents rate limit bucket sharing across parallel tests
- Ensures proper test isolation
- Allows retry logic time to work
- More predictable resource usage

### Fix 5: Rate Limit Delays Between Tests

**Location**: `/home/user/athena/tests/e2e/scenarios/video_workflow_test.go` (all test files)

**Implementation:**
```go
// Before each registration
e2e.RateLimitDelay(t, 500*time.Millisecond)

userID, token := client.RegisterUser(t, username, email, password)
```

**Impact:**
- 500ms delay before each registration reduces burst traffic
- Spreads requests across rate limit window
- Minimal test duration impact (~1.5s total for 3 tests)

## File Changes Summary

### Modified Files

1. **`/home/user/athena/tests/e2e/helpers.go`**
   - Lines 1-26: Added imports for `crypto/rand`, `encoding/hex`, `sync/atomic`
   - Lines 23-26: Added global atomic counter for username uniqueness
   - Lines 86-171: Updated `RegisterUser()` with retry logic and exponential backoff
   - Lines 173-257: Updated `Login()` with retry logic and exponential backoff
   - Lines 456-501: Added helper functions:
     - `GenerateUniqueUsername()`: Multi-layer entropy username generation
     - `GenerateTestEmail()`: Email generation from username
     - `RateLimitDelay()`: Controlled delays to avoid rate limits

2. **`/home/user/athena/tests/e2e/scenarios/video_workflow_test.go`**
   - Lines 3-12: Removed unused imports (`crypto/md5`, `fmt`)
   - Lines 37-50: Updated `TestVideoUploadWorkflow()` to use new helpers
   - Lines 115-128: Updated `TestUserAuthenticationFlow()` to use new helpers
   - Lines 167-176: Updated `TestVideoSearchFunctionality()` to use new helpers

3. **`/home/user/athena/tests/e2e/docker-compose.yml`**
   - Lines 103-107: Added relaxed rate limit configuration for test environment

4. **`/home/user/athena/.github/workflows/e2e-tests.yml`**
   - Lines 94-106: Updated E2E test execution with sequential flags and documentation
   - Lines 210-221: Updated race detector test execution with sequential flags

## Testing Strategy

### Local Testing

```bash
# Set up test environment
docker compose -f tests/e2e/docker-compose.yml up -d

# Wait for services
timeout 180 bash -c 'until curl -sf http://localhost:18080/health > /dev/null 2>&1; do sleep 5; done'

# Run tests sequentially
go test -v -timeout 30m -count=1 -p=1 -parallel=1 ./tests/e2e/scenarios/...

# Cleanup
docker compose -f tests/e2e/docker-compose.yml down -v
```

### CI/CD Testing

The GitHub Actions workflow now automatically:
1. Cleans up previous test environments
2. Starts isolated containers using `COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }}`
3. Waits for services to be healthy
4. Runs tests sequentially with retry logic
5. Collects logs on failure
6. Cleans up environment

## Expected Outcomes

### Before Fixes
```
TestVideoUploadWorkflow: FAIL (409 Conflict)
TestUserAuthenticationFlow: FAIL (429 Rate Limit)
TestVideoSearchFunctionality: FAIL (429 Rate Limit)
```

### After Fixes
```
TestVideoUploadWorkflow: PASS (with retry logging if rate limited)
TestUserAuthenticationFlow: PASS (with retry logging if rate limited)
TestVideoSearchFunctionality: PASS (with retry logging if rate limited)
```

### Test Duration Impact
- **Before**: ~5 seconds (when tests don't fail)
- **After**: ~6-7 seconds (500ms × 3 delays + retry overhead if needed)
- **On Retry**: Up to ~65 seconds worst case (if all retries needed: 2+4+8+16+32 = 62s)

## Monitoring and Alerting

### Test Logs to Monitor

1. **Username Generation:**
   ```
   Generated unique username: e2e_001_12345678_a1b2c3d4
   ```

2. **Successful Registration:**
   ```
   Successfully registered user: e2e_001_12345678_a1b2c3d4 (ID: uuid-here)
   ```

3. **Retry Attempts:**
   ```
   Retry attempt 2/5 after 4s (previous error: rate limit exceeded (429))
   ```

4. **Rate Limit Delays:**
   ```
   Rate limit delay: sleeping for 500ms
   ```

### Failure Patterns to Watch

1. **Persistent 409 Errors**: Indicates username generation issue or database cleanup failure
2. **Excessive Retries**: Indicates rate limits may still be too strict
3. **Test Timeouts**: Indicates retry logic is taking too long

## Advanced Troubleshooting

### If Tests Still Fail with 409

1. **Check database cleanup:**
   ```bash
   docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e psql -U athena_test -d athena_e2e -c "SELECT username FROM users WHERE username LIKE 'e2e_%';"
   ```

2. **Verify atomic counter is incrementing:**
   - Check test logs for username patterns
   - Ensure counter values are sequential

3. **Check for database constraints:**
   ```bash
   docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e psql -U athena_test -d athena_e2e -c "\d users"
   ```

### If Tests Still Fail with 429

1. **Check rate limiter configuration:**
   ```bash
   docker compose -f tests/e2e/docker-compose.yml exec athena-api-e2e env | grep RATE_LIMIT
   ```

2. **Increase rate limits further:**
   - In `docker-compose.yml`, increase `RATE_LIMIT_REQUESTS` to 5000
   - Consider also setting stricter limits via environment variables if hardcoded

3. **Add more delays:**
   - Increase `RateLimitDelay` from 500ms to 1000ms
   - Add delays between other API calls, not just registration

4. **Check rate limiter state persistence:**
   - Verify Redis is properly isolated per test run
   - Check if rate limiter is using in-memory (good) or Redis (could persist)

## Alternative Strategies (If Above Fixes Insufficient)

### Strategy A: Per-Test Rate Limit Reset

Implement an admin endpoint to reset rate limiters (test environment only):

```go
// In routes.go (test environment only)
if cfg.Environment == "test" {
    r.Post("/test/reset-rate-limits", adminHandler.ResetRateLimits)
}
```

Call this endpoint before each test:
```go
func (t *TestClient) ResetRateLimits(t *testing.T) {
    c.Post("/test/reset-rate-limits", "application/json", nil)
}
```

### Strategy B: Per-User Rate Limiting (Instead of Per-IP)

Modify rate limiter to track by user ID instead of IP for authenticated endpoints:

```go
func extractRateLimitKey(r *http.Request) string {
    if userID := r.Context().Value("user_id"); userID != nil {
        return fmt.Sprintf("user:%s", userID)
    }
    return extractIP(r)  // Fallback to IP
}
```

### Strategy C: Test-Specific Subdomains

Use different subdomains for each test to get separate IP-based rate limit buckets:

```yaml
# docker-compose.yml
extra_hosts:
  - "test1.localhost:127.0.0.1"
  - "test2.localhost:127.0.0.1"
  - "test3.localhost:127.0.0.1"
```

### Strategy D: Database Cleanup Between Tests

Add cleanup in test teardown:

```go
func CleanupTestUser(t *testing.T, username string) {
    db := connectToTestDB(t)
    defer db.Close()

    _, err := db.Exec("DELETE FROM users WHERE username LIKE 'e2e_%'")
    require.NoError(t, err)
}
```

## Performance Impact Analysis

### CI/CD Build Time
- **Before**: ~3-5 minutes (when tests pass)
- **After**: ~3-6 minutes (500ms delays + retry overhead)
- **Worst Case**: ~4-8 minutes (if retries are needed)

### Resource Usage
- **CPU**: Minimal impact (sequential execution)
- **Memory**: Minimal impact (one test at a time)
- **Network**: Slight increase due to retries
- **Database**: Minimal impact (same number of users created)

## Security Considerations

### Rate Limit Configuration
- Test environment uses 10x relaxed limits (1000 vs 100 req/min)
- This is acceptable because:
  1. Test environment is isolated
  2. Not exposed to public internet
  3. Only used in CI/CD and local development
  4. Uses temporary containers with tmpfs storage

### Production Rate Limits Unchanged
- Production still uses strict limits:
  - General: 100 req/min
  - Registration: 5 req/min (hardcoded in routes.go)
  - Login: 10 req/min (hardcoded in routes.go)

## Conclusion

This comprehensive fix strategy addresses both the 409 username collision errors and 429 rate limit errors through multiple complementary approaches:

1. **Better username generation** (atomic counter + timestamp + random hex)
2. **Intelligent retry logic** (exponential backoff, terminal error detection)
3. **Relaxed test environment rate limits** (10x production limits)
4. **Sequential test execution** (prevents parallel rate limit conflicts)
5. **Strategic delays** (spreads requests across rate limit windows)

The fixes are defensive in depth - even if one mechanism fails, others provide fallback protection. This ensures reliable E2E test execution in CI/CD environments while maintaining strict rate limits in production.

## References

- [Go Testing Package Documentation](https://pkg.go.dev/testing)
- [Rate Limiting Patterns](https://cloud.google.com/architecture/rate-limiting-strategies-techniques)
- [E2E Test Best Practices](https://martinfowler.com/articles/practical-test-pyramid.html)
- Related commits:
  - d9ab0fc: Fix critical E2E test 409 errors
  - 96be3a3: Fix username collisions in E2E tests

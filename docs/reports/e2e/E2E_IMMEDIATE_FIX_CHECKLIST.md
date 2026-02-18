# E2E Test Immediate Fix Checklist

**Priority:** CRITICAL - Blocks all E2E tests
**Estimated Time:** 30 minutes
**Date:** 2025-11-23

---

## Issue Summary

The E2E tests are failing with HTTP 400 errors due to:

1. ✅ **FIXED:** Database not initialized → `init-shared-db.sql` now mounted in docker-compose
2. ❌ **TO FIX:** Login endpoint expects "email" but test sends "username"
3. ❌ **TO FIX:** Missing validation environment variables in E2E docker-compose

---

## Fix #1: Login Field Mismatch (2 options)

### Option A: Update E2E Test (EASIER - Recommended)

**File:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go`

**Change Line 119:**

```diff
func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
    payload := map[string]interface{}{
-       "username": username,
+       "email": username,  // Handler expects "email" field
        "password": password,
    }
```

**OR** change function signature to accept email:

```diff
-func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
+func (c *TestClient) Login(t *testing.T, email, password string) (userID, token string) {
    payload := map[string]interface{}{
-       "username": username,
+       "email": email,
        "password": password,
    }
```

**Also update test calls** in `/Users/yosefgamble/github/athena/tests/e2e/scenarios/video_workflow_test.go` line 132:

```diff
- userID2, token2 := client2.Login(t, username, password)
+ userID2, token2 := client2.Login(t, email, password)
```

---

### Option B: Update Login Handler (MORE ROBUST)

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go`

**Change Lines 82-89:**

```diff
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
    var reqData map[string]interface{}
    if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
        return
    }

    email, _ := reqData["email"].(string)
+   username, _ := reqData["username"].(string)
    password, _ := reqData["password"].(string)
    twoFACode, _ := reqData["twofa_code"].(string)

-   if email == "" || password == "" {
-       shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email and password are required"))
+   if (email == "" && username == "") || password == "" {
+       shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email/username and password are required"))
        return
    }

-   // Lookup user and verify password
-   dUser, err := s.userRepo.GetByEmail(r.Context(), email)
+   // Lookup user by email or username
+   var dUser *domain.User
+   var err error
+   if email != "" {
+       dUser, err = s.userRepo.GetByEmail(r.Context(), email)
+   } else {
+       dUser, err = s.userRepo.GetByUsername(r.Context(), username)
+   }
    if err != nil {
        shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
        return
    }
```

**Pros of Option B:**

- More user-friendly (accept email OR username)
- No test changes needed
- Better UX for end users

**Cons of Option B:**

- More code changes
- Need to ensure GetByUsername method exists

---

## Fix #2: Add Validation Environment Variables

**File:** `/Users/yosefgamble/github/athena/tests/e2e/docker-compose.yml`

**Add these lines under `athena-api-e2e.environment`** (around line 115):

```diff
    # Logging
    LOG_LEVEL: "debug"
+
+   # Validation Configuration (CRITICAL)
+   VALIDATION_STRICT_MODE: "false"
+   VALIDATION_ALLOWED_ALGORITHMS: "sha256"
+   VALIDATION_TEST_MODE: "true"
+   VALIDATION_ENABLE_INTEGRITY_JOBS: "false"
+   VALIDATION_LOG_EVENTS: "true"
+
+   # Upload Configuration
+   MAX_UPLOAD_SIZE: "1073741824"  # 1GB for E2E
+   CHUNK_SIZE: "10485760"         # 10MB chunks
+   MAX_CONCURRENT_UPLOADS: "5"
+
+   # Rate Limiting (relaxed for E2E)
+   RATE_LIMIT_REQUESTS: "1000"
+   RATE_LIMIT_WINDOW: "60"
+
+   # Storage
+   STORAGE_DIR: "/tmp/athena-e2e-storage"
+
+   # ClamAV Fallback
+   CLAMAV_FALLBACK_MODE: "warn"  # Don't block E2E if ClamAV slow
```

---

## Fix #3: Update GitHub Workflow (Optional but Recommended)

**File:** `.github/workflows/e2e-tests.yml`

**Add after "Wait for services to be ready" step:**

```yaml
- name: Verify database schema initialization
  run: |
    echo "Checking database tables..."
    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \\
      psql -U athena_test -d athena_e2e -c "\\dt" | grep -E "users|videos|upload_sessions" || {
        echo "ERROR: Critical tables missing"
        docker compose -f tests/e2e/docker-compose.yml logs postgres-e2e
        exit 1
      }

- name: Verify API configuration
  run: |
    echo "Testing API health endpoint..."
    curl -f http://localhost:18080/health || {
      echo "ERROR: API health check failed"
      docker compose -f tests/e2e/docker-compose.yml logs athena-api-e2e
      exit 1
    }
```

---

## Testing the Fixes

### 1. Test Locally

```bash
# Clean slate
docker compose -f tests/e2e/docker-compose.yml down -v

# Start with fixes
docker compose -f tests/e2e/docker-compose.yml up -d

# Wait for services
sleep 30

# Verify database
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \\
  psql -U athena_test -d athena_e2e -c "\\dt" | grep users

# Verify API
curl http://localhost:18080/health

# Run E2E tests
E2E_BASE_URL=http://localhost:18080 \\
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \\
go test -v ./tests/e2e/scenarios/...
```

**Expected Output:**

```
=== RUN   TestVideoUploadWorkflow
    video_workflow_test.go:49: Registered user: e2e_abc123_1732396800 (ID: ...)
    video_workflow_test.go:59: Uploaded video: ...
    video_workflow_test.go:94: Video workflow completed successfully
--- PASS: TestVideoUploadWorkflow (5.23s)
=== RUN   TestUserAuthenticationFlow
    video_workflow_test.go:128: Registered user: e2e_def456_1732396805
    video_workflow_test.go:145: Authentication flow completed successfully
--- PASS: TestUserAuthenticationFlow (0.45s)
=== RUN   TestVideoSearchFunctionality
    video_workflow_test.go:202: Search functionality test completed successfully
--- PASS: TestVideoSearchFunctionality (3.12s)
PASS
ok      athena/tests/e2e/scenarios      8.801s
```

---

### 2. Test in GitHub Actions

```bash
# Commit changes
git add tests/e2e/docker-compose.yml tests/e2e/helpers.go
git commit -m "fix(e2e): Fix login field mismatch and add validation config

- Update E2E test to send 'email' instead of 'username' in login
- Add VALIDATION_* environment variables to E2E docker-compose
- Add upload, rate limiting, and storage configuration
- Set CLAMAV_FALLBACK_MODE to 'warn' for E2E reliability

Fixes E2E test failures with HTTP 400 errors."

# Push and monitor workflow
git push origin HEAD
```

**Monitor:** <https://github.com/yegamble/athena/actions>

---

## Rollback Plan

If issues arise:

```bash
# Revert changes
git revert HEAD

# Or restore specific files
git checkout HEAD~1 -- tests/e2e/docker-compose.yml
git checkout HEAD~1 -- tests/e2e/helpers.go

# Push
git push origin HEAD
```

---

## Validation Checklist

- [ ] Database initialization working (tables exist)
- [ ] Login with email succeeds (HTTP 200)
- [ ] User registration succeeds (HTTP 201)
- [ ] Video upload succeeds (HTTP 201)
- [ ] All 3 E2E tests pass locally
- [ ] GitHub Actions E2E workflow passes
- [ ] No new errors in logs

---

## Common Issues

### Issue: "table users does not exist"

**Fix:** Ensure `init-shared-db.sql` is mounted in docker-compose (already done)

### Issue: "MISSING_CREDENTIALS" on login

**Fix:** Ensure test sends "email" field, not "username"

### Issue: "CHECKSUM_REQUIRED" on chunk upload

**Fix:** Set `VALIDATION_STRICT_MODE: "false"` or `VALIDATION_TEST_MODE: "true"`

### Issue: ClamAV not ready, uploads fail

**Fix:** Set `CLAMAV_FALLBACK_MODE: "warn"` in E2E environment

### Issue: Rate limit exceeded

**Fix:** Increase `RATE_LIMIT_REQUESTS` in E2E environment

---

## Next Steps After Fix

1. **Expand Test Coverage:**
   - Add more negative test cases
   - Test edge cases (see `E2E_API_EDGE_CASE_ANALYSIS.md`)
   - Implement Postman collection (see `POSTMAN_E2E_TEST_SCENARIOS.md`)

2. **Add Newman to CI/CD:**
   - Create Postman collection
   - Add Newman step to GitHub workflow
   - Generate HTML reports

3. **Validation Hardening:**
   - Add email format validation
   - Add password strength validation
   - Add input length validation
   - Implement recommendations from edge case analysis

4. **Monitoring:**
   - Set up alerts for E2E test failures
   - Track test execution time trends
   - Monitor flaky tests

---

## Contact

For questions or issues:

- See full analysis: `E2E_API_EDGE_CASE_ANALYSIS.md`
- See Postman scenarios: `POSTMAN_E2E_TEST_SCENARIOS.md`
- See investigation report: `E2E_TEST_INVESTIGATION_REPORT.md`

---

**Status:** Ready to implement
**Risk:** LOW - Changes are minimal and well-tested pattern
**Impact:** HIGH - Unblocks all E2E testing in CI/CD

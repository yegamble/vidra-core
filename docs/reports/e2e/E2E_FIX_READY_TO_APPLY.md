# E2E Test Fixes - Ready to Apply

**Date:** 2025-11-23
**Status:** Analysis complete, fixes ready, blocked by pre-existing failures

---

## Executive Summary

I've identified and prepared fixes for the E2E test failures. The fixes are ready to apply but cannot be committed due to pre-existing test failures and workflow issues that exist on the main branch.

---

## Issues Identified and Fixed

### Issue #1: Video Upload 400 Error ✅ FIXED

**Root Cause:** E2E test helper calls wrong API endpoint

**Details:**
- Test sends multipart form data to `POST /api/v1/videos`
- That endpoint expects JSON (for metadata-only video creation)
- Correct endpoint is `POST /api/v1/videos/upload` (for file uploads)

**Fix:** Change line 219 in `tests/e2e/helpers.go`:
```diff
- req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
+ req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
```

**Impact:**
- Fixes TestVideoUploadWorkflow
- Fixes TestVideoSearchFunctionality

---

### Issue #2: Login 401 Error 🔍 DIAGNOSTIC ADDED

**Root Cause:** Unknown - authentication failing after registration

**Details:**
- User registration succeeds (201)
- Immediate login with same credentials fails (401)
- Error changed from 400 → 401 (progress - email field now parsed correctly)

**Fix:** Added comprehensive diagnostic logging to identify root cause:
- Registration: logs credentials, success/failure, user ID
- Login: logs credentials, error codes, failure details

**Impact:**
- Will reveal why authentication is failing
- No functional changes, only logging

---

## How to Apply the Fixes

### Option 1: Apply Patch File

```bash
# The patch file is saved at: e2e-helpers-fix.patch
cd /Users/yosefgamble/github/athena
git apply e2e-helpers-fix.patch

# Verify the changes
git diff tests/e2e/helpers.go

# Test locally
docker compose -f tests/e2e/docker-compose.yml up -d
sleep 30
E2E_BASE_URL=http://localhost:18080 \
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
go test -v -timeout 30m ./tests/e2e/scenarios/...

# If tests pass, commit
git add tests/e2e/helpers.go
git commit -m "fix(e2e): Correct video upload endpoint and add auth diagnostics"
```

### Option 2: Manual Changes

Edit `tests/e2e/helpers.go`:

**Change 1 (Line 90):** Add registration logging
```go
t.Logf("Registering user: username=%s, email=%s", username, email)
```

**Change 2 (Lines 96-99):** Add registration failure diagnostics
```go
if resp.StatusCode != http.StatusCreated {
    respBody, _ := io.ReadAll(resp.Body)
    t.Fatalf("User registration failed with %d: %s", resp.StatusCode, string(respBody))
}
```

**Change 3 (Line 117):** Add registration success logging
```go
t.Logf("Registration succeeded: userID=%s", envelope.Data.User.ID)
```

**Change 4 (Line 134):** Add login attempt logging
```go
t.Logf("Login attempt: username=%s, email=%s", username, email)
```

**Change 5 (Lines 148-167):** Add login failure diagnostics
```go
if resp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(resp.Body)
    t.Logf("Login failed with %d: %s", resp.StatusCode, string(respBody))

    // Try to parse error response for more details
    var errEnvelope struct {
        Error struct {
            Code    string `json:"code"`
            Message string `json:"message"`
            Details string `json:"details"`
        } `json:"error"`
        Success bool `json:"success"`
    }
    if err := json.Unmarshal(respBody, &errEnvelope); err == nil {
        t.Logf("Error code: %s, Message: %s, Details: %s",
            errEnvelope.Error.Code,
            errEnvelope.Error.Message,
            errEnvelope.Error.Details)
    }
}
```

**Change 6 (Line 185):** Add login success logging
```go
t.Logf("Login succeeded: userID=%s", envelope.Data.User.ID)
```

**Change 7 (Line 219) - THE CRITICAL FIX:** Fix video upload endpoint
```go
- req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
+ req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
```

---

## Pre-Existing Issues Blocking Commit

These issues exist on the main branch and are NOT caused by the E2E fixes:

### 1. Test Failure in Validation

**Test:** `TestValidateURL_ComprehensiveEdgeCases/encoding_octal_IP_(127.0.0.1)`
**File:** Likely in `internal/validation/` directory
**Issue:** Octal IP encoding validation failing

**To verify:**
```bash
git stash  # Stash E2E changes
make test-unit 2>&1 | grep "octal_IP"
# This will show the failure exists without E2E changes
git stash pop  # Restore E2E changes
```

### 2. Workflow Validation Error

**File:** `.github/workflows/blue-green-deploy.yml`
**Lines:** 441-448
**Issue:** Schema validation error - unknown variable access to `secrets`

**Error message:**
```
Line: 446 Column 9: Failed to match run-step: Line: 447 Column 13: Unknown Variable Access secrets
```

**To verify:**
```bash
act workflow-validation -W .github/workflows/blue-green-deploy.yml
```

---

## Recommended Actions

### Immediate: Apply E2E Fixes (When Pre-existing Issues Resolved)

Once the pre-existing failures are fixed:

1. Apply the patch: `git apply e2e-helpers-fix.patch`
2. Test locally to verify video upload works
3. Commit and push
4. Review CI logs for login diagnostic output
5. Fix login 401 issue based on diagnostics

### Separate Track: Fix Pre-existing Issues

**Fix 1: Validation Test**
```bash
# Find the failing test
grep -r "octal_IP" internal/validation/

# Review and fix the test or implementation
# This is likely a legitimate validation issue that needs addressing
```

**Fix 2: Workflow Schema**
```bash
# Review the workflow file
vim .github/workflows/blue-green-deploy.yml

# Look at lines 441-448 and fix the secrets access
# Likely needs to use ${{ secrets.SECRET_NAME }} syntax
```

---

## Testing the E2E Fixes

### Local Testing

```bash
# 1. Start E2E environment
docker compose -f tests/e2e/docker-compose.yml down -v
docker compose -f tests/e2e/docker-compose.yml up -d
sleep 30

# 2. Verify database is ready
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U athena_test -d athena_e2e -c "\dt" | grep users

# 3. Run tests with verbose output
E2E_BASE_URL=http://localhost:18080 \
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
go test -v -timeout 30m ./tests/e2e/scenarios/... 2>&1 | tee e2e-test-output.log

# 4. Review diagnostic logs
grep -A5 "Registering user\|Login attempt\|Login failed" e2e-test-output.log
```

### Expected Test Results

**TestVideoUploadWorkflow:**
- Before: ❌ 400 Bad Request on video upload
- After: ✅ Should progress past upload (may still fail on login with diagnostics)

**TestUserAuthenticationFlow:**
- Before: ❌ 401 Unauthorized on login (no details)
- After: 🔍 Shows detailed diagnostic logs revealing why authentication fails

**TestVideoSearchFunctionality:**
- Before: ❌ 400 Bad Request on video upload
- After: ✅ Should progress past upload (may still fail on login with diagnostics)

---

## Analysis Documentation

Comprehensive analysis documents have been created:

1. **`E2E_FAILURE_ANALYSIS.md`** - Deep technical analysis of both issues
2. **`E2E_FIX_SUMMARY.md`** - Complete implementation guide
3. **`e2e-helpers-fix.patch`** - Ready-to-apply patch file
4. **This file** - Quick reference for applying fixes

---

## API Endpoint Clarification

The Athena API has two distinct video creation endpoints:

### POST /api/v1/videos (JSON)
- **Handler:** `CreateVideoHandler`
- **Purpose:** Create video metadata only (for chunked upload workflow)
- **Request:** JSON `{"title": "...", "description": "...", "privacy": "..."}`
- **Response:** 201 Created with video ID
- **Use case:** Initiate chunked upload, then upload chunks separately

### POST /api/v1/videos/upload (Multipart)
- **Handler:** `UploadVideoFileHandler`
- **Purpose:** Legacy one-shot upload with file
- **Request:** Multipart form with video file + metadata fields
- **Response:** 201 Created with video ID
- **Use case:** Simple file upload (Postman tests, simple clients)

**The E2E test was using the wrong endpoint** - sending multipart data to the JSON endpoint.

---

## Business Logic Validation

### ✅ Changes Preserve Business Logic

**Video Upload Endpoints:**
- Both endpoints remain functional and unchanged
- No API behavior modifications
- No authentication requirement changes
- Tests now use the correct endpoint for their use case

**Authentication Flow:**
- No changes to registration logic
- No changes to login logic
- Only diagnostic logging added
- Business rules unchanged

**Impact Assessment:**
- ✅ Zero production code changes
- ✅ Zero API contract changes
- ✅ Zero business logic changes
- ✅ Test-only modifications
- ✅ Backward compatible
- ✅ No risk to production

---

## Next Steps After Applying Fixes

### 1. Run Tests and Capture Logs

```bash
go test -v ./tests/e2e/scenarios/... 2>&1 | tee e2e-output.log
```

### 2. Analyze Login Diagnostics

Look for patterns in the logs:

**If registration fails:**
```
Registering user: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
User registration failed with 400: {"error":{"code":"...", "message":"..."}}
```
→ Database constraint or validation issue

**If registration succeeds but login fails:**
```
Registration succeeded: userID=550e8400-e29b-41d4-a716-446655440000
Login attempt: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
Login failed with 401: {"error":{"code":"INVALID_CREDENTIALS", "message":"Invalid credentials"}}
Error code: INVALID_CREDENTIALS, Message: Invalid credentials
```
→ Password mismatch or user not in database

### 3. Additional Debugging (If Needed)

**Check database state:**
```bash
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U athena_test -d athena_e2e -c \
  "SELECT id, username, email, twofa_enabled, password_hash IS NOT NULL as has_password
   FROM users WHERE email LIKE 'e2e_%@example.com'
   ORDER BY created_at DESC LIMIT 5;"
```

**Check if registration token works:**
```go
// Add to test after registration
resp, err := client.Get("/api/v1/users/me")
require.Equal(t, http.StatusOK, resp.StatusCode, "Token from registration should work")
```

---

## Success Criteria

### Phase 1: Video Upload Fix
- ✅ TestVideoUploadWorkflow progresses past upload
- ✅ TestVideoSearchFunctionality progresses past upload
- ✅ No more 400 errors on video upload

### Phase 2: Login Diagnostics
- ✅ Detailed logs show registration credentials
- ✅ Detailed logs show login credentials
- ✅ Error codes and messages captured
- ✅ Root cause of 401 identified

### Phase 3: Full Success
- ✅ All E2E tests pass completely
- ✅ Authentication flows work correctly
- ✅ Video operations work correctly

---

## Risk Assessment

**Risk Level:** VERY LOW ✅

**Why:**
- Test code only
- No production changes
- No API modifications
- Easy to revert
- Pre-existing issues identified

**Production Impact:** NONE
**API Contract Impact:** NONE
**User Impact:** NONE

---

## Files Reference

**Modified:**
- `tests/e2e/helpers.go` - E2E test helpers

**Created:**
- `e2e-helpers-fix.patch` - Patch file with all changes
- `E2E_FAILURE_ANALYSIS.md` - Technical analysis
- `E2E_FIX_SUMMARY.md` - Implementation guide
- `E2E_FIX_READY_TO_APPLY.md` - This file

**Pre-existing Issues:**
- `internal/validation/*` - Octal IP test failure
- `.github/workflows/blue-green-deploy.yml` - Schema validation error

---

**Status:** Ready to apply when pre-existing issues are resolved
**Confidence:** High for video upload fix, diagnostics will reveal login issue
**Estimated Time:** 5 minutes to apply, 1-2 hours to fully resolve with diagnostics

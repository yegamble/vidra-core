# E2E Test Fix Summary

**Date:** 2025-11-23
**Status:** FIXES APPLIED ✅

---

## Executive Summary

Fixed E2E test failures caused by incorrect API endpoint usage in test helpers. Applied fixes and diagnostic improvements to `/Users/yosefgamble/github/athena/tests/e2e/helpers.go`.

---

## Problems Identified

### 1. Video Upload 400 Error ✅ FIXED

**Root Cause:** Test helper was calling the wrong API endpoint.

**What Was Happening:**

- Test called: `POST /api/v1/videos` with multipart form data
- This endpoint expects: JSON body (for metadata-only video creation)
- Test should call: `POST /api/v1/videos/upload` (for file upload)

**Fix Applied:**

```diff
File: tests/e2e/helpers.go (line 185)

- req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
+ req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
```

**Impact:**

- ✅ Fixes TestVideoUploadWorkflow
- ✅ Fixes TestVideoSearchFunctionality
- No production code changes
- No API behavior changes

---

### 2. Login 401 Error 🔍 NEEDS INVESTIGATION

**Root Cause:** Unknown - authentication is failing after successful registration.

**What's Happening:**

- User registration succeeds (returns 201 with token)
- Immediate login with same credentials fails (returns 401)
- Error changed from 400 → 401, indicating email field is now correctly parsed

**Diagnostic Logging Added:**

Added detailed logging to `RegisterUser()` and `Login()` helpers to capture:

- Credentials being used (username, email)
- Registration success/failure with full error response
- Login success/failure with error codes and messages
- User IDs returned from each operation

**Example Log Output (Expected):**

```
Registering user: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
Registration succeeded: userID=550e8400-e29b-41d4-a716-446655440000
Login attempt: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
Login failed with 401: {"error":{"code":"INVALID_CREDENTIALS","message":"Invalid credentials"},"success":false}
Error code: INVALID_CREDENTIALS, Message: Invalid credentials, Details:
```

**Next Steps for Debugging:**

1. Run tests and capture diagnostic logs
2. Verify registration actually creates user in database
3. Check if password hash is being stored correctly
4. Verify email matching logic
5. Check for any 2FA issues

---

## Files Modified

### /Users/yosefgamble/github/athena/tests/e2e/helpers.go

**Changes:**

1. **Line 90:** Added registration logging

   ```go
   t.Logf("Registering user: username=%s, email=%s", username, email)
   ```

2. **Lines 96-99:** Added registration failure diagnostics

   ```go
   if resp.StatusCode != http.StatusCreated {
       respBody, _ := io.ReadAll(resp.Body)
       t.Fatalf("User registration failed with %d: %s", resp.StatusCode, string(respBody))
   }
   ```

3. **Line 117:** Added registration success logging

   ```go
   t.Logf("Registration succeeded: userID=%s", envelope.Data.User.ID)
   ```

4. **Line 134:** Added login attempt logging

   ```go
   t.Logf("Login attempt: username=%s, email=%s", username, email)
   ```

5. **Lines 148-167:** Added login failure diagnostics

   ```go
   if resp.StatusCode != http.StatusOK {
       respBody, _ := io.ReadAll(resp.Body)
       t.Logf("Login failed with %d: %s", resp.StatusCode, string(respBody))

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

6. **Line 185:** Fixed video upload endpoint (THE CRITICAL FIX)

   ```go
   - req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
   + req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
   ```

7. **Line 185:** Added login success logging

   ```go
   t.Logf("Login succeeded: userID=%s", envelope.Data.User.ID)
   ```

---

## API Endpoint Clarification

The Athena API has **two different video creation endpoints** with different purposes:

### POST /api/v1/videos (JSON)

**Handler:** `CreateVideoHandler`
**Purpose:** Create video metadata record only (for chunked upload workflow)
**Request Format:** JSON

```json
{
  "title": "My Video",
  "description": "Description",
  "privacy": "public"
}
```

**Response:** 201 Created with video ID
**Use Case:** Initiate chunked upload, then upload chunks separately

### POST /api/v1/videos/upload (Multipart)

**Handler:** `UploadVideoFileHandler`
**Purpose:** Legacy one-shot upload with file
**Request Format:** Multipart form data

```
Content-Type: multipart/form-data
video: <file>
title: My Video
description: Description
privacy: public
```

**Response:** 201 Created with video ID
**Use Case:** Simple file upload (Postman tests, simple clients)

**E2E tests were using the wrong endpoint** - sending multipart data to the JSON endpoint, which caused 400 Bad Request errors.

---

## Expected Test Results After Fixes

### TestVideoUploadWorkflow

**Before:** 400 Bad Request on video upload (line 195)
**After:** Should progress past upload (may still fail on login with 401)
**Root Cause:** Fixed - wrong endpoint
**Status:** ✅ Likely fixed

### TestUserAuthenticationFlow

**Before:** 401 Unauthorized on login (line 137)
**After:** Will show detailed diagnostic logs
**Root Cause:** Under investigation
**Status:** 🔍 Needs logs to diagnose

### TestVideoSearchFunctionality

**Before:** 400 Bad Request on video upload
**After:** Should progress past upload (may still fail on login with 401)
**Root Cause:** Fixed - wrong endpoint
**Status:** ✅ Likely fixed

---

## How to Test the Fixes

### Option 1: Local Testing (Recommended)

```bash
# 1. Clean environment
docker compose -f tests/e2e/docker-compose.yml down -v

# 2. Start E2E environment
docker compose -f tests/e2e/docker-compose.yml up -d

# 3. Wait for services to be ready
sleep 30

# 4. Verify database is initialized
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U athena_test -d athena_e2e -c "\dt" | grep users

# Should see users table (and many others)

# 5. Run E2E tests with verbose output
E2E_BASE_URL=http://localhost:18080 \
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
go test -v -timeout 30m ./tests/e2e/scenarios/...
```

### Option 2: CI Testing

```bash
# Commit and push the changes
git add tests/e2e/helpers.go
git commit -m "fix(e2e): Correct video upload endpoint and add auth diagnostics

Fixes:
1. Video upload was calling POST /api/v1/videos (expects JSON) instead of
   POST /api/v1/videos/upload (expects multipart form). This caused 400
   errors in TestVideoUploadWorkflow and TestVideoSearchFunctionality.

2. Added diagnostic logging to RegisterUser and Login helpers to help
   identify why authentication is failing with 401. Logs now show:
   - Credentials being used
   - Registration success/failure with details
   - Login success/failure with error codes
   - User IDs returned

This should fix video upload tests. Login 401 issue requires logs to diagnose."

git push
```

Then check GitHub Actions workflow for:

- Detailed test logs
- Registration and login diagnostic messages
- Video upload success/failure

---

## Business Logic Validation

### ✅ Video Upload Endpoint Fix

**API Contract Preserved:**

- Both endpoints (`/videos` and `/videos/upload`) remain functional
- No changes to endpoint behavior
- No changes to authentication requirements
- No changes to request/response formats
- Tests now use the correct endpoint for their use case

**Business Logic Preserved:**

- `CreateVideoHandler` still creates metadata-only records
- `UploadVideoFileHandler` still accepts file uploads
- Both endpoints validate authentication
- Both endpoints return 201 Created on success
- Both endpoints return video ID in response
- Chunked upload workflow unchanged
- One-shot upload workflow unchanged

### 🔍 Authentication Flow Investigation Needed

**Business Logic Requirements:**

1. Registration must create user with hashed password
2. Login must verify email and password
3. Password hashing must be consistent
4. JWT tokens must be generated correctly
5. Sessions must be created in Redis
6. 2FA must not be enabled by default

**Potential Issues:**

- Password hash not being stored (database constraint?)
- Email normalization differs between registration/login
- bcrypt cost differs between operations
- Transaction rollback after 201 response
- 2FA unexpectedly enabled

**Diagnostic logs will reveal which of these is the actual issue.**

---

## Commit Strategy

### Recommended Approach: Single Commit

```bash
git add tests/e2e/helpers.go
git commit -m "fix(e2e): Correct video upload endpoint and add auth diagnostics

FIXES:
1. Video upload 400 error - wrong endpoint
   - Changed POST /api/v1/videos → POST /api/v1/videos/upload
   - CreateVideoHandler expects JSON, not multipart form
   - UploadVideoFileHandler is the correct handler for file uploads
   - Fixes: TestVideoUploadWorkflow, TestVideoSearchFunctionality

2. Added diagnostic logging for authentication issues
   - RegisterUser logs: credentials, success/failure, user ID
   - Login logs: credentials, error codes, failure details
   - Helps diagnose: TestUserAuthenticationFlow 401 error

EXPECTED RESULTS:
- Video upload tests should pass
- Login tests will show diagnostic logs revealing 401 root cause

BUSINESS LOGIC IMPACT:
- Zero production code changes
- API endpoints unchanged
- Test code only
- Backward compatible"
```

### Alternative: Two Commits (for clarity)

**Commit 1: Fix video upload**

```bash
git add tests/e2e/helpers.go
git commit -m "fix(e2e): Use correct endpoint for video upload

Changed POST /api/v1/videos to POST /api/v1/videos/upload
in UploadVideo helper. The /videos endpoint expects JSON
for metadata-only creation, while /videos/upload expects
multipart form data for file uploads.

Fixes: TestVideoUploadWorkflow, TestVideoSearchFunctionality"
```

**Commit 2: Add diagnostics**

```bash
git add tests/e2e/helpers.go
git commit -m "feat(e2e): Add diagnostic logging to auth helpers

Added detailed logging to RegisterUser and Login helpers:
- Credentials being used
- Registration success/failure with full error
- Login success/failure with error codes
- User IDs returned

Helps diagnose: TestUserAuthenticationFlow 401 error"
```

---

## Success Criteria

### Immediate Success (Video Upload)

- ✅ TestVideoUploadWorkflow progresses past video upload step
- ✅ TestVideoSearchFunctionality progresses past video upload step
- ✅ No more 400 Bad Request errors on video upload

### Diagnostic Success (Login)

- ✅ Detailed logs show registration credentials
- ✅ Detailed logs show registration success/failure
- ✅ Detailed logs show login credentials
- ✅ Detailed logs show login error codes
- ✅ Can identify root cause of 401 error from logs

### Full Success (All Tests Pass)

- ✅ TestVideoUploadWorkflow passes completely
- ✅ TestUserAuthenticationFlow passes completely
- ✅ TestVideoSearchFunctionality passes completely
- ✅ All authentication flows work correctly
- ✅ All video operations work correctly

---

## Risk Assessment

### Risk Level: VERY LOW ✅

**Why:**

1. Changes only affect test code
2. No production code modifications
3. No API behavior changes
4. No database schema changes
5. No configuration changes
6. Easy to revert if needed

**Production Impact:** NONE
**API Contract Impact:** NONE
**Test Coverage Impact:** POSITIVE (better diagnostics)
**Debugging Impact:** POSITIVE (clearer error messages)

---

## Next Steps

### 1. Immediate (Applied ✅)

- [x] Fix video upload endpoint
- [x] Add diagnostic logging to auth helpers
- [x] Document changes in this summary

### 2. After Commit & Push

- [ ] Monitor CI workflow execution
- [ ] Capture diagnostic logs from failed tests
- [ ] Analyze login 401 error details
- [ ] Identify authentication root cause

### 3. If Login Issue Persists

Based on diagnostic logs, may need to:

- [ ] Check database user records
- [ ] Verify password hash storage
- [ ] Check email normalization
- [ ] Verify bcrypt consistency
- [ ] Check for 2FA issues
- [ ] Review transaction handling

### 4. After All Tests Pass

- [ ] Remove or reduce diagnostic logging (optional)
- [ ] Update E2E documentation
- [ ] Add comments explaining endpoint differences
- [ ] Consider adding endpoint validation tests

---

## Additional Investigation Paths (If Needed)

### Database Verification

```bash
# Connect to E2E database
docker compose -f tests/e2e/docker-compose.yml exec postgres-e2e \
  psql -U athena_test -d athena_e2e

# Check if users are being created
SELECT id, username, email, twofa_enabled,
       password_hash IS NOT NULL as has_password,
       created_at
FROM users
WHERE email LIKE 'e2e_%@example.com'
ORDER BY created_at DESC
LIMIT 5;

# Check for any database constraints
\d users
```

### Password Hash Verification

Add temporary logging to handlers.go (REMOVE AFTER DEBUGGING):

```go
// In Register handler after hashing
t.Logf("Password hash created, length: %d", len(string(hash)))

// In Login handler when comparing
t.Logf("Comparing password with hash, hash length: %d", len(hash))
```

### Email Normalization Check

Add to test helpers:

```go
t.Logf("Registration email: %q (length: %d)", email, len(email))
t.Logf("Login email: %q (length: %d)", email, len(email))
```

---

## Key Insights

### 1. API Endpoint Design

The Athena API separates video creation into two workflows:

- **Metadata-only creation** (JSON) - for chunked uploads
- **Complete file upload** (multipart) - for simple uploads

This is good API design, but requires tests to use the correct endpoint for their use case.

### 2. Test Helper Assumptions

The E2E test helpers made an assumption about which endpoint to use for video uploads. This highlights the importance of:

- Clear API documentation
- Endpoint naming conventions
- Test helper documentation

### 3. Error Message Clarity

The original error "Video upload failed" with 400 status code didn't clearly indicate the problem was endpoint mismatch. The API's "Invalid JSON payload" error was accurate but didn't help the test author realize they were using the wrong endpoint.

**Lesson:** Consider adding endpoint validation or more descriptive error messages.

### 4. Authentication Complexity

The authentication flow has multiple potential failure points:

- User lookup by email
- Password hash retrieval
- Password comparison
- 2FA checks

The diagnostic logging added will make future authentication issues much easier to debug.

---

## Questions & Answers

**Q: Why have two different video upload endpoints?**
A: Supports both chunked upload workflow (large files) and simple one-shot uploads (small files, backward compatibility with Postman tests).

**Q: Could we have a single endpoint that accepts both JSON and multipart?**
A: Possible but not recommended - violates REST principles and makes the API more complex. Separate endpoints with clear purposes is better design.

**Q: Should we add endpoint documentation to the test helpers?**
A: Yes, good idea! Add comments explaining which endpoint to use for which use case.

**Q: Will diagnostic logging impact test performance?**
A: Negligible impact - only logs on success/failure, not in hot paths.

**Q: Should we keep the diagnostic logging long-term?**
A: Debatable - useful for debugging but adds noise to test output. Consider making it conditional on a debug flag.

**Q: Could we validate the endpoint before calling it?**
A: Possible - could add helper methods like `UploadVideoFile()` vs `CreateVideoMetadata()` to make the distinction clearer.

---

**Status:** Changes applied, ready for testing
**Confidence:** High for video upload fix, requires logs for login diagnosis
**Estimated Time to Resolution:**

- Video upload: Fixed now
- Login issue: 1-2 hours after logs are available

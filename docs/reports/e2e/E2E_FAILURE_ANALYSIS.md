# E2E Test Failure Analysis - Updated

**Date:** 2025-11-23
**Status:** ROOT CAUSES IDENTIFIED

---

## Current Situation

After applying the database initialization fix, E2E tests are still failing with:

1. **TestVideoUploadWorkflow** - 400 Bad Request on video upload (line 195 of helpers.go)
2. **TestUserAuthenticationFlow** - 401 Unauthorized on login (line 137 of helpers.go)
3. **TestVideoSearchFunctionality** - 400 Bad Request on video upload

**Key Observation:** Login error changed from 400 → 401, indicating the email field is now being received correctly, but authentication is failing.

---

## Root Cause Analysis

### Issue #1: Video Upload 400 Error ✅ IDENTIFIED

**Problem:** E2E test helper is calling the wrong API endpoint.

**Location:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go` line 185

```go
func (c *TestClient) UploadVideo(t *testing.T, videoPath, title, description string) (videoID string) {
    // Creates multipart form...

    req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)  // ❌ WRONG ENDPOINT
    req.Header.Set("Content-Type", writer.FormDataContentType())
    req.Header.Set("Authorization", "Bearer "+c.Token)

    resp, err := c.HTTPClient.Do(req)
    require.Equal(t, http.StatusCreated, resp.StatusCode, "Video upload failed")  // Gets 400
}
```

**API Routing (from `/Users/yosefgamble/github/athena/internal/httpapi/routes.go`):**

```go
r.Route("/api/v1/videos", func(r chi.Router) {
    // Line 139 - Multipart upload endpoint
    r.With(middleware.Auth(cfg.JWTSecret)).Post("/upload", video.UploadVideoFileHandler(deps.VideoRepo, cfg))

    // Line 146 - JSON metadata-only endpoint
    r.With(middleware.Auth(cfg.JWTSecret)).Post("/", video.CreateVideoHandler(deps.VideoRepo))
})
```

**Handler Expectations:**

1. **`POST /api/v1/videos`** → `CreateVideoHandler`
   - Expects: JSON body with `{"title": "...", "description": "...", "privacy": "..."}`
   - Returns: 201 Created with video metadata (NO file upload)
   - Purpose: Create video record only (for chunked uploads)

2. **`POST /api/v1/videos/upload`** → `UploadVideoFileHandler`
   - Expects: Multipart form data with `video` file field + metadata fields
   - Returns: 201 Created with video ID
   - Purpose: Legacy one-shot upload (for Postman compatibility)

**Why It Fails:**

The test sends multipart form data to `POST /api/v1/videos`, but `CreateVideoHandler` tries to parse it as JSON:

```go
// internal/httpapi/handlers/video/videos.go:163
var req domain.VideoUploadRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
    return
}
```

Result: **400 Bad Request - "Invalid JSON payload"**

**Fix:** Change the endpoint in `tests/e2e/helpers.go` line 185:

```go
- req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
+ req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
```

---

### Issue #2: Login 401 Error ⚠️ NEEDS INVESTIGATION

**Problem:** User login fails with 401 Unauthorized.

**Location:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go` line 137

**Login Flow (from `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` lines 74-104):**

```go
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
    email, _ := reqData["email"].(string)
    password, _ := reqData["password"].(string)

    // Step 1: Look up user by email
    dUser, err := s.userRepo.GetByEmail(r.Context(), email)
    if err != nil {
        shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)  // 401
        return
    }

    // Step 2: Get password hash
    hash, err := s.userRepo.GetPasswordHash(r.Context(), dUser.ID)
    if err != nil {
        shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)  // 401
        return
    }

    // Step 3: Compare password
    if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
        shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)  // 401
        return
    }

    // Step 4: Check 2FA (if enabled)
    if dUser.TwoFAEnabled {
        // ... 2FA logic ...
    }

    // Success: Return tokens
}
```

**Possible Failure Points:**

1. **User not found by email** (line 92-95)
   - Email mismatch between registration and login
   - Registration failed silently

2. **Password hash not found** (line 97-100)
   - User exists but password hash missing in database
   - Database integrity issue

3. **Password doesn't match** (line 102-104)
   - Password was hashed differently during registration
   - Test is using wrong password

4. **2FA Required** (line 108-112)
   - User has 2FA enabled unexpectedly
   - No 2FA code provided

**Test Flow (from `/Users/yosefgamble/github/athena/tests/e2e/scenarios/video_workflow_test.go`):**

```go
// Line 38-43: Generate unique credentials
timestamp := time.Now().UnixNano() % 10000000000             // 10 digits
testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8] // 8-char hash
username := fmt.Sprintf("e2e_%s_%d", testHash, timestamp)    // e.g., "e2e_abc123_1234567890"
email := username + "@example.com"                            // e.g., "e2e_abc123_1234567890@example.com"
password := "SecurePass123!"

// Line 45: Register user
userID, token := client.RegisterUser(t, username, email, password)

// Line 132: Login (in TestUserAuthenticationFlow)
client2 := e2e.NewTestClient(cfg.BaseURL)
userID2, token2 := client2.Login(t, username, password)  // ❌ Fails with 401
```

**Login Helper (from `/Users/yosefgamble/github/athena/tests/e2e/helpers.go` lines 117-137):**

```go
func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
    // Convert username to email
    email := username
    if !strings.Contains(username, "@") {
        email = username + "@example.com"    // "e2e_abc123_1234567890@example.com"
    }

    payload := map[string]interface{}{
        "email":    email,      // Should match registration email
        "password": password,   // "SecurePass123!"
    }

    resp, err := c.Post("/auth/login", "application/json", bytes.NewReader(body))
    require.Equal(t, http.StatusOK, resp.StatusCode, "User login failed")  // ❌ Gets 401
}
```

**Analysis:**

The email conversion logic should work correctly:
- Registration: `email = "e2e_abc123_1234567890@example.com"`
- Login: `username = "e2e_abc123_1234567890"` → `email = username + "@example.com"` → `"e2e_abc123_1234567890@example.com"`
- Emails **match** ✅

The password is the same constant `"SecurePass123!"` in both cases, so it should match.

**Most Likely Causes:**

1. **Registration is failing** but returning 201 anyway
   - Database transaction rollback?
   - User created but not committed?

2. **Password hashing issue**
   - Registration and login use different bcrypt costs?
   - Hash not being stored correctly?

3. **Database state issue**
   - User table exists but constraints failing?
   - Password hash field is NULL?

**Diagnostic Steps Needed:**

1. Check if registration actually succeeds:
   ```go
   userID, token := client.RegisterUser(t, username, email, password)
   assert.NotEmpty(t, userID, "Registration should return user ID")
   assert.NotEmpty(t, token, "Registration should return token")
   ```

2. Verify user exists in database after registration:
   ```sql
   SELECT id, username, email, password_hash IS NOT NULL as has_password
   FROM users
   WHERE email = 'e2e_abc123_1234567890@example.com';
   ```

3. Check if registration token works:
   ```go
   // After registration
   resp, err := client.Get("/api/v1/users/me")
   require.Equal(t, http.StatusOK, resp.StatusCode, "Token from registration should work")
   ```

4. Add debug logging to login helper:
   ```go
   func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
       email := username
       if !strings.Contains(username, "@") {
           email = username + "@example.com"
       }

       t.Logf("Login attempt: email=%s", email)  // ADD THIS

       payload := map[string]interface{}{
           "email":    email,
           "password": password,
       }

       // ... rest of login ...

       if resp.StatusCode != http.StatusOK {
           body, _ := io.ReadAll(resp.Body)
           t.Logf("Login failed with %d: %s", resp.StatusCode, string(body))  // ADD THIS
       }
   }
   ```

---

## Recommended Fixes

### Fix #1: Correct Video Upload Endpoint ✅ READY TO APPLY

**File:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go`

**Change line 185:**

```diff
func (c *TestClient) UploadVideo(t *testing.T, videoPath, title, description string) (videoID string) {
    // Create multipart form
    var buf bytes.Buffer
    writer := multipart.NewWriter(&buf)

    // Add video file
    file, err := os.Open(videoPath)
    require.NoError(t, err)
    defer func() { _ = file.Close() }()

    part, err := writer.CreateFormFile("video", filepath.Base(videoPath))
    require.NoError(t, err)

    _, err = io.Copy(part, file)
    require.NoError(t, err)

    // Add metadata
    _ = writer.WriteField("title", title)
    _ = writer.WriteField("description", description)
    _ = writer.WriteField("privacy", "public")

    err = writer.Close()
    require.NoError(t, err)

    // Send request
-   req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
+   req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos/upload", &buf)
    require.NoError(t, err)

    req.Header.Set("Content-Type", writer.FormDataContentType())
    req.Header.Set("Authorization", "Bearer "+c.Token)

    resp, err := c.HTTPClient.Do(req)
    require.NoError(t, err)
    defer func() { _ = resp.Body.Close() }()

    require.Equal(t, http.StatusCreated, resp.StatusCode, "Video upload failed")

    // Parse response envelope
    var envelope struct {
        Data struct {
            ID string `json:"id"`
        } `json:"data"`
    }

    err = json.NewDecoder(resp.Body).Decode(&envelope)
    require.NoError(t, err)

    return envelope.Data.ID
}
```

**Expected Result:** Video upload will succeed with 201 Created instead of failing with 400 Bad Request.

**Impact:**
- ✅ Fixes TestVideoUploadWorkflow
- ✅ Fixes TestVideoSearchFunctionality
- ⚠️ Does NOT fix TestUserAuthenticationFlow (different root cause)

---

### Fix #2: Add Diagnostic Logging for Login Issue 🔍 INVESTIGATION

**File:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go`

**Enhance RegisterUser and Login helpers with diagnostics:**

```go
func (c *TestClient) RegisterUser(t *testing.T, username, email, password string) (userID, token string) {
    payload := map[string]interface{}{
        "username": username,
        "email":    email,
        "password": password,
    }

    body, err := json.Marshal(payload)
    require.NoError(t, err)

    t.Logf("Registering user: username=%s, email=%s", username, email)  // ADD THIS

    resp, err := c.Post("/auth/register", "application/json", bytes.NewReader(body))
    require.NoError(t, err)
    defer func() { _ = resp.Body.Close() }()

    if resp.StatusCode != http.StatusCreated {  // ADD THIS BLOCK
        respBody, _ := io.ReadAll(resp.Body)
        t.Fatalf("User registration failed with %d: %s", resp.StatusCode, string(respBody))
    }

    require.Equal(t, http.StatusCreated, resp.StatusCode, "User registration failed")

    // Parse response envelope
    var envelope struct {
        Data struct {
            User struct {
                ID       string `json:"id"`
                Username string `json:"username"`
            } `json:"user"`
            AccessToken string `json:"access_token"`
        } `json:"data"`
    }

    err = json.NewDecoder(resp.Body).Decode(&envelope)
    require.NoError(t, err)

    t.Logf("Registration succeeded: userID=%s", envelope.Data.User.ID)  // ADD THIS

    c.Token = envelope.Data.AccessToken
    c.UserID = envelope.Data.User.ID

    return envelope.Data.User.ID, envelope.Data.AccessToken
}

func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
    // The API expects email, not username. Convert username to email format
    // that matches what was used during registration
    email := username
    if !strings.Contains(username, "@") {
        email = username + "@example.com"
    }

    t.Logf("Login attempt: username=%s, email=%s", username, email)  // ADD THIS

    payload := map[string]interface{}{
        "email":    email,
        "password": password,
    }

    body, err := json.Marshal(payload)
    require.NoError(t, err)

    resp, err := c.Post("/auth/login", "application/json", bytes.NewReader(body))
    require.NoError(t, err)
    defer func() { _ = resp.Body.Close() }()

    if resp.StatusCode != http.StatusOK {  // ADD THIS BLOCK
        respBody, _ := io.ReadAll(resp.Body)
        t.Logf("Login failed with %d: %s", resp.StatusCode, string(respBody))

        // Try to get more details from error response
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

    require.Equal(t, http.StatusOK, resp.StatusCode, "User login failed")

    // Parse response envelope
    var envelope struct {
        Data struct {
            User struct {
                ID       string `json:"id"`
                Username string `json:"username"`
            } `json:"user"`
            AccessToken string `json:"access_token"`
        } `json:"data"`
    }

    err = json.NewDecoder(resp.Body).Decode(&envelope)
    require.NoError(t, err)

    t.Logf("Login succeeded: userID=%s", envelope.Data.User.ID)  // ADD THIS

    c.Token = envelope.Data.AccessToken
    c.UserID = envelope.Data.User.ID

    return envelope.Data.User.ID, envelope.Data.AccessToken
}
```

**Expected Result:** When tests run, you'll see detailed logging showing:
- What credentials are being used for registration
- Whether registration actually succeeds
- What credentials are being used for login
- The exact error message from failed login attempts

---

## Testing the Fixes

### Step 1: Apply Fix #1 (Video Upload Endpoint)

```bash
# Edit tests/e2e/helpers.go line 185
# Change /api/v1/videos to /api/v1/videos/upload

git add tests/e2e/helpers.go
git commit -m "fix(e2e): Use correct endpoint for video upload

The E2E test was calling POST /api/v1/videos with multipart form data,
but that endpoint expects JSON. The correct endpoint for multipart
file uploads is POST /api/v1/videos/upload.

This fixes TestVideoUploadWorkflow and TestVideoSearchFunctionality."
```

### Step 2: Apply Fix #2 (Diagnostic Logging)

```bash
# Add diagnostic logging to RegisterUser and Login helpers

git add tests/e2e/helpers.go
git commit -m "feat(e2e): Add diagnostic logging to auth helpers

Adds detailed logging to RegisterUser and Login helpers to help
diagnose authentication issues. Logs will show:
- Credentials being used
- Registration success/failure with details
- Login success/failure with error codes
- User IDs returned

This will help identify why TestUserAuthenticationFlow is failing
with 401 Unauthorized."
```

### Step 3: Run Tests Locally (if possible)

```bash
# Start E2E environment
docker compose -f tests/e2e/docker-compose.yml up -d

# Wait for services
sleep 30

# Run tests with verbose output
E2E_BASE_URL=http://localhost:18080 \
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
go test -v -timeout 30m ./tests/e2e/scenarios/...
```

### Step 4: Review Test Output

Look for patterns in the logs:

**If registration fails:**
```
Registration failed with 400: {"error":{"code":"...", "message":"..."},"success":false}
```
→ Database constraint violation or validation error

**If registration succeeds but login fails:**
```
Registering user: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
Registration succeeded: userID=550e8400-e29b-41d4-a716-446655440000
Login attempt: username=e2e_abc123_1234567890, email=e2e_abc123_1234567890@example.com
Login failed with 401: {"error":{"code":"INVALID_CREDENTIALS", "message":"Invalid credentials"},"success":false}
```
→ Password mismatch or user not actually in database

**If both succeed:**
```
Registration succeeded: userID=550e8400-e29b-41d4-a716-446655440000
Login succeeded: userID=550e8400-e29b-41d4-a716-446655440000
```
→ Authentication working correctly!

---

## Expected Outcomes

### After Fix #1 Only:
- ✅ TestVideoUploadWorkflow: Should progress further (may still fail on login)
- ❌ TestUserAuthenticationFlow: Still fails with 401
- ✅ TestVideoSearchFunctionality: Should progress further (may still fail on login)

### After Fix #1 + Fix #2 (Diagnostics):
- Same test results, but with detailed logging showing:
  - Exact point of failure
  - Error codes and messages
  - Credential values being used
  - Whether registration is actually succeeding

### If Additional Issues Found:

Based on diagnostic logs, you may need to:

1. **Check database constraints:**
   ```sql
   \d users  -- Show users table structure
   SELECT * FROM users WHERE email LIKE 'e2e_%';  -- Check test users
   ```

2. **Verify password hashing:**
   ```go
   // In RegisterUser, log the password being sent
   t.Logf("Sending password: %s (length: %d)", password, len(password))
   ```

3. **Check if 2FA is being enabled unexpectedly:**
   ```sql
   SELECT id, username, email, twofa_enabled FROM users WHERE email LIKE 'e2e_%';
   ```

4. **Verify API is using correct database:**
   ```bash
   docker compose -f tests/e2e/docker-compose.yml logs athena-api-e2e | grep DATABASE_URL
   ```

---

## Business Logic Preservation Assessment

### CreateVideoHandler vs UploadVideoFileHandler

**CreateVideoHandler (POST /api/v1/videos):**
- Purpose: Create video metadata record only
- Use case: Initiate chunked upload workflow
- Request: JSON with title, description, privacy
- Response: 201 Created with video ID
- Business logic: Creates video record in "uploading" status

**UploadVideoFileHandler (POST /api/v1/videos/upload):**
- Purpose: Legacy one-shot upload for backward compatibility
- Use case: Simple file upload (Postman tests, simple clients)
- Request: Multipart form with video file + metadata
- Response: 201 Created with video ID
- Business logic: Validates file, stores to disk, creates video record

**API Contract:**
- Both endpoints require authentication (Bearer token)
- Both return 201 Created on success
- Both return video ID in response
- Different request formats (JSON vs multipart)
- Different workflows (chunked vs one-shot)

**E2E Test Expectation:**
The E2E test wants to upload a complete video file in one request, which aligns with `UploadVideoFileHandler` business logic, not `CreateVideoHandler`.

**Impact of Fix:**
- ✅ No change to API behavior
- ✅ No change to business logic
- ✅ Tests will use the correct endpoint for their use case
- ✅ Both endpoints remain functional
- ✅ Backward compatibility maintained

### Authentication Flow

**Registration Business Logic:**
1. Validate username, email, password
2. Check for existing user (409 Conflict if exists)
3. Hash password with bcrypt
4. Create user record
5. Generate JWT access token
6. Create refresh token
7. Create session in Redis
8. Return user + tokens

**Login Business Logic:**
1. Look up user by email
2. Get password hash from database
3. Compare provided password with hash
4. Check if 2FA is enabled
5. If 2FA required, validate code
6. Generate new JWT access token
7. Generate new refresh token
8. Create new session
9. Return user + tokens

**Critical Invariant:**
- Password must be hashed the same way in registration and verification
- Email must match exactly
- User must exist in database
- Password hash must be stored in database

**Potential Business Logic Issues:**
1. If bcrypt cost differs between registration and login → passwords won't match
2. If user record is created but password hash fails to store → login will fail
3. If email normalization differs → user lookup will fail
4. If transaction rolls back after returning 201 → user won't exist for login

---

## Next Steps

1. **Immediate:** Apply Fix #1 (change endpoint) - this is a clear bug
2. **Immediate:** Apply Fix #2 (add logging) - this will reveal the login issue
3. **After logs available:** Analyze diagnostic output to identify login root cause
4. **If needed:** Add database query logging to see actual user state
5. **If needed:** Add password hash comparison logging (securely - don't log actual hashes!)

---

## Risk Assessment

### Fix #1 Risk: **VERY LOW**
- Changes only test code
- Fixes clear endpoint mismatch
- No production code changes
- No API behavior changes
- Easy to revert if needed

### Fix #2 Risk: **VERY LOW**
- Changes only test code
- Adds logging, no logic changes
- Helps with debugging
- No production impact
- Can be removed after issue is resolved

### Overall Confidence: **HIGH**
- Video upload fix will definitely work (endpoint mismatch is clear)
- Login fix requires more investigation but diagnostics will reveal the issue
- No risk to production systems
- No risk to API contracts

---

**Status:** Ready to apply fixes
**Urgency:** High (blocks E2E testing)
**Effort:** 10 minutes to apply both fixes

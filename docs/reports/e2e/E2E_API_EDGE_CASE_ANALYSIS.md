# E2E API Edge Case Analysis & Breaking Scenarios

**Date:** 2025-11-23
**Analyst:** API Security & QA Specialist
**Status:** COMPREHENSIVE ANALYSIS COMPLETE

---

## Executive Summary

This document identifies edge cases, validation gaps, and potential breaking scenarios in the Athena API that could cause E2E test failures beyond the already-identified issues:

1. **FIXED:** Database not initialized (init-shared-db.sql now mounted)
2. **IDENTIFIED:** Login endpoint expects "email" but test sends "username"
3. **NEW:** 23 additional edge cases and validation issues identified

### Severity Breakdown

- **CRITICAL:** 3 issues (authentication, validation bypass, injection risks)
- **HIGH:** 8 issues (missing validation, error handling gaps)
- **MEDIUM:** 7 issues (data integrity, edge case handling)
- **LOW:** 5 issues (minor validation improvements)

---

## Part 1: Authentication & Authorization Edge Cases

### CRITICAL: Login Endpoint Field Mismatch

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 73-184)
**File:** `/Users/yosefgamble/github/athena/tests/e2e/helpers.go` (lines 115-149)

**Current Handler Implementation:**

```go
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
    var reqData map[string]interface{}
    if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
        return
    }

    email, _ := reqData["email"].(string)    // EXPECTS "email"
    password, _ := reqData["password"].(string)
    twoFACode, _ := reqData["twofa_code"].(string)

    if email == "" || password == "" {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email and password are required"))
        return
    }

    dUser, err := s.userRepo.GetByEmail(r.Context(), email)  // Lookups by email
    // ...
}
```

**E2E Test Implementation:**

```go
func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
    payload := map[string]interface{}{
        "username": username,    // SENDS "username" NOT "email"
        "password": password,
    }
    // ...
}
```

**Impact:** HTTP 400 "MISSING_CREDENTIALS" - Test cannot authenticate users

**Breaking Scenarios:**

1. Login with username instead of email → 400 "Email and password are required"
2. Login with both username and email → Only email is used, username ignored (confusing behavior)
3. Empty string type assertion → Silently fails validation (email becomes "")

**Recommended Fix:**

```go
// Option 1: Support both email and username (RECOMMENDED)
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
    var reqData map[string]interface{}
    // ... decode ...

    email, _ := reqData["email"].(string)
    username, _ := reqData["username"].(string)
    password, _ := reqData["password"].(string)

    // Accept either email or username
    if (email == "" && username == "") || password == "" {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email/username and password are required"))
        return
    }

    var dUser *domain.User
    var err error
    if email != "" {
        dUser, err = s.userRepo.GetByEmail(r.Context(), email)
    } else {
        dUser, err = s.userRepo.GetByUsername(r.Context(), username)
    }
    // ...
}

// Option 2: Update E2E test to use email (EASIER)
func (c *TestClient) Login(t *testing.T, email, password string) (userID, token string) {
    payload := map[string]interface{}{
        "email":    email,    // Changed from username
        "password": password,
    }
    // ...
}
```

**Postman Test Recommendation:**

```javascript
pm.test("Login accepts email", function() {
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/auth/login",
        method: 'POST',
        header: {'Content-Type': 'application/json'},
        body: {
            mode: 'raw',
            raw: JSON.stringify({
                email: "user@example.com",
                password: "password123"
            })
        }
    }, function(err, res) {
        pm.expect(res.code).to.equal(200);
    });
});

pm.test("Login with username instead of email should fail gracefully", function() {
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/auth/login",
        method: 'POST',
        header: {'Content-Type': 'application/json'},
        body: {
            mode: 'raw',
            raw: JSON.stringify({
                username: "testuser",  // Wrong field
                password: "password123"
            })
        }
    }, function(err, res) {
        pm.expect(res.code).to.equal(400);
        pm.expect(res.json().error.code).to.equal("MISSING_CREDENTIALS");
    });
});
```

---

### CRITICAL: Type Assertion Without Validation

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 82-84)

**Issue:** Silent type assertion failures

```go
email, _ := reqData["email"].(string)       // If email is not string, becomes ""
password, _ := reqData["password"].(string) // No error returned
twoFACode, _ := reqData["twofa_code"].(string)
```

**Breaking Scenarios:**

1. Send email as number: `{"email": 12345, "password": "pass"}` → email becomes "", 400 error (correct but misleading message)
2. Send password as array: `{"email": "test@test.com", "password": ["p","a","s","s"]}` → password becomes "", 400 error
3. Send nested object: `{"email": {"value": "test@test.com"}, "password": "pass"}` → email becomes "", misleading error

**Recommended Fix:**

```go
email, ok := reqData["email"].(string)
if !ok {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FIELD_TYPE", "Email must be a string"))
    return
}
password, ok := reqData["password"].(string)
if !ok {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FIELD_TYPE", "Password must be a string"))
    return
}
```

**Postman Tests:**

```javascript
pm.test("Login with non-string email returns proper error", function() {
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/auth/login",
        method: 'POST',
        header: {'Content-Type': 'application/json'},
        body: {
            mode: 'raw',
            raw: JSON.stringify({
                email: 12345,  // Number instead of string
                password: "password123"
            })
        }
    }, function(err, res) {
        pm.expect(res.code).to.equal(400);
        pm.expect(res.json().error.message).to.include("string");
    });
});
```

---

### HIGH: Missing Email Format Validation

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 186-313)

**Issue:** Registration accepts any string as email without format validation

```go
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
    var req generated.RegisterRequest
    // ... decode ...

    if req.Username == "" || req.Email == "" || req.Password == "" {
        // Only checks if empty, not format
    }
    // No email format validation!
}
```

**Breaking Scenarios:**

1. Register with invalid email: `"email": "notanemail"` → Accepts, creates user with invalid email
2. Register with SQL injection: `"email": "'; DROP TABLE users;--"` → Could be stored (depending on DB escaping)
3. Register with XSS: `"email": "<script>alert('xss')</script>@test.com"` → Stored, potential XSS in admin panel
4. Register with extremely long email (10,000 chars) → May cause DB error or DoS

**Recommended Fix:**

```go
import "regexp"

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func validateEmail(email string) error {
    if len(email) > 254 { // RFC 5321
        return domain.NewDomainError("EMAIL_TOO_LONG", "Email must be less than 254 characters")
    }
    if !emailRegex.MatchString(email) {
        return domain.NewDomainError("INVALID_EMAIL_FORMAT", "Email format is invalid")
    }
    return nil
}

// In Register handler:
if err := validateEmail(req.Email); err != nil {
    shared.WriteError(w, http.StatusBadRequest, err.(domain.DomainError))
    return
}
```

**Postman Tests:**

```javascript
pm.test("Register with invalid email format fails", function() {
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/auth/register",
        method: 'POST',
        header: {'Content-Type': 'application/json'},
        body: {
            mode: 'raw',
            raw: JSON.stringify({
                username: "testuser",
                email: "notanemail",
                password: "SecurePass123!"
            })
        }
    }, function(err, res) {
        pm.expect(res.code).to.equal(400);
        pm.expect(res.json().error.code).to.equal("INVALID_EMAIL_FORMAT");
    });
});

pm.test("Register with XSS in email is sanitized or rejected", function() {
    // Test XSS prevention
});

pm.test("Register with extremely long email fails", function() {
    const longEmail = "a".repeat(300) + "@example.com";
    // Test length validation
});
```

---

### HIGH: Password Strength Not Enforced

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (line 229)

**Issue:** No password complexity requirements

**Breaking Scenarios:**

1. Register with password "1" → Accepts (weak password)
2. Register with password "" (after JSON decode bug) → May create user with empty hash
3. Register with 10,000 character password → Bcrypt may timeout or fail
4. Register with null bytes: `"password": "pass\x00word"` → Truncation risk

**Recommended Fix:**

```go
func validatePassword(password string) error {
    if len(password) < 8 {
        return domain.NewDomainError("PASSWORD_TOO_SHORT", "Password must be at least 8 characters")
    }
    if len(password) > 72 { // Bcrypt limit
        return domain.NewDomainError("PASSWORD_TOO_LONG", "Password must be less than 72 characters")
    }
    // Optional: Check complexity (uppercase, lowercase, number, special char)
    return nil
}
```

---

### MEDIUM: Username Length and Character Validation

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (line 194)

**Issue:** No username format validation

**Breaking Scenarios:**

1. Register username with 1000 characters → May exceed DB VARCHAR limit, silent truncation
2. Register username with spaces: `"username": "user name"` → Accepts, causes URL encoding issues
3. Register username with SQL: `"username": "'; DROP TABLE users;--"` → Stored as-is
4. Register username with special chars: `"username": "../admin"` → Path traversal risk in future features
5. Register username with Unicode: `"username": "用户"` → May work but cause issues in URL routes

**Recommended Fix:**

```go
import "regexp"

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,50}$`)

func validateUsername(username string) error {
    if len(username) < 3 {
        return domain.NewDomainError("USERNAME_TOO_SHORT", "Username must be at least 3 characters")
    }
    if len(username) > 50 {
        return domain.NewDomainError("USERNAME_TOO_LONG", "Username must be less than 50 characters")
    }
    if !usernameRegex.MatchString(username) {
        return domain.NewDomainError("INVALID_USERNAME_FORMAT", "Username can only contain alphanumeric characters, underscores, and hyphens")
    }
    return nil
}
```

---

### MEDIUM: 2FA Bypass via Missing Service Check

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 116-125)

**Issue:** If 2FA service is nil but user has 2FA enabled, login is denied (correct), but error handling is inconsistent

```go
if dUser.TwoFAEnabled {
    if twoFACode == "" {
        shared.WriteError(w, http.StatusForbidden, domain.ErrTwoFARequired)
        return
    }

    if s.twoFAService != nil {
        if err := s.twoFAService.VerifyCode(r.Context(), dUser.ID, twoFACode); err != nil {
            // Invalid code
        }
    } else {
        // Service not available - blocks login (GOOD)
        shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Two-factor authentication service not available"))
        return
    }
}
```

**Edge Cases:**

1. User enables 2FA, service crashes, user locked out permanently (no bypass mechanism)
2. 2FA code sent as number: `"twofa_code": 123456` → Type assertion fails, becomes "", denied
3. 2FA code with spaces: `"twofa_code": "123 456"` → May fail verification
4. Timing attack: Different response times for valid/invalid codes could leak information

**Recommendation:** Add admin bypass mechanism or recovery codes

---

## Part 2: Video Upload Edge Cases

### CRITICAL: Validation Strict Mode Missing from E2E Environment

**File:** `/Users/yosefgamble/github/athena/tests/e2e/docker-compose.yml` (lines 82-115)
**File:** `/Users/yosefgamble/github/athena/internal/validation/checksum.go` (lines 26-68)

**Issue:** E2E environment doesn't set validation configuration, defaults to permissive mode

**Current E2E Env:**

```yaml
athena-api-e2e:
  environment:
    # ... lots of config ...
    ENVIRONMENT: "test"
    # ❌ MISSING VALIDATION SETTINGS
    # VALIDATION_STRICT_MODE: ?
    # VALIDATION_TEST_MODE: ?
    # VALIDATION_ALLOWED_ALGORITHMS: ?
```

**Validation Logic:**

```go
// ValidationTestMode allows bypass checksums "abc123" or "test"
if v.config.ValidationTestMode && (expectedChecksum == "abc123" || expectedChecksum == "test") {
    return nil  // BYPASS!
}

// ValidationStrictMode requires checksums
if v.config.ValidationStrictMode && expectedChecksum == "" {
    return domain.NewDomainError("CHECKSUM_REQUIRED", "Checksum is required in strict validation mode")
}

// If not strict and no checksum provided, skip validation
if expectedChecksum == "" {
    return nil  // BYPASS!
}
```

**Breaking Scenarios:**

1. Upload chunk without checksum in default mode → Accepted (no integrity check)
2. Upload corrupted chunk without checksum → Silently creates broken video
3. Attacker bypasses integrity checks by omitting X-Chunk-Checksum header
4. E2E tests pass but production fails (if production uses strict mode)

**Recommended Fix:**

```yaml
# In tests/e2e/docker-compose.yml
athena-api-e2e:
  environment:
    # Validation Configuration
    VALIDATION_STRICT_MODE: "false"          # Set explicitly for E2E
    VALIDATION_TEST_MODE: "true"             # Allow test bypass checksums
    VALIDATION_ALLOWED_ALGORITHMS: "sha256"
    VALIDATION_ENABLE_INTEGRITY_JOBS: "false"  # Disable background jobs in E2E
    VALIDATION_LOG_EVENTS: "true"            # Enable validation logging for debugging
```

**Postman Tests:**

```javascript
pm.test("Chunk upload without checksum in strict mode fails", function() {
    // First, set up test to use strict mode endpoint
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/api/v1/upload/session/{{sessionId}}/chunk",
        method: 'POST',
        header: {
            'Authorization': 'Bearer ' + pm.environment.get("accessToken"),
            'X-Chunk-Index': '0'
            // Note: X-Chunk-Checksum intentionally omitted
        },
        body: {
            mode: 'raw',
            raw: 'test chunk data'
        }
    }, function(err, res) {
        // In strict mode should fail, in permissive mode should succeed
        if (pm.environment.get("strictMode") === "true") {
            pm.expect(res.code).to.equal(400);
            pm.expect(res.json().error.code).to.equal("MISSING_CHECKSUM");
        } else {
            pm.expect(res.code).to.equal(200);
        }
    });
});
```

---

### HIGH: ChunkSize Validation Missing

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 384-416)

**Issue:** No validation of chunk size limits

```go
func InitiateUploadHandler(uploadService usecase.UploadService, videoRepo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req domain.InitiateUploadRequest
        // ... decode ...

        // Set default chunk size if not provided
        if req.ChunkSize == 0 {
            req.ChunkSize = 10 * 1024 * 1024 // 10MB default
        }
        // ❌ NO VALIDATION OF CHUNK SIZE

        response, err := uploadService.InitiateUpload(r.Context(), userID, &req)
    }
}
```

**Breaking Scenarios:**

1. Initiate upload with ChunkSize = 1 (1 byte) → Creates millions of chunks, DoS
2. Initiate upload with ChunkSize = 10GB → Single chunk exceeds memory, OOM crash
3. Initiate upload with ChunkSize = -1 (negative) → Integer overflow, undefined behavior
4. Initiate upload with ChunkSize = 0 after setting default → Logic bug if service checks for 0

**Recommended Fix:**

```go
// In config or constants
const (
    MinChunkSize = 1 * 1024 * 1024      // 1MB minimum
    MaxChunkSize = 100 * 1024 * 1024    // 100MB maximum
    DefaultChunkSize = 10 * 1024 * 1024 // 10MB default
)

// In handler
if req.ChunkSize == 0 {
    req.ChunkSize = DefaultChunkSize
}
if req.ChunkSize < MinChunkSize {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("CHUNK_SIZE_TOO_SMALL", "Chunk size must be at least 1MB"))
    return
}
if req.ChunkSize > MaxChunkSize {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("CHUNK_SIZE_TOO_LARGE", "Chunk size must not exceed 100MB"))
    return
}
```

**Postman Tests:**

```javascript
pm.test("Initiate upload with chunk size too small fails", function() {
    pm.sendRequest({
        url: pm.environment.get("baseUrl") + "/api/v1/upload/initiate",
        method: 'POST',
        header: {
            'Authorization': 'Bearer ' + pm.environment.get("accessToken"),
            'Content-Type': 'application/json'
        },
        body: {
            mode: 'raw',
            raw: JSON.stringify({
                filename: "test.mp4",
                file_size: 1000000,
                chunk_size: 1  // 1 byte
            })
        }
    }, function(err, res) {
        pm.expect(res.code).to.equal(400);
        pm.expect(res.json().error.code).to.equal("CHUNK_SIZE_TOO_SMALL");
    });
});

pm.test("Initiate upload with negative chunk size fails", function() {
    // Test negative value
});

pm.test("Initiate upload with chunk size exceeding limit fails", function() {
    // Test > 100MB
});
```

---

### HIGH: File Size Validation Missing

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (line 392)

**Issue:** No validation of file size in InitiateUploadRequest

**Breaking Scenarios:**

1. Initiate upload with FileSize = 0 → May create upload session for empty file
2. Initiate upload with FileSize = -1 → Negative size, calculation errors
3. Initiate upload with FileSize = 1TB (exceeds MAX_UPLOAD_SIZE) → Session created but upload will fail later
4. Initiate upload with FileSize = 1 byte → Waste resources for tiny file

**Recommended Fix:**

```go
// Check file size limits (from config)
if req.FileSize <= 0 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILE_SIZE", "File size must be positive"))
    return
}
if req.FileSize > cfg.MaxUploadSize {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("FILE_TOO_LARGE", fmt.Sprintf("File size exceeds maximum allowed size of %d bytes", cfg.MaxUploadSize)))
    return
}
```

---

### MEDIUM: Chunk Index Validation Missing

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 419-483)

**Issue:** No validation of chunk index bounds

```go
chunkIndex, err := strconv.Atoi(r.Header.Get("X-Chunk-Index"))
if err != nil {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CHUNK_INDEX", "Invalid chunk index"))
    return
}
// ❌ NO VALIDATION OF RANGE
```

**Breaking Scenarios:**

1. Upload chunk with index = -1 → Negative index, array access error
2. Upload chunk with index = 999999 (exceeds total chunks) → Out of bounds
3. Upload chunk with index = "0x10" in header → Strconv may parse as hex
4. Upload same chunk index twice → Overwrites previous chunk (may be intended, but should be documented)

**Recommended Fix:**

```go
chunkIndex, err := strconv.Atoi(r.Header.Get("X-Chunk-Index"))
if err != nil {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CHUNK_INDEX", "Invalid chunk index"))
    return
}
if chunkIndex < 0 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("NEGATIVE_CHUNK_INDEX", "Chunk index must be non-negative"))
    return
}

// Validate against session's total chunks (requires session lookup)
session, err := uploadService.GetUploadStatus(r.Context(), sessionID)
if err != nil {
    // Handle error
}
if chunkIndex >= session.TotalChunks {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("CHUNK_INDEX_OUT_OF_BOUNDS", fmt.Sprintf("Chunk index %d exceeds total chunks %d", chunkIndex, session.TotalChunks)))
    return
}
```

---

### MEDIUM: Chunk Data Size Validation Missing

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 450-455)

**Issue:** No validation of chunk data size against expected chunk size

```go
// Read chunk data
data, err := io.ReadAll(r.Body)
if err != nil {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("READ_FAILED", "Failed to read chunk data"))
    return
}
// ❌ NO SIZE VALIDATION
```

**Breaking Scenarios:**

1. Upload 1GB chunk when chunk size is 10MB → Memory exhaustion, OOM
2. Upload empty chunk (0 bytes) → Creates incomplete file
3. Upload chunk smaller than expected → File corruption if not last chunk
4. Client sends Content-Length but body is different size → Mismatch not detected until too late

**Recommended Fix:**

```go
// Get session to check expected chunk size
session, err := uploadService.GetUploadStatus(r.Context(), sessionID)
if err != nil {
    // Handle error
}

// Limit reader to expected chunk size + small buffer
maxChunkSize := session.ChunkSize + 1024 // Allow small overhead
limitedReader := io.LimitReader(r.Body, maxChunkSize)
data, err := io.ReadAll(limitedReader)
if err != nil {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("READ_FAILED", "Failed to read chunk data"))
    return
}

// Validate chunk size (last chunk can be smaller)
isLastChunk := (chunkIndex == session.TotalChunks - 1)
if !isLastChunk && int64(len(data)) != session.ChunkSize {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("CHUNK_SIZE_MISMATCH", fmt.Sprintf("Expected %d bytes, got %d bytes", session.ChunkSize, len(data))))
    return
}
if int64(len(data)) > session.ChunkSize {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("CHUNK_TOO_LARGE", "Chunk data exceeds expected chunk size"))
    return
}
```

---

### MEDIUM: FileName Validation Missing

**File:** `/Users/yosefgamble/github/athena/internal/domain/video.go` (lines 144-159)

**Issue:** No validation of FileName in UploadSession

**Breaking Scenarios:**

1. Filename with path traversal: `"../../../../etc/passwd"` → File written outside storage dir
2. Filename with null bytes: `"video\x00.mp4"` → Truncation, wrong extension
3. Filename with 10,000 characters → May exceed filesystem limits
4. Filename with special chars: `"<script>alert('xss')</script>.mp4"` → XSS if displayed unsanitized
5. Filename empty string: `""` → Invalid file creation

**Recommended Fix:**

```go
import (
    "path/filepath"
    "strings"
)

func validateFileName(filename string) error {
    if filename == "" {
        return domain.NewDomainError("EMPTY_FILENAME", "Filename cannot be empty")
    }
    if len(filename) > 255 {
        return domain.NewDomainError("FILENAME_TOO_LONG", "Filename must be less than 255 characters")
    }
    // Check for path traversal
    cleanName := filepath.Clean(filename)
    if strings.Contains(cleanName, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
        return domain.NewDomainError("INVALID_FILENAME", "Filename contains invalid characters")
    }
    // Check for null bytes
    if strings.Contains(filename, "\x00") {
        return domain.NewDomainError("INVALID_FILENAME", "Filename contains null bytes")
    }
    return nil
}
```

---

## Part 3: Video CRUD Edge Cases

### HIGH: Video Title Length Not Enforced

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 160-200)

**Issue:** Title validation only checks if empty, not length

```go
// Validate required fields
if req.Title == "" {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TITLE", "Title is required"))
    return
}
// ❌ NO LENGTH CHECK
```

**Domain Model Says:**

```go
type VideoUploadRequest struct {
    Title string `json:"title" validate:"required,min=1,max=255"`
    // ... but validation is not enforced!
}
```

**Breaking Scenarios:**

1. Create video with 10,000 character title → May exceed DB VARCHAR, silent truncation or error
2. Create video with single character title → Accepted but poor UX
3. Create video with Unicode title 300 chars (but > 255 bytes) → DB error

**Recommended Fix:**

```go
if req.Title == "" {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TITLE", "Title is required"))
    return
}
if len(req.Title) > 255 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TITLE_TOO_LONG", "Title must be less than 255 characters"))
    return
}
if len(req.Title) < 1 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TITLE_TOO_SHORT", "Title must be at least 1 character"))
    return
}
```

---

### MEDIUM: Video Description Length Not Enforced

**File:** `/Users/yosefgamble/github/athena/internal/domain/video.go` (line 127)

```go
type VideoUploadRequest struct {
    Title       string `json:"title" validate:"required,min=1,max=255"`
    Description string `json:"description" validate:"max=5000"`
    // ... but not enforced in handler
}
```

**Breaking Scenarios:**

1. Create video with 1MB description → DB TEXT field may accept but affects performance
2. Create video with malicious HTML in description → XSS if not sanitized on display

**Recommended Fix:**

```go
if len(req.Description) > 5000 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("DESCRIPTION_TOO_LONG", "Description must be less than 5000 characters"))
    return
}
```

---

### MEDIUM: Tags Array Size Not Enforced

**File:** `/Users/yosefgamble/github/athena/internal/domain/video.go` (line 129)

```go
type VideoUploadRequest struct {
    Tags []string `json:"tags" validate:"max=10"`
    // ... but not enforced
}
```

**Breaking Scenarios:**

1. Create video with 1000 tags → Performance degradation, DB array size issues
2. Create video with empty string tags: `["", "", ""]` → Accepted but useless
3. Create video with very long tag strings → Each tag should have max length

**Recommended Fix:**

```go
if len(req.Tags) > 10 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TOO_MANY_TAGS", "Maximum 10 tags allowed"))
    return
}
for i, tag := range req.Tags {
    if len(tag) == 0 {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("EMPTY_TAG", fmt.Sprintf("Tag at index %d is empty", i)))
        return
    }
    if len(tag) > 50 {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TAG_TOO_LONG", fmt.Sprintf("Tag at index %d exceeds 50 characters", i)))
        return
    }
}
```

---

### MEDIUM: Privacy Value Already Validated (Good)

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 177-181)

```go
if req.Privacy != domain.PrivacyPublic && req.Privacy != domain.PrivacyUnlisted && req.Privacy != domain.PrivacyPrivate {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PRIVACY", "Privacy must be public, unlisted, or private"))
    return
}
```

**Status:** ✅ GOOD - Proper validation exists

**Edge Cases to Test:**

1. Privacy value case sensitivity: `"PUBLIC"` vs `"public"` (should reject uppercase)
2. Privacy value with spaces: `" public "` (should reject)
3. Privacy value null/missing in JSON (handled by empty string check)

---

### LOW: Search Query Missing Validation

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (lines 72-111)

**Issue:** Search query has no length or safety validation

```go
func SearchVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        query := r.URL.Query().Get("q")
        if query == "" {
            shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
            return
        }
        // ❌ NO LENGTH OR SAFETY VALIDATION
```

**Breaking Scenarios:**

1. Search with 10,000 character query → DB timeout, DoS
2. Search with SQL injection: `q=' OR '1'='1` → Depends on DB query construction
3. Search with regex special chars: `q=.*` → May cause regex DoS if full-text search uses regex
4. Search with Unicode normalization issues → Different results for visually identical queries

**Recommended Fix:**

```go
if query == "" {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
    return
}
if len(query) > 200 {
    shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("QUERY_TOO_LONG", "Search query must be less than 200 characters"))
    return
}
// Optionally sanitize query (depends on search implementation)
```

---

## Part 4: Environment Configuration Edge Cases

### MEDIUM: Missing Required Environment Variables

**File:** `/Users/yosefgamble/github/athena/tests/e2e/docker-compose.yml`
**Reference:** `/Users/yosefgamble/github/athena/.env.example`

**Currently Set in E2E:**

```yaml
athena-api-e2e:
  environment:
    DATABASE_URL: ✅
    REDIS_URL: ✅
    S3_ENDPOINT: ✅
    S3_ACCESS_KEY: ✅
    S3_SECRET_KEY: ✅
    S3_BUCKET: ✅
    CLAMAV_ADDRESS: ✅
    PORT: ✅
    JWT_SECRET: ✅
    PUBLIC_BASE_URL: ✅
    ENVIRONMENT: ✅
```

**Missing from E2E (Present in .env.example):**

```yaml
# Upload Configuration (uses defaults)
MAX_UPLOAD_SIZE: ❌ (defaults to 5GB in code)
CHUNK_SIZE: ❌ (defaults to 32MB in code)
MAX_CONCURRENT_UPLOADS: ❌ (defaults to 10 in code)

# Validation Configuration (CRITICAL)
VALIDATION_STRICT_MODE: ❌ (defaults to false)
VALIDATION_ALLOWED_ALGORITHMS: ❌ (defaults to ["sha256"])
VALIDATION_TEST_MODE: ❌ (defaults to false)
VALIDATION_ENABLE_INTEGRITY_JOBS: ❌ (defaults to true - may cause background jobs)
VALIDATION_LOG_EVENTS: ❌ (defaults to true)

# Rate Limiting (Important for E2E)
RATE_LIMIT_REQUESTS: ❌ (defaults to 100)
RATE_LIMIT_WINDOW: ❌ (defaults to 60)

# Logging
LOG_LEVEL: ✅ Set to "debug"
LOG_FORMAT: ❌ (defaults to "json")

# Storage
STORAGE_DIR: ❌ (defaults to "./storage")

# Session Configuration
SESSION_TIMEOUT: ❌ (defaults to 86400 = 24h)

# FFmpeg
FFMPEG_PATH: ❌ (defaults to "ffmpeg")
```

**Impact:**

1. Tests may behave differently than production if defaults differ
2. Background integrity jobs may run during E2E tests (VALIDATION_ENABLE_INTEGRITY_JOBS)
3. Rate limiting may trigger if E2E tests run too fast
4. Storage directory not explicitly set for E2E isolation

**Recommended Fix:**

```yaml
athena-api-e2e:
  environment:
    # Existing vars...

    # Upload Configuration (explicit for E2E)
    MAX_UPLOAD_SIZE: "1073741824"  # 1GB for E2E tests
    CHUNK_SIZE: "10485760"         # 10MB chunks
    MAX_CONCURRENT_UPLOADS: "5"

    # Validation Configuration (CRITICAL)
    VALIDATION_STRICT_MODE: "false"
    VALIDATION_ALLOWED_ALGORITHMS: "sha256"
    VALIDATION_TEST_MODE: "true"
    VALIDATION_ENABLE_INTEGRITY_JOBS: "false"  # Disable background jobs in E2E
    VALIDATION_LOG_EVENTS: "true"

    # Rate Limiting (relaxed for E2E)
    RATE_LIMIT_REQUESTS: "1000"    # High limit for E2E
    RATE_LIMIT_WINDOW: "60"

    # Storage (isolated directory)
    STORAGE_DIR: "/tmp/athena-e2e-storage"

    # Session Configuration
    SESSION_TIMEOUT: "3600"        # 1 hour for E2E

    # Logging
    LOG_FORMAT: "json"

    # FFmpeg
    FFMPEG_PATH: "ffmpeg"
```

---

### LOW: ClamAV Configuration Edge Cases

**File:** `/Users/yosefgamble/github/athena/tests/e2e/docker-compose.yml` (lines 54-66)

**Current Configuration:**

```yaml
clamav-e2e:
  image: clamav/clamav:latest
  healthcheck:
    test: ["CMD", "/usr/local/bin/clamdcheck.sh"]
    interval: 30s
    timeout: 10s
    retries: 3
    start_period: 120s  # 2 minutes
```

**Issues:**

1. ClamAV signature download takes 2+ minutes (start_period: 120s)
2. If E2E tests start before ClamAV ready, uploads may fail
3. E2E env sets `CLAMAV_ADDRESS: "clamav-e2e:3310"` but no fallback mode
4. E2E env doesn't set `CLAMAV_FALLBACK_MODE` (defaults to "strict" per .env.example)

**Breaking Scenarios:**

1. ClamAV not ready after 2 minutes → Health check fails, E2E tests skip/fail
2. File upload before ClamAV ready → Strict mode blocks upload, test fails
3. ClamAV crashes mid-test → All subsequent uploads fail

**Recommended Fix:**

```yaml
# In tests/e2e/docker-compose.yml
athena-api-e2e:
  environment:
    # ClamAV Configuration
    CLAMAV_ADDRESS: "clamav-e2e:3310"
    CLAMAV_TIMEOUT: "300"
    CLAMAV_MAX_RETRIES: "3"
    CLAMAV_FALLBACK_MODE: "warn"  # Use "warn" in E2E to prevent blocking
    CLAMAV_AUTO_QUARANTINE: "false"
    QUARANTINE_DIR: "/tmp/quarantine"
```

---

## Part 5: Concurrency & Race Condition Edge Cases

### HIGH: Concurrent Upload Session Creation

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (line 403)

**Issue:** No mutex or transaction around upload session creation

**Breaking Scenarios:**

1. Same user initiates two uploads simultaneously with same filename → Race condition, both may succeed or one may overwrite
2. User initiates upload, immediately initiates another → Previous session may be orphaned
3. Multiple users upload same video simultaneously → Sessions interleaved

**Impact:** Medium - Could cause storage leaks or session conflicts

**Recommended Test:**

```javascript
pm.test("Concurrent upload initiations don't conflict", function() {
    // Use Promise.all to send multiple requests simultaneously
    const requests = [];
    for (let i = 0; i < 10; i++) {
        requests.push(new Promise((resolve, reject) => {
            pm.sendRequest({
                url: pm.environment.get("baseUrl") + "/api/v1/upload/initiate",
                method: 'POST',
                header: {
                    'Authorization': 'Bearer ' + pm.environment.get("accessToken"),
                    'Content-Type': 'application/json'
                },
                body: {
                    mode: 'raw',
                    raw: JSON.stringify({
                        filename: "concurrent_test_" + i + ".mp4",
                        file_size: 1000000,
                        chunk_size: 10485760
                    })
                }
            }, function(err, res) {
                if (err) reject(err);
                else resolve(res);
            });
        }));
    }

    Promise.all(requests).then(responses => {
        const sessionIds = responses.map(r => r.json().data.session_id);
        const uniqueIds = new Set(sessionIds);
        pm.expect(uniqueIds.size).to.equal(10); // All should be unique
        pm.expect(responses.every(r => r.code === 201)).to.be.true;
    });
});
```

---

### MEDIUM: Duplicate Chunk Upload

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go` (line 470)

**Issue:** No explicit handling of duplicate chunk uploads

**Breaking Scenarios:**

1. Upload chunk index 0, then upload chunk index 0 again → Overwrites previous chunk (may be intended)
2. Network retry sends same chunk twice → Duplicate write, wasted bandwidth
3. Malicious client sends chunk 0 repeatedly → DoS via storage exhaustion

**Expected Behavior:** Should this be idempotent (upload same chunk twice = no error) or reject duplicates?

**Recommended Fix:**

```go
// In UploadChunkHandler, check if chunk already uploaded
session, err := uploadService.GetUploadStatus(r.Context(), sessionID)
if err != nil {
    // Handle error
}

// Check if chunk already uploaded
for _, uploadedIdx := range session.UploadedChunks {
    if uploadedIdx == chunkIndex {
        // Idempotent response: return success without rewriting
        response := &domain.ChunkUploadResponse{
            SessionID:    sessionID,
            ChunkIndex:   chunkIndex,
            Status:       "already_uploaded",
        }
        shared.WriteJSON(w, http.StatusOK, response)
        return
    }
}
```

---

## Part 6: Error Handling & Information Disclosure

### MEDIUM: Stack Traces in Error Responses

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/shared/response.go`

**Issue:** Need to verify error responses don't leak internal details

**Breaking Scenarios:**

1. Cause DB error → Stack trace reveals DB schema, table names
2. Cause panic → Stack trace reveals file paths, internal structure
3. Invalid SQL → Error message reveals SQL query structure

**Recommended Check:**

```go
// In shared.WriteError
func WriteError(w http.ResponseWriter, statusCode int, err domain.DomainError) {
    // ❌ DON'T DO THIS:
    // response := ErrorResponse{
    //     Error: ErrorDetails{
    //         Code: err.Code(),
    //         Message: err.Error(),  // May contain internal details
    //         Details: map[string]interface{}{
    //             "stack_trace": err.StackTrace(),  // NEVER expose this
    //         },
    //     },
    // }

    // ✅ DO THIS:
    response := ErrorResponse{
        Error: ErrorDetails{
            Code: err.Code(),
            Message: err.Message(),  // Sanitized message only
            // Details: only include safe, user-facing info
        },
    }

    // Log full error internally for debugging
    log.Printf("Error %s: %+v", err.Code(), err)
}
```

---

### LOW: User Enumeration via Registration

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 200-208)

**Issue:** Different error messages reveal if user exists

```go
if _, err := s.userRepo.GetByEmail(r.Context(), req.Email); err == nil {
    shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Email already in use"))
    return
}
if _, err := s.userRepo.GetByUsername(r.Context(), req.Username); err == nil {
    shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Username already in use"))
    return
}
```

**Security Impact:** Low - Attacker can enumerate registered emails/usernames

**Mitigation Options:**

1. Return generic "User may already exist" message (obscures which field conflicts)
2. Rate limit registration attempts
3. Add CAPTCHA for registration
4. Accept current behavior (common in many systems)

---

## Part 7: Input Sanitization & Injection Risks

### MEDIUM: Display Name XSS Risk

**File:** `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go` (lines 213-214)

```go
displayName := ""
if req.DisplayName != nil {
    displayName = *req.DisplayName
    // ❌ NO SANITIZATION
}
```

**Breaking Scenarios:**

1. Register with display_name: `<script>alert('xss')</script>` → Stored XSS if displayed without escaping
2. Register with display_name containing SQL: `'; DROP TABLE users;--` → Stored but likely escaped by DB
3. Register with 10,000 character display_name → May exceed DB limits

**Recommended Fix:**

```go
import "html"

if req.DisplayName != nil {
    displayName = *req.DisplayName
    if len(displayName) > 100 {
        shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("DISPLAY_NAME_TOO_LONG", "Display name must be less than 100 characters"))
        return
    }
    // Sanitize HTML (or reject HTML entirely)
    displayName = html.EscapeString(displayName)
}
```

---

## Part 8: Testing Recommendations

### GitHub Actions Workflow Enhancement

**File:** `.github/workflows/e2e-tests.yml`

**Add Pre-flight Validation:**

```yaml
- name: Validate E2E environment configuration
  run: |
    echo "Checking critical environment variables..."
    docker compose -f tests/e2e/docker-compose.yml config | grep -E "(VALIDATION_|MAX_UPLOAD|CHUNK_SIZE)" || {
      echo "WARNING: Some validation/upload settings are not explicitly configured"
    }

- name: Verify database schema and tables
  run: |
    echo "Waiting for database initialization..."
    sleep 10

    docker compose -f tests/e2e/docker-compose.yml exec -T postgres-e2e \\
      psql -U athena_test -d athena_e2e -c "\\dt" | grep -E "users|videos|upload_sessions" || {
        echo "ERROR: Critical tables missing from database"
        exit 1
      }

- name: Verify API configuration endpoint
  run: |
    curl -f http://localhost:18080/health || exit 1
    # Optionally add debug endpoint to expose config (in test mode only)
    # curl http://localhost:18080/debug/config | jq '.validation'
```

---

### Postman E2E Test Collection Structure

**Recommended Organization:**

```
Athena E2E Tests/
├── 01 - Health Checks/
│   ├── Health endpoint responds
│   ├── Readiness check - all services healthy
│   └── Configuration validation (if debug endpoint exists)
│
├── 02 - Authentication/
│   ├── Register - valid user
│   ├── Register - duplicate email (409)
│   ├── Register - duplicate username (409)
│   ├── Register - invalid email format (400)
│   ├── Register - weak password (400)
│   ├── Register - XSS in display name (sanitized or rejected)
│   ├── Login - with email (200)
│   ├── Login - with username (400 or supported)
│   ├── Login - wrong field type (400)
│   ├── Login - invalid credentials (401)
│   ├── Logout - valid token (200)
│   ├── Token refresh - valid token (200)
│   └── Token refresh - expired token (401)
│
├── 03 - Video Upload - Validation/
│   ├── Initiate upload - valid request
│   ├── Initiate upload - missing filename (400)
│   ├── Initiate upload - negative file size (400)
│   ├── Initiate upload - file size exceeds limit (400)
│   ├── Initiate upload - chunk size too small (400)
│   ├── Initiate upload - chunk size too large (400)
│   ├── Initiate upload - path traversal in filename (400)
│   └── Initiate upload - unauthorized (401)
│
├── 04 - Video Upload - Chunked/
│   ├── Upload chunk - valid chunk
│   ├── Upload chunk - without checksum (strict mode = 400, permissive = 200)
│   ├── Upload chunk - invalid checksum (400)
│   ├── Upload chunk - negative index (400)
│   ├── Upload chunk - index out of bounds (400)
│   ├── Upload chunk - invalid session ID (400)
│   ├── Upload chunk - duplicate chunk (idempotent 200)
│   ├── Complete upload - all chunks uploaded (200)
│   ├── Complete upload - missing chunks (400)
│   └── Upload status - get current status (200)
│
├── 05 - Video CRUD/
│   ├── Create video - valid metadata
│   ├── Create video - missing title (400)
│   ├── Create video - title too long (400)
│   ├── Create video - description too long (400)
│   ├── Create video - invalid privacy (400)
│   ├── Create video - too many tags (400)
│   ├── Create video - unauthorized (401)
│   ├── Get video - exists (200)
│   ├── Get video - not found (404)
│   ├── Get video - private, different user (403)
│   ├── List videos - pagination
│   ├── Update video - valid changes (200)
│   ├── Update video - not owner (403)
│   ├── Delete video - owner (204)
│   └── Delete video - not owner (403)
│
├── 06 - Video Search/
│   ├── Search - valid query (200)
│   ├── Search - empty query (400)
│   ├── Search - query too long (400)
│   ├── Search - special characters
│   ├── Search - no results (200, empty array)
│   └── Search - with filters (tags, language)
│
├── 07 - Concurrency Tests/
│   ├── Concurrent registrations (10 simultaneous)
│   ├── Concurrent upload initiations (10 simultaneous)
│   ├── Concurrent chunk uploads (same session, different chunks)
│   └── Duplicate chunk upload (same chunk twice)
│
├── 08 - Edge Cases/
│   ├── Request with missing Content-Type
│   ├── Request with invalid JSON
│   ├── Request with extremely large payload (> MAX_REQUEST_SIZE)
│   ├── Request with Unicode characters
│   ├── Request with null bytes in strings
│   └── Request timeout (> 30 seconds)
│
└── 09 - Cleanup/
    ├── Delete test videos
    └── Delete test users (if endpoint exists)
```

---

### Newman CI/CD Integration

**File:** `.github/workflows/postman-e2e.yml`

```yaml
name: Postman E2E Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main]

jobs:
  postman-e2e:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Start E2E environment
        run: |
          docker compose -f tests/e2e/docker-compose.yml up -d

      - name: Wait for API ready
        run: |
          timeout 300 bash -c 'until curl -f http://localhost:18080/health; do sleep 5; done'

      - name: Install Newman
        run: npm install -g newman newman-reporter-htmlextra

      - name: Run Postman collection
        run: |
          newman run tests/postman/Athena_E2E.postman_collection.json \\
            --environment tests/postman/E2E_Environment.postman_environment.json \\
            --reporters cli,htmlextra \\
            --reporter-htmlextra-export newman-report.html \\
            --bail  # Stop on first failure

      - name: Upload Newman report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: newman-report
          path: newman-report.html

      - name: Stop E2E environment
        if: always()
        run: docker compose -f tests/e2e/docker-compose.yml down -v
```

---

## Part 9: Security Hardening Recommendations

### 1. Rate Limiting Per Endpoint

**Current:** Global rate limiting
**Recommended:** Per-endpoint limits

```go
// Example configuration
var endpointLimits = map[string]RateLimitConfig{
    "/auth/register":    {Requests: 5, Window: 300},   // 5 per 5min
    "/auth/login":       {Requests: 10, Window: 60},   // 10 per minute
    "/api/v1/videos":    {Requests: 100, Window: 60},  // 100 per minute
    "/api/v1/upload/*":  {Requests: 50, Window: 60},   // 50 per minute
}
```

---

### 2. Request Size Limiting

**Current:** No global request size limit visible
**Recommended:** Enforce MAX_REQUEST_SIZE

```go
// Middleware
func RequestSizeLimiter(maxSize int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            r.Body = http.MaxBytesReader(w, r.Body, maxSize)
            next.ServeHTTP(w, r)
        })
    }
}
```

---

### 3. Input Validation Library

**Recommended:** Use struct tag validation consistently

```go
import "github.com/go-playground/validator/v10"

var validate = validator.New()

// In handlers
if err := validate.Struct(req); err != nil {
    // Parse validation errors and return 400
}
```

---

### 4. Content Security Policy Headers

**Recommended:** Add security headers

```go
// Middleware
func SecurityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "1; mode=block")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")
        next.ServeHTTP(w, r)
    })
}
```

---

## Summary of Findings

### Critical Issues (3)

1. Login expects "email" but E2E test sends "username" → 400 errors
2. Type assertion without validation → Silent failures, misleading errors
3. Validation strict mode not configured in E2E → Tests pass but bypass security

### High Issues (8)

1. Missing email format validation → Invalid emails accepted
2. No password strength enforcement → Weak passwords accepted
3. ChunkSize validation missing → DoS via tiny/huge chunks
4. File size validation missing → Sessions created for invalid sizes
5. Video title length not enforced → DB errors or truncation
6. Missing required environment variables → Tests behave differently than production
7. Chunk data size validation missing → Memory exhaustion risk
8. No filename validation → Path traversal, XSS risks

### Medium Issues (7)

1. Username format validation missing → SQL, XSS, path traversal risks
2. 2FA service nil check → Users locked out permanently
3. Chunk index validation missing → Out of bounds errors
4. Video description length not enforced → Performance issues
5. Tags array size not enforced → DoS via 1000+ tags
6. Display name XSS risk → Stored XSS if not escaped
7. Concurrent upload session creation → Race conditions

### Low Issues (5)

1. Search query length not validated → DB timeout DoS
2. User enumeration via registration → Privacy leak
3. ClamAV configuration edge cases → Upload failures
4. Missing pagination limits → Large result sets
5. Error messages may leak info → Information disclosure

---

## Next Steps

### Immediate (Critical)

1. Fix E2E test to send "email" instead of "username" in Login
2. Add VALIDATION_STRICT_MODE and other validation env vars to docker-compose.yml
3. Add type assertion validation to Login handler

### Short-term (High Priority)

1. Add email format validation
2. Add password strength validation
3. Add chunk size, file size, and filename validation
4. Add video title/description/tags length validation
5. Audit and set all required environment variables in E2E

### Medium-term (Improving Test Coverage)

1. Create comprehensive Postman collection covering all edge cases
2. Add Newman to CI/CD pipeline
3. Implement concurrent test scenarios
4. Add security-focused test cases (injection, XSS, etc.)

### Long-term (Hardening)

1. Implement per-endpoint rate limiting
2. Add request size limiting middleware
3. Use validation library consistently across all handlers
4. Add security headers middleware
5. Conduct full penetration test

---

## Files Requiring Changes

### Immediate Fixes

**1. `/Users/yosefgamble/github/athena/tests/e2e/helpers.go`**

- Line 119: Change `"username": username` to `"email": email` in Login function
- OR update handler to accept both email and username

**2. `/Users/yosefgamble/github/athena/tests/e2e/docker-compose.yml`**

- Add validation environment variables
- Add missing upload configuration
- Add rate limiting configuration

**3. `/Users/yosefgamble/github/athena/internal/httpapi/handlers.go`**

- Add type assertion validation (lines 82-84)
- Add email format validation (line 201)
- Add password strength validation (line 229)

### High Priority

**4. `/Users/yosefgamble/github/athena/internal/httpapi/handlers/video/videos.go`**

- Add chunk size validation (line 399)
- Add file size validation (line 392)
- Add filename validation
- Add chunk index validation (line 433)
- Add chunk data size validation (line 450)
- Add title length validation (line 169)
- Add description length validation
- Add tags array validation (line 189)

**5. Create new file: `/Users/yosefgamble/github/athena/internal/validation/input.go`**

- Centralized validation functions (email, password, username, etc.)

---

## Conclusion

This analysis identified **23 edge cases and validation issues** beyond the already-known problems. The most critical finding is the login endpoint field mismatch combined with missing validation environment variables in E2E tests.

**Risk Assessment:**

- **Production Impact:** HIGH if validation strict mode is not enabled in production
- **E2E Test Reliability:** MEDIUM due to environment configuration mismatches
- **Security Posture:** MEDIUM with multiple input validation gaps

**Recommended Priority:**

1. Fix login field mismatch (blocks all E2E tests)
2. Configure validation settings in E2E environment
3. Add comprehensive input validation across all endpoints
4. Expand Postman test coverage with Newman CI/CD integration

**Estimated Effort:**

- Critical fixes: 2-4 hours
- High priority validation: 1-2 days
- Comprehensive Postman collection: 2-3 days
- Full hardening implementation: 1-2 weeks

---

**Report End**

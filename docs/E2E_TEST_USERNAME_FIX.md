# E2E Test Username Fix - Implementation Guide

**Issue**: E2E tests failing with 409 Conflict due to username length exceeding database VARCHAR(50) limit

**Root Cause**: `testuser_TestVideoUploadWorkflow_1732157256893097400` = 52 characters (exceeds limit by 2)

---

## Option 1: Shorten Username Generation (RECOMMENDED)

### Implementation

```go
// tests/e2e/scenarios/video_workflow_test.go

// BEFORE (52+ chars):
timestamp := time.Now().UnixNano()
username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)

// AFTER (guaranteed < 50 chars):
timestamp := time.Now().UnixNano()
// Extract just the test function name, remove package prefix
testName := filepath.Base(t.Name())
// Use modulo to shorten timestamp (still unique enough for tests)
shortTimestamp := timestamp % 10000000000 // Last 10 digits
username := fmt.Sprintf("u_%s_%d", testName, shortTimestamp)
```

### Example Outputs

| Before | After | Length |
|--------|-------|--------|
| `testuser_TestVideoUploadWorkflow_1732157256893097400` (52) | `u_TestVideoUploadWorkflow_3097400` (31) | ✅ 31 |
| `authtest_TestUserAuthenticationFlow_1732157256959470600` (55) | `u_TestUserAuthenticationFlow_9470600` (35) | ✅ 35 |
| `searchtest_TestVideoSearchFunctionality_1732157256774459700` (58) | `u_TestVideoSearchFunctionality_4459700` (39) | ✅ 39 |

### Pros

- Maintains readability
- Keeps test name in username
- Still unique (10-digit timestamp + test name)
- Works with existing test infrastructure

### Cons

- Slightly less readable than current format
- Need to update all test files

---

## Option 2: Use UUID-Based Usernames

### Implementation

```go
import "github.com/google/uuid"

// Short, guaranteed unique
username := fmt.Sprintf("test_%s", uuid.New().String()[:8])
// Example: test_a1b2c3d4 (13 chars)
```

### Pros

- Very short (13 chars)
- Guaranteed unique
- No collision risk

### Cons

- Less readable in logs
- Can't identify which test created the user
- Need to import uuid package

---

## Option 3: Sequential Counter

### Implementation

```go
var testCounter atomic.Int64

func generateTestUsername(t *testing.T) string {
    counter := testCounter.Add(1)
    testName := filepath.Base(t.Name())
    // Limit test name to 30 chars
    if len(testName) > 30 {
        testName = testName[:30]
    }
    return fmt.Sprintf("t_%s_%d", testName, counter)
}
```

### Pros

- Deterministic ordering
- Easy to debug
- Short usernames

### Cons

- Requires shared counter state
- Counter resets between test runs
- More complex implementation

---

## Option 4: Hash-Based Usernames

### Implementation

```go
import (
    "crypto/sha256"
    "encoding/hex"
)

func generateTestUsername(t *testing.T) string {
    // Combine test name + timestamp
    data := fmt.Sprintf("%s_%d", t.Name(), time.Now().UnixNano())
    hash := sha256.Sum256([]byte(data))
    // Use first 12 chars of hex hash
    return fmt.Sprintf("test_%s", hex.EncodeToString(hash[:])[:12])
}
// Example: test_a1b2c3d4e5f6 (17 chars)
```

### Pros

- Always same length (17 chars)
- Derived from test name (deterministic for same test+time)
- No collision risk

### Cons

- Not human-readable
- Requires crypto/sha256 import

---

## Recommended Fix (Option 1 with Safety Check)

### File: `/home/user/vidra/tests/e2e/helpers.go`

Add a helper function:

```go
package e2e

import (
    "fmt"
    "path/filepath"
    "testing"
    "time"
)

const MaxUsernameLength = 50

// GenerateTestUsername creates a unique username for E2E tests
// that respects the database VARCHAR(50) constraint
func GenerateTestUsername(t *testing.T, prefix string) string {
    // Get short test name (remove package path)
    testName := filepath.Base(t.Name())

    // Use nanosecond timestamp modulo to keep it shorter
    timestamp := time.Now().UnixNano() % 10000000000 // 10 digits

    // Build username
    username := fmt.Sprintf("%s_%s_%d", prefix, testName, timestamp)

    // Safety check: ensure we don't exceed database limit
    if len(username) > MaxUsernameLength {
        // Truncate test name if needed
        maxTestNameLen := MaxUsernameLength - len(prefix) - 1 - 10 - 1 // prefix_XXX_timestamp
        if maxTestNameLen > 0 && len(testName) > maxTestNameLen {
            testName = testName[:maxTestNameLen]
        }
        username = fmt.Sprintf("%s_%s_%d", prefix, testName, timestamp)
    }

    // Final safety check
    if len(username) > MaxUsernameLength {
        t.Fatalf("Generated username exceeds max length: %d > %d", len(username), MaxUsernameLength)
    }

    t.Logf("Generated username: %s (length: %d)", username, len(username))
    return username
}
```

### File: `/home/user/vidra/tests/e2e/scenarios/video_workflow_test.go`

Update all tests to use the helper:

```go
// BEFORE:
timestamp := time.Now().UnixNano()
username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)

// AFTER:
username := e2e.GenerateTestUsername(t, "test")
```

### Complete diff for video_workflow_test.go

```diff
--- a/tests/e2e/scenarios/video_workflow_test.go
+++ b/tests/e2e/scenarios/video_workflow_test.go
@@ -34,9 +34,7 @@ func TestVideoUploadWorkflow(t *testing.T) {
  client := e2e.NewTestClient(cfg.BaseURL)

- // Step 1: Register a new user with unique username (test name + nanosecond timestamp)
- timestamp := time.Now().UnixNano()
- username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)
+ username := e2e.GenerateTestUsername(t, "test")
  email := username + "@example.com"
  password := "SecurePass123!"

@@ -111,9 +109,7 @@ func TestUserAuthenticationFlow(t *testing.T) {

  client := e2e.NewTestClient(cfg.BaseURL)

- // Step 1: Register a new user with unique username (test name + nanosecond timestamp)
- timestamp := time.Now().UnixNano()
- username := fmt.Sprintf("authtest_%s_%d", t.Name(), timestamp)
+ username := e2e.GenerateTestUsername(t, "auth")
  email := username + "@example.com"
  password := "SecurePass123!"

@@ -160,9 +156,7 @@ func TestVideoSearchFunctionality(t *testing.T) {

  client := e2e.NewTestClient(cfg.BaseURL)

- // Register user with unique username (test name + nanosecond timestamp)
- timestamp := time.Now().UnixNano()
- username := fmt.Sprintf("searchtest_%s_%d", t.Name(), timestamp)
+ username := e2e.GenerateTestUsername(t, "search")
  email := username + "@example.com"
  client.RegisterUser(t, username, email, "SecurePass123!")
```

---

## Additional API-Level Fix

### File: `/home/user/vidra/internal/httpapi/handlers.go`

Add validation before database insertion:

```go
// Add after line 197 (after checking for empty fields)

const MaxUsernameLength = 50
const MaxDisplayNameLength = 100

// Validate username length
if len(req.Username) > MaxUsernameLength {
    shared.WriteError(w, http.StatusBadRequest,
        domain.NewDomainError("INVALID_USERNAME",
            fmt.Sprintf("Username must be %d characters or less (got %d)", MaxUsernameLength, len(req.Username))))
    return
}

// Validate username characters (alphanumeric, underscore, hyphen only)
if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(req.Username) {
    shared.WriteError(w, http.StatusBadRequest,
        domain.NewDomainError("INVALID_USERNAME",
            "Username can only contain letters, numbers, underscores, and hyphens"))
    return
}

// Validate display name length if provided
if req.DisplayName != nil && len(*req.DisplayName) > MaxDisplayNameLength {
    shared.WriteError(w, http.StatusBadRequest,
        domain.NewDomainError("INVALID_DISPLAY_NAME",
            fmt.Sprintf("Display name must be %d characters or less", MaxDisplayNameLength)))
    return
}
```

### Complete diff for handlers.go

```diff
--- a/internal/httpapi/handlers.go
+++ b/internal/httpapi/handlers.go
@@ -3,6 +3,7 @@ package httpapi
 import (
  "encoding/json"
  "net/http"
+ "regexp"
  "time"

  "github.com/google/uuid"
@@ -186,6 +187,8 @@ func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
 // Register implements ServerInterface.Register
 func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
+ const MaxUsernameLength = 50
+ const MaxDisplayNameLength = 100
  var req generated.RegisterRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
   shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
@@ -197,6 +200,25 @@ func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
   return
  }

+ // Validate username length
+ if len(req.Username) > MaxUsernameLength {
+  shared.WriteError(w, http.StatusBadRequest,
+   domain.NewDomainError("INVALID_USERNAME",
+    fmt.Sprintf("Username must be %d characters or less (got %d)", MaxUsernameLength, len(req.Username))))
+  return
+ }
+
+ // Validate username format
+ if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(req.Username) {
+  shared.WriteError(w, http.StatusBadRequest,
+   domain.NewDomainError("INVALID_USERNAME",
+    "Username can only contain letters, numbers, underscores, and hyphens"))
+  return
+ }
+
+ // Additional validations can be added here:
+ // - Email format validation
+ // - Password strength requirements
+
  // Optional pre-check for clearer 409s
  if s.userRepo != nil {
   if _, err := s.userRepo.GetByEmail(r.Context(), req.Email); err == nil {
```

---

## Validation Tests

Add unit tests to verify the fix:

### File: `/home/user/vidra/internal/httpapi/handlers_test.go`

```go
func TestRegister_UsernameTooLong(t *testing.T) {
    // Username with 51 characters
    username := strings.Repeat("a", 51)

    req := RegisterRequest{
        Username: username,
        Email:    "test@example.com",
        Password: "SecurePass123!",
    }

    resp := testRegister(t, req)
    assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

    var body ErrorResponse
    json.NewDecoder(resp.Body).Decode(&body)
    assert.Equal(t, "INVALID_USERNAME", body.Error.Code)
    assert.Contains(t, body.Error.Message, "50 characters")
}

func TestRegister_UsernameMaxLength(t *testing.T) {
    // Username with exactly 50 characters
    username := strings.Repeat("a", 50)

    req := RegisterRequest{
        Username: username,
        Email:    "test@example.com",
        Password: "SecurePass123!",
    }

    resp := testRegister(t, req)
    assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestRegister_UsernameInvalidChars(t *testing.T) {
    tests := []struct {
        name     string
        username string
    }{
        {"forward slash", "test/user"},
        {"at symbol", "test@user"},
        {"space", "test user"},
        {"special chars", "test!@#$"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := RegisterRequest{
                Username: tt.username,
                Email:    "test@example.com",
                Password: "SecurePass123!",
            }

            resp := testRegister(t, req)
            assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
        })
    }
}
```

---

## Testing the Fix

### 1. Run Unit Tests

```bash
cd /home/user/vidra
go test ./internal/httpapi -v -run TestRegister
```

### 2. Run E2E Tests

```bash
cd /home/user/vidra
go test ./tests/e2e/scenarios -v
```

### 3. Run Postman Collection

```bash
cd /home/user/vidra
newman run postman/vidra-registration-edge-cases.postman_collection.json \
  --environment postman/environments/local.postman_environment.json
```

### 4. Verify with Manual Test

```bash
# Test with exactly 50 chars
curl -X POST http://localhost:18080/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "12345678901234567890123456789012345678901234567890",
    "email": "test50@example.com",
    "password": "SecurePass123!"
  }'

# Should succeed with 201

# Test with 51 chars
curl -X POST http://localhost:18080/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "123456789012345678901234567890123456789012345678901",
    "email": "test51@example.com",
    "password": "SecurePass123!"
  }'

# Should fail with 400 INVALID_USERNAME
```

---

## Migration Strategy

### Phase 1: Immediate (Hot Fix)

1. ✅ Add API-level validation for username length (blocks bad requests)
2. ✅ Update E2E tests to use GenerateTestUsername helper
3. ✅ Deploy to staging and verify

### Phase 2: Short Term

1. Add comprehensive unit tests for all validation rules
2. Add email format validation
3. Add password strength requirements
4. Update OpenAPI spec with validation constraints
5. Add monitoring for validation failures

### Phase 3: Long Term

1. Implement username normalization (case-insensitive)
2. Add CAPTCHA for public registration
3. Implement progressive rate limiting
4. Add abuse detection (repeated registration attempts)

---

## Monitoring & Alerts

### Metrics to Track

```promql
# Registration failures by error code
registration_errors_total{code="INVALID_USERNAME"}

# Username length distribution
histogram_quantile(0.95, registration_username_length_bucket)

# Rate of 409 conflicts
rate(registration_errors_total{code="USER_EXISTS"}[5m])
```

### Alerts

```yaml
- alert: HighUsernameValidationFailures
  expr: rate(registration_errors_total{code="INVALID_USERNAME"}[5m]) > 10
  annotations:
    summary: High rate of username validation failures
    description: May indicate an attack or broken client

- alert: UsernameCollisions
  expr: rate(registration_errors_total{code="USER_EXISTS"}[5m]) > 5
  annotations:
    summary: High rate of username collisions
    description: Check for duplicate registration attempts
```

---

## Rollback Plan

If the fix causes issues:

1. **Revert API validation** (allow long usernames temporarily)

   ```bash
   git revert <commit-hash>
   git push
   ```

2. **Keep E2E test fix** (still necessary to prevent truncation collisions)

3. **Monitor for database errors** related to varchar overflow

4. **Gradual rollout**: Enable validation on 10% of requests, monitor, increase to 100%

---

## Success Criteria

- [ ] All E2E tests pass consistently
- [ ] No 409 errors due to username truncation
- [ ] Usernames with 51+ chars return 400 Bad Request
- [ ] Usernames with exactly 50 chars are accepted
- [ ] Special characters in usernames are properly validated
- [ ] Newman Postman tests all pass
- [ ] No new security vulnerabilities introduced

---

## Related Files

- `/home/user/vidra/docs/security/USER_REGISTRATION_409_ANALYSIS.md` - Full vulnerability analysis
- `/home/user/vidra/postman/vidra-registration-edge-cases.postman_collection.json` - Test collection
- `/home/user/vidra/.github/workflows/registration-api-tests.yml` - CI/CD pipeline
- `/home/user/vidra/migrations/002_create_users_table.sql` - Database schema
- `/home/user/vidra/tests/e2e/scenarios/video_workflow_test.go` - E2E tests
- `/home/user/vidra/internal/httpapi/handlers.go` - Registration handler

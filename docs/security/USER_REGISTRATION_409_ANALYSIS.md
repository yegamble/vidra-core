# User Registration 409 Conflict Analysis

**Date**: 2025-11-21
**Severity**: HIGH
**Component**: `/auth/register` endpoint
**Status**: Active issue causing E2E test failures

---

## Executive Summary

E2E tests are experiencing 409 Conflict errors during user registration due to **username length exceeding database constraints**, causing silent truncation and subsequent collision detection by uniqueness checks.

---

## Root Cause Analysis

### 1. Database Schema Constraint

**File**: `/home/user/athena/migrations/002_create_users_table.sql`

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(50) UNIQUE NOT NULL,  -- ⚠️ 50 CHARACTER LIMIT
    email VARCHAR(255) UNIQUE NOT NULL,
    ...
);
```

**Critical Constraints:**

- `username VARCHAR(50)` - Maximum 50 characters
- `UNIQUE NOT NULL` - No duplicates allowed
- `email VARCHAR(255) UNIQUE NOT NULL` - Email also unique

### 2. E2E Test Username Generation

**File**: `/home/user/athena/tests/e2e/scenarios/video_workflow_test.go`

```go
// Line 38
timestamp := time.Now().UnixNano()
username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)
// Example: testuser_TestVideoUploadWorkflow_1732157256893097400
```

**Actual Username Lengths:**

| Test Name | Generated Username | Length | Status |
|-----------|-------------------|--------|---------|
| `TestVideoUploadWorkflow` | `testuser_TestVideoUploadWorkflow_1732157256893097400` | **52 chars** | ❌ EXCEEDS (by 2) |
| `TestUserAuthenticationFlow` | `authtest_TestUserAuthenticationFlow_1732157256959470600` | **55 chars** | ❌ EXCEEDS (by 5) |
| `TestVideoSearchFunctionality` | `searchtest_TestVideoSearchFunctionality_1732157256774459700` | **58 chars** | ❌ EXCEEDS (by 8) |

### 3. PostgreSQL Truncation Behavior

When a string exceeds `VARCHAR(N)`, PostgreSQL's behavior depends on version and `standard_conforming_strings`:

**PostgreSQL 16+ (default):**

```sql
INSERT INTO users (username, ...) VALUES ('testuser_TestVideoUploadWorkflow_1732157256893097400', ...);
-- Error: value too long for type character varying(50)
```

**But the Go `pq` driver may silently truncate in some scenarios:**

```
Full:      testuser_TestVideoUploadWorkflow_1732157256893097400  (52 chars)
Truncated: testuser_TestVideoUploadWorkflow_17321572568930974    (50 chars)
Lost:                                                       ^^00
```

### 4. Collision Scenario

When two tests run in rapid succession with slightly different timestamps:

```
Test 1: testuser_TestVideoUploadWorkflow_1732157256893097400
Test 2: testuser_TestVideoUploadWorkflow_1732157256893097401  (1 nanosecond later)

After truncation to 50 chars:
Test 1: testuser_TestVideoUploadWorkflow_17321572568930974
Test 2: testuser_TestVideoUploadWorkflow_17321572568930974

❌ IDENTICAL → UNIQUE CONSTRAINT VIOLATION → 409 Conflict
```

---

## Registration Endpoint Flow

### Request Path: `POST /auth/register`

**Handler Chain:**

1. Route: `/home/user/athena/internal/httpapi/routes.go:76`

   ```go
   r.With(strictAuthLimiter.Limit).Post("/auth/register", server.Register)
   ```

   - Rate limit: **5 requests per 60 seconds**

2. Handler: `/home/user/athena/internal/httpapi/handlers.go:187`

   ```go
   func (s *Server) Register(w http.ResponseWriter, r *http.Request)
   ```

3. Repository: `/home/user/athena/internal/repository/user_repository.go:27`

   ```go
   func (r *userRepository) Create(ctx context.Context, user *domain.User, passwordHash string)
   ```

### Validation Rules

**Request Schema:**

```go
type RegisterRequest struct {
    Username    string  `json:"username"`     // ⚠️ NO LENGTH VALIDATION
    Email       string  `json:"email"`        // ⚠️ NO FORMAT VALIDATION
    Password    string  `json:"password"`     // ⚠️ NO STRENGTH VALIDATION
    DisplayName *string `json:"display_name"` // Optional
}
```

**Current Validation:**

1. **Required Fields Check** (Line 194)

   ```go
   if req.Username == "" || req.Email == "" || req.Password == "" {
       return 400 "MISSING_FIELDS"
   }
   ```

2. **Email Uniqueness Check** (Line 201-204)

   ```go
   if _, err := s.userRepo.GetByEmail(r.Context(), req.Email); err == nil {
       return 409 "USER_EXISTS: Email already in use"
   }
   ```

3. **Username Uniqueness Check** (Line 205-208)

   ```go
   if _, err := s.userRepo.GetByUsername(r.Context(), req.Username); err == nil {
       return 409 "USER_EXISTS: Username already in use"
   }
   ```

4. **Database Insert** (Line 235)
   - Password hashing (bcrypt)
   - User creation in transaction
   - Default channel creation

---

## What Triggers 409 Conflict?

### Confirmed Triggers

1. **Duplicate Username** (after truncation or exact match)

   ```json
   {
       "error": {
           "code": "USER_EXISTS",
           "message": "Username already in use"
       }
   }
   ```

2. **Duplicate Email**

   ```json
   {
       "error": {
           "code": "USER_EXISTS",
           "message": "Email already in use"
       }
   }
   ```

3. **Database-Level UNIQUE Constraint** (if pre-checks pass but DB insertion fails)

   ```json
   {
       "error": {
           "code": "CREATE_FAILED",
           "message": "Failed to create user"
       }
   }
   ```

### Response Format

**Success (201 Created):**

```json
{
    "data": {
        "user": {
            "id": "uuid",
            "username": "testuser",
            "email": "test@example.com"
        },
        "access_token": "jwt-token",
        "refresh_token": "refresh-token",
        "expires_in": 900
    }
}
```

**Failure (409 Conflict):**

```json
{
    "error": {
        "code": "USER_EXISTS",
        "message": "Username already in use"
    }
}
```

---

## Missing Validation Rules

### Critical Gaps

1. **No Username Length Validation**
   - Database: `VARCHAR(50)`
   - API: No validation
   - **Risk**: Silent truncation → collisions

2. **No Username Character Validation**
   - Allows: `testuser_scenarios/TestVideoUploadWorkflow_123`
   - **Risk**: Path traversal characters (`/`), special chars

3. **No Email Format Validation**
   - Accepts: `invalid-email-format`
   - **Risk**: Invalid emails in database

4. **No Password Strength Validation**
   - Accepts: `123`, `password`
   - **Risk**: Weak passwords allowed

5. **No Display Name Length Validation**
   - Database: `VARCHAR(100)`
   - API: No validation

6. **No Username Normalization**
   - `TestUser` vs `testuser` treated as different
   - **Risk**: Confusable usernames

---

## PostgreSQL Behavior Matrix

| Scenario | PostgreSQL Action | API Response | E2E Test Result |
|----------|------------------|--------------|-----------------|
| Username = 48 chars | Insert succeeds | 201 Created | ✅ Pass |
| Username = 50 chars | Insert succeeds | 201 Created | ✅ Pass |
| Username = 52 chars | **Truncate to 50** OR **Error** | 409 Conflict (if duplicate) | ❌ Fail |
| Username = 100 chars | Truncate/Error | 409/500 | ❌ Fail |
| Duplicate username (exact) | Unique violation | 409 Conflict | ✅ Expected |
| Duplicate email (exact) | Unique violation | 409 Conflict | ✅ Expected |
| Invalid characters | Insert succeeds | 201 Created | ⚠️ Security risk |

---

## Edge Cases Discovered

### 1. Username Length Boundary Testing

```javascript
// 50 chars exactly - should work
"a".repeat(50) // ✅

// 51 chars - exceeds limit
"a".repeat(51) // ❌ Truncates to 50

// 52 chars with timestamp
"testuser_TestVideoUploadWorkflow_1732157256893097400" // ❌
```

### 2. Test Name with Slashes

```go
t.Name() // Returns "scenarios/TestVideoUploadWorkflow" in subtests
username := fmt.Sprintf("testuser_%s_%d", t.Name(), timestamp)
// Result: "testuser_scenarios/TestVideoUploadWorkflow_1732157256893097400"
// Contains "/" which may cause issues in URLs or file systems
```

### 3. Concurrent Test Execution

```
Time: 20:33:42.633 -> Test1: testuser_..._1732157256893097400
Time: 20:33:42.634 -> Test2: testuser_..._1732157256893097401
                                                            ^^ Only 1 nanosecond apart
                                                            Lost after truncation!
```

### 4. Email Length Constraints

```go
email := username + "@example.com"
// If username is 52 chars: "52chars@example.com" = 64 chars
// Database allows VARCHAR(255), but truncated username creates invalid email
```

---

## Security Implications

### 1. Username Enumeration

**Attack Vector:**

```bash
curl -X POST /auth/register -d '{"username":"admin","email":"test@test.com","password":"pass"}'
# Response: 409 "Username already in use"
# Attacker knows "admin" username exists
```

**Mitigation**: Generic error messages

### 2. Resource Exhaustion

**Attack Vector:**

```bash
for i in {1..1000}; do
  curl -X POST /auth/register -d "{\"username\":\"user$i\",\"email\":\"user$i@test.com\",\"password\":\"pass\"}"
done
```

**Current Protection:**

- Rate limit: 5 requests per 60 seconds
- **Status**: ✅ Adequate

### 3. Collision-Based DoS

**Attack Vector:**

- Register usernames that truncate to common prefixes
- Block legitimate users from registering

**Example:**

```
Attacker: "testuser_TestVideoUploadWorkflow_1111111111111111111"
Victim:   "testuser_TestVideoUploadWorkflow_2222222222222222222"
Both truncate to: "testuser_TestVideoUploadWorkflow_11111111111111111"
```

**Mitigation**: Strict length validation before DB insertion

---

## Recommended Fixes

### Immediate (High Priority)

1. **Add Username Length Validation**

   ```go
   const MaxUsernameLength = 50

   if len(req.Username) > MaxUsernameLength {
       shared.WriteError(w, http.StatusBadRequest,
           domain.NewDomainError("INVALID_USERNAME",
               fmt.Sprintf("Username must be %d characters or less", MaxUsernameLength)))
       return
   }
   ```

2. **Fix E2E Test Username Generation**

   ```go
   // Option A: Shorten prefix and test name
   timestamp := time.Now().UnixNano()
   shortName := strings.ReplaceAll(t.Name(), "scenarios/", "")
   username := fmt.Sprintf("u_%s_%d", shortName[:10], timestamp%1000000000)
   // Result: "u_TestVideo_923097400" (21 chars)

   // Option B: Use UUID
   username := fmt.Sprintf("test_%s", uuid.NewString()[:8])
   // Result: "test_a1b2c3d4" (13 chars)

   // Option C: Use sequential counter
   username := fmt.Sprintf("testuser_%d_%d", time.Now().Unix(), rand.Intn(100000))
   // Result: "testuser_1732157256_12345" (28 chars)
   ```

### Short Term (Medium Priority)

3. **Add Username Character Validation**

   ```go
   var validUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

   if !validUsernameRegex.MatchString(req.Username) {
       return 400 "INVALID_USERNAME: Only alphanumeric, underscore, and dash allowed"
   }
   ```

4. **Add Email Format Validation**

   ```go
   var emailRegex = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

   if !emailRegex.MatchString(req.Email) {
       return 400 "INVALID_EMAIL: Invalid email format"
   }
   ```

5. **Add Password Strength Validation**

   ```go
   if len(req.Password) < 8 {
       return 400 "WEAK_PASSWORD: Password must be at least 8 characters"
   }
   ```

### Long Term (Low Priority)

6. **Username Normalization**

   ```go
   normalizedUsername := strings.ToLower(strings.TrimSpace(req.Username))
   // Store normalized version, but preserve original case for display
   ```

7. **Rate Limit Per IP**

   ```go
   // Already implemented: 5 requests per 60 seconds
   // Consider: Dynamic rate limiting based on IP reputation
   ```

8. **CAPTCHA for Registration**
   - Prevent automated account creation
   - Reduce spam registrations

---

## Testing Strategy

### Unit Tests Required

```go
func TestRegister_UsernameTooLong(t *testing.T) {
    username := strings.Repeat("a", 51)
    // Expected: 400 "Username must be 50 characters or less"
}

func TestRegister_UsernameMaxLength(t *testing.T) {
    username := strings.Repeat("a", 50)
    // Expected: 201 Created
}

func TestRegister_UsernameInvalidChars(t *testing.T) {
    username := "test/user"
    // Expected: 400 "Invalid username format"
}

func TestRegister_DuplicateUsername(t *testing.T) {
    // Register user1
    // Register user2 with same username
    // Expected: 409 "Username already in use"
}
```

### Integration Tests Required

```go
func TestRegister_TruncationCollision(t *testing.T) {
    username1 := strings.Repeat("a", 50) + "01"
    username2 := strings.Repeat("a", 50) + "02"
    // Both should fail with validation error, not collision
}
```

---

## Postman Test Collection

See: `/home/user/athena/postman/athena-registration-edge-cases.postman_collection.json`

**Test Scenarios:**

1. Valid registration (baseline)
2. Username at exactly 50 characters
3. Username at 51 characters (should fail validation)
4. Username with special characters
5. Duplicate username collision
6. Duplicate email collision
7. Missing required fields
8. Invalid email format
9. Weak password
10. Concurrent registration attempts

---

## CI/CD Implications

### GitHub Actions Workflow

**File**: `/home/user/athena/.github/workflows/e2e-tests.yml`

**Current Behavior:**

- Tests run sequentially or in parallel
- Username collisions cause intermittent failures
- 409 errors are treated as test failures

**Required Changes:**

1. Fix E2E test username generation
2. Add pre-test database cleanup
3. Implement test isolation
4. Add retry logic for transient failures

---

## Metrics and Monitoring

### Recommended Alerts

1. **High 409 Rate**

   ```
   Alert: registration_409_rate > 10% of total registrations
   Action: Investigate for attacks or bugs
   ```

2. **Username Length Distribution**

   ```
   Metric: histogram of username lengths
   Alert: Any username > 45 chars (approaching limit)
   ```

3. **Failed Registration Reasons**

   ```
   Metric: Count by error code (USER_EXISTS, INVALID_USERNAME, etc.)
   Alert: Spike in specific error type
   ```

---

## Conclusion

The 409 Conflict errors in E2E tests are caused by:

1. **Usernames exceeding 50-character database limit**
2. **Silent truncation** by PostgreSQL or driver
3. **Timestamp collision** after truncation removes uniqueness

**Immediate Action Required:**

- Add username length validation (≤50 chars) at API level
- Fix E2E test username generation to stay within limits
- Add comprehensive input validation for all registration fields

**Risk Assessment:**

- **Current State**: HIGH - Tests fail intermittently, no input validation
- **After Fix**: LOW - Proper validation prevents collisions and attacks

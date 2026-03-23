# Postman E2E Test Scenarios - Vidra Core API

**Purpose:** Comprehensive Postman test collection for breaking the API and validating edge cases
**Target:** Vidra Core video platform API
**Date:** 2025-11-23

---

## Collection Variables

```json
{
  "baseUrl": "http://localhost:18080",
  "accessToken": "",
  "refreshToken": "",
  "userId": "",
  "testVideoId": "",
  "testSessionId": "",
  "testUsername": "e2e_{{$timestamp}}",
  "testEmail": "e2e_{{$timestamp}}@example.com",
  "testPassword": "SecurePass123!"
}
```

---

## 01 - Pre-flight & Health Checks

### Test 1.1: Health Check

```javascript
pm.test("API is healthy", function() {
    pm.response.to.have.status(200);
    const body = pm.response.json();
    pm.expect(body.status).to.equal("healthy");
});

pm.test("Response time is acceptable", function() {
    pm.expect(pm.response.responseTime).to.be.below(1000);
});
```

### Test 1.2: Readiness Check - All Services

```javascript
pm.test("All services are ready", function() {
    pm.response.to.have.status(200);
    const body = pm.response.json();
    pm.expect(body.status).to.equal("ready");
    pm.expect(body.checks.database).to.equal("healthy");
    pm.expect(body.checks.redis).to.equal("healthy");
});

pm.test("Critical services are present", function() {
    const body = pm.response.json();
    pm.expect(body.checks).to.have.property("database");
    pm.expect(body.checks).to.have.property("redis");
});
```

---

## 02 - Authentication Edge Cases

### Test 2.1: Register - Valid User

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "{{testUsername}}",
  "email": "{{testEmail}}",
  "password": "{{testPassword}}",
  "display_name": "Test User"
}

// Tests
pm.test("Registration successful", function() {
    pm.response.to.have.status(201);
});

pm.test("Response contains user data", function() {
    const body = pm.response.json();
    pm.expect(body.data.user).to.exist;
    pm.expect(body.data.user.id).to.exist;
    pm.expect(body.data.user.username).to.equal(pm.collectionVariables.get("testUsername"));
    pm.expect(body.data.user.email).to.equal(pm.collectionVariables.get("testEmail"));
});

pm.test("Access token is provided", function() {
    const body = pm.response.json();
    pm.expect(body.data.access_token).to.exist;
    pm.expect(body.data.refresh_token).to.exist;

    // Save tokens for subsequent tests
    pm.collectionVariables.set("accessToken", body.data.access_token);
    pm.collectionVariables.set("refreshToken", body.data.refresh_token);
    pm.collectionVariables.set("userId", body.data.user.id);
});

pm.test("Response has security headers", function() {
    pm.response.to.have.header("X-Content-Type-Options");
});
```

### Test 2.2: Register - Duplicate Email

```javascript
// Request (same email as 2.1)
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "different_user",
  "email": "{{testEmail}}",
  "password": "{{testPassword}}"
}

// Tests
pm.test("Duplicate email is rejected", function() {
    pm.response.to.have.status(409);
});

pm.test("Error message indicates conflict", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("USER_EXISTS");
    pm.expect(body.error.message).to.include("Email");
});
```

### Test 2.3: Register - Invalid Email Format

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "testuser",
  "email": "not-an-email",
  "password": "SecurePass123!"
}

// Tests
pm.test("Invalid email format is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error code indicates invalid email", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.be.oneOf(["INVALID_EMAIL_FORMAT", "MISSING_FIELDS", "CREATE_FAILED"]);
});
```

### Test 2.4: Register - Email as Number (Type Validation)

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "testuser",
  "email": 12345,
  "password": "SecurePass123!"
}

// Tests
pm.test("Non-string email is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates type mismatch", function() {
    const body = pm.response.json();
    pm.expect(body.error.message).to.exist;
    // Should mention "string" or "invalid type"
});
```

### Test 2.5: Register - Weak Password

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "weakpass_user",
  "email": "weakpass@example.com",
  "password": "123"
}

// Tests
pm.test("Weak password is rejected (if validation exists)", function() {
    // May be 400 if validation exists, or 201 if not implemented yet
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["PASSWORD_TOO_SHORT", "WEAK_PASSWORD"]);
    } else {
        console.warn("WARNING: Weak password was accepted - password strength validation not implemented");
    }
});
```

### Test 2.6: Register - Username with Special Characters

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "../../etc/passwd",
  "email": "pathtraversal@example.com",
  "password": "SecurePass123!"
}

// Tests
pm.test("Path traversal in username is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["INVALID_USERNAME_FORMAT", "INVALID_USERNAME"]);
    } else {
        console.warn("WARNING: Path traversal characters in username were accepted");
    }
});
```

### Test 2.7: Register - XSS in Display Name

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "xss_test_{{$timestamp}}",
  "email": "xss_{{$timestamp}}@example.com",
  "password": "SecurePass123!",
  "display_name": "<script>alert('XSS')</script>"
}

// Tests
pm.test("XSS in display name is sanitized or rejected", function() {
    if (pm.response.code === 201) {
        const body = pm.response.json();
        const displayName = body.data.user.display_name;
        pm.expect(displayName).to.not.include("<script>");
        pm.expect(displayName).to.not.include("</script>");
        console.log("Display name after sanitization:", displayName);
    } else if (pm.response.code === 400) {
        console.log("XSS attempt was rejected (good)");
    }
});
```

### Test 2.8: Register - Extremely Long Fields

```javascript
// Request
POST {{baseUrl}}/auth/register
Content-Type: application/json

{
  "username": "a".repeat(1000),
  "email": "long@example.com",
  "password": "SecurePass123!"
}

// Tests
pm.test("Extremely long username is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates length violation", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.be.oneOf(["USERNAME_TOO_LONG", "INVALID_USERNAME", "CREATE_FAILED"]);
});
```

### Test 2.9: Login - With Email (Correct Field)

```javascript
// Request
POST {{baseUrl}}/auth/login
Content-Type: application/json

{
  "email": "{{testEmail}}",
  "password": "{{testPassword}}"
}

// Tests
pm.test("Login with email succeeds", function() {
    pm.response.to.have.status(200);
});

pm.test("Login returns access token", function() {
    const body = pm.response.json();
    pm.expect(body.data.access_token).to.exist;
    pm.collectionVariables.set("accessToken", body.data.access_token);
});

pm.test("User data matches registration", function() {
    const body = pm.response.json();
    pm.expect(body.data.user.id).to.equal(pm.collectionVariables.get("userId"));
});
```

### Test 2.10: Login - With Username (Wrong Field - Should Fail)

```javascript
// Request
POST {{baseUrl}}/auth/login
Content-Type: application/json

{
  "username": "{{testUsername}}",
  "password": "{{testPassword}}"
}

// Tests
pm.test("Login with username instead of email fails", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates missing credentials", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("MISSING_CREDENTIALS");
    pm.expect(body.error.message).to.include("Email");
});
```

### Test 2.11: Login - Wrong Password

```javascript
// Request
POST {{baseUrl}}/auth/login
Content-Type: application/json

{
  "email": "{{testEmail}}",
  "password": "WrongPassword123!"
}

// Tests
pm.test("Login with wrong password fails", function() {
    pm.response.to.have.status(401);
});

pm.test("Error is generic to prevent enumeration", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("INVALID_CREDENTIALS");
    // Should NOT reveal if email exists or password is wrong
});
```

### Test 2.12: Login - Non-existent User

```javascript
// Request
POST {{baseUrl}}/auth/login
Content-Type: application/json

{
  "email": "nonexistent@example.com",
  "password": "SomePassword123!"
}

// Tests
pm.test("Login for non-existent user fails", function() {
    pm.response.to.have.status(401);
});

pm.test("Error message is same as wrong password", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("INVALID_CREDENTIALS");
    // Prevents user enumeration
});
```

### Test 2.13: Protected Endpoint - No Token

```javascript
// Request
GET {{baseUrl}}/api/v1/users/me
// No Authorization header

// Tests
pm.test("Protected endpoint rejects missing token", function() {
    pm.response.to.have.status(401);
});
```

### Test 2.14: Protected Endpoint - Invalid Token

```javascript
// Request
GET {{baseUrl}}/api/v1/users/me
Authorization: Bearer invalid_token_12345

// Tests
pm.test("Protected endpoint rejects invalid token", function() {
    pm.response.to.have.status(401);
});
```

---

## 03 - Video Upload Validation

### Test 3.1: Initiate Upload - Valid Request

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "test_video.mp4",
  "file_size": 1048576,
  "chunk_size": 10485760
}

// Tests
pm.test("Upload initiation succeeds", function() {
    pm.response.to.have.status(201);
});

pm.test("Response contains session ID", function() {
    const body = pm.response.json();
    pm.expect(body.data.session_id).to.exist;
    pm.expect(body.data.total_chunks).to.be.a('number');
    pm.collectionVariables.set("testSessionId", body.data.session_id);
});

pm.test("Total chunks calculation is correct", function() {
    const body = pm.response.json();
    const expectedChunks = Math.ceil(1048576 / 10485760);
    pm.expect(body.data.total_chunks).to.equal(expectedChunks);
});
```

### Test 3.2: Initiate Upload - Missing Filename

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "file_size": 1048576,
  "chunk_size": 10485760
}

// Tests
pm.test("Missing filename is rejected", function() {
    pm.response.to.have.status(400);
});
```

### Test 3.3: Initiate Upload - Zero File Size

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "test.mp4",
  "file_size": 0,
  "chunk_size": 10485760
}

// Tests
pm.test("Zero file size is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["INVALID_FILE_SIZE", "FILE_SIZE_INVALID"]);
    } else {
        console.warn("WARNING: Zero file size was accepted");
    }
});
```

### Test 3.4: Initiate Upload - Negative File Size

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "test.mp4",
  "file_size": -1,
  "chunk_size": 10485760
}

// Tests
pm.test("Negative file size is rejected", function() {
    pm.response.to.have.status(400);
});
```

### Test 3.5: Initiate Upload - File Size Exceeds Limit

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "huge_video.mp4",
  "file_size": 10737418240,
  "chunk_size": 10485760
}

// Tests
pm.test("File size exceeding limit is rejected (if validation exists)", function() {
    // MAX_UPLOAD_SIZE default is 5GB
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["FILE_TOO_LARGE", "EXCEEDS_LIMIT"]);
    } else {
        console.warn("WARNING: File size exceeding limit was accepted");
    }
});
```

### Test 3.6: Initiate Upload - Chunk Size Too Small

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "test.mp4",
  "file_size": 1048576,
  "chunk_size": 1
}

// Tests
pm.test("Chunk size too small is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["CHUNK_SIZE_TOO_SMALL", "INVALID_CHUNK_SIZE"]);
    } else {
        console.warn("WARNING: Very small chunk size was accepted - could cause DoS");
    }
});
```

### Test 3.7: Initiate Upload - Chunk Size Too Large

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "test.mp4",
  "file_size": 1048576,
  "chunk_size": 1073741824
}

// Tests
pm.test("Chunk size too large is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["CHUNK_SIZE_TOO_LARGE", "INVALID_CHUNK_SIZE"]);
    } else {
        console.warn("WARNING: Very large chunk size was accepted - could cause OOM");
    }
});
```

### Test 3.8: Initiate Upload - Path Traversal in Filename

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/initiate
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "filename": "../../../../etc/passwd",
  "file_size": 1048576,
  "chunk_size": 10485760
}

// Tests
pm.test("Path traversal in filename is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["INVALID_FILENAME", "SECURITY_VIOLATION"]);
    } else {
        console.warn("SECURITY WARNING: Path traversal in filename was accepted!");
    }
});
```

---

## 04 - Chunked Upload Edge Cases

### Test 4.1: Upload Chunk - Valid Chunk with Checksum

```javascript
// Pre-request Script
const crypto = require('crypto-js');
const chunkData = "test chunk data for upload";
const checksum = crypto.SHA256(chunkData).toString();
pm.collectionVariables.set("chunkChecksum", checksum);

// Request
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 0
X-Chunk-Checksum: {{chunkChecksum}}
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Chunk upload succeeds", function() {
    pm.response.to.have.status(200);
});

pm.test("Response confirms chunk acceptance", function() {
    const body = pm.response.json();
    pm.expect(body.data.chunk_index).to.equal(0);
    pm.expect(body.data.status).to.be.oneOf(["uploaded", "accepted"]);
});
```

### Test 4.2: Upload Chunk - Without Checksum (Permissive Mode)

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 1
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Chunk upload without checksum (depends on mode)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.equal("MISSING_CHECKSUM");
        console.log("Validation strict mode is ENABLED (good)");
    } else if (pm.response.code === 200) {
        console.warn("WARNING: Chunk without checksum was accepted - strict mode disabled");
    }
});
```

### Test 4.3: Upload Chunk - Invalid Checksum

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 2
X-Chunk-Checksum: invalid_checksum_abc123
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Chunk with invalid checksum is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates checksum mismatch", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.be.oneOf(["CHECKSUM_MISMATCH", "INVALID_CHECKSUM"]);
});
```

### Test 4.4: Upload Chunk - Negative Index

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: -1
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Chunk with negative index is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates invalid index", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.be.oneOf(["INVALID_CHUNK_INDEX", "NEGATIVE_CHUNK_INDEX"]);
});
```

### Test 4.5: Upload Chunk - Index Out of Bounds

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 999999
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Chunk index out of bounds is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["CHUNK_INDEX_OUT_OF_BOUNDS", "INVALID_CHUNK_INDEX"]);
    } else {
        console.warn("WARNING: Out of bounds chunk index was accepted");
    }
});
```

### Test 4.6: Upload Chunk - Invalid Session ID

```javascript
// Request
POST {{baseUrl}}/api/v1/upload/session/invalid-session-id/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 0
Content-Type: application/octet-stream

[Binary chunk data]

// Tests
pm.test("Invalid session ID is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates invalid session", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.be.oneOf(["INVALID_SESSION_ID", "SESSION_NOT_FOUND"]);
});
```

### Test 4.7: Upload Chunk - Duplicate Chunk (Idempotency Test)

```javascript
// Request (upload same chunk twice)
POST {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/chunk
Authorization: Bearer {{accessToken}}
X-Chunk-Index: 0
Content-Type: application/octet-stream

[Same binary chunk data as 4.1]

// Tests
pm.test("Duplicate chunk upload is idempotent", function() {
    // Should succeed (idempotent) or reject gracefully
    pm.expect(pm.response.code).to.be.oneOf([200, 400, 409]);
});

pm.test("If rejected, error is clear", function() {
    if (pm.response.code === 400 || pm.response.code === 409) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["CHUNK_ALREADY_UPLOADED", "DUPLICATE_CHUNK"]);
    }
});
```

### Test 4.8: Get Upload Status

```javascript
// Request
GET {{baseUrl}}/api/v1/upload/session/{{testSessionId}}/status
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Upload status retrieved successfully", function() {
    pm.response.to.have.status(200);
});

pm.test("Status shows uploaded chunks", function() {
    const body = pm.response.json();
    pm.expect(body.data.session_id).to.equal(pm.collectionVariables.get("testSessionId"));
    pm.expect(body.data.uploaded_chunks).to.be.an('array');
    pm.expect(body.data.total_chunks).to.be.a('number');
});
```

---

## 05 - Video CRUD Edge Cases

### Test 5.1: Create Video - Valid Metadata

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "title": "Test Video {{$timestamp}}",
  "description": "This is a test video for E2E testing",
  "privacy": "public",
  "tags": ["test", "e2e"],
  "language": "en"
}

// Tests
pm.test("Video creation succeeds", function() {
    pm.response.to.have.status(201);
});

pm.test("Response contains video ID", function() {
    const body = pm.response.json();
    pm.expect(body.data.id).to.exist;
    pm.collectionVariables.set("testVideoId", body.data.id);
});

pm.test("Video metadata matches request", function() {
    const body = pm.response.json();
    pm.expect(body.data.title).to.include("Test Video");
    pm.expect(body.data.privacy).to.equal("public");
});
```

### Test 5.2: Create Video - Missing Title

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "description": "Video without title",
  "privacy": "public"
}

// Tests
pm.test("Missing title is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates missing title", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("MISSING_TITLE");
});
```

### Test 5.3: Create Video - Title Too Long

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "title": "a".repeat(300),
  "description": "Test",
  "privacy": "public"
}

// Tests
pm.test("Title exceeding length is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["TITLE_TOO_LONG", "VALIDATION_ERROR"]);
    } else {
        console.warn("WARNING: Very long title was accepted");
    }
});
```

### Test 5.4: Create Video - Description Too Long

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "title": "Test Video",
  "description": "a".repeat(6000),
  "privacy": "public"
}

// Tests
pm.test("Description exceeding limit is rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["DESCRIPTION_TOO_LONG", "VALIDATION_ERROR"]);
    } else {
        console.warn("WARNING: Very long description was accepted");
    }
});
```

### Test 5.5: Create Video - Invalid Privacy Value

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "title": "Test Video",
  "description": "Test",
  "privacy": "invalid_privacy"
}

// Tests
pm.test("Invalid privacy value is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates invalid privacy", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("INVALID_PRIVACY");
    pm.expect(body.error.message).to.include("public, unlisted, or private");
});
```

### Test 5.6: Create Video - Too Many Tags

```javascript
// Request
POST {{baseUrl}}/api/v1/videos
Authorization: Bearer {{accessToken}}
Content-Type: application/json

{
  "title": "Test Video",
  "description": "Test",
  "privacy": "public",
  "tags": ["tag1", "tag2", "tag3", "tag4", "tag5", "tag6", "tag7", "tag8", "tag9", "tag10", "tag11"]
}

// Tests
pm.test("Too many tags are rejected (if validation exists)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["TOO_MANY_TAGS", "VALIDATION_ERROR"]);
    } else {
        console.warn("WARNING: More than 10 tags were accepted");
    }
});
```

### Test 5.7: Get Video - By ID

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/{{testVideoId}}
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Video retrieval succeeds", function() {
    pm.response.to.have.status(200);
});

pm.test("Video data is complete", function() {
    const body = pm.response.json();
    pm.expect(body.data.id).to.equal(pm.collectionVariables.get("testVideoId"));
    pm.expect(body.data.title).to.exist;
    pm.expect(body.data.privacy).to.exist;
});
```

### Test 5.8: Get Video - Non-existent ID

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/00000000-0000-0000-0000-000000000000
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Non-existent video returns 404", function() {
    pm.response.to.have.status(404);
});
```

### Test 5.9: Delete Video - Owner

```javascript
// Request
DELETE {{baseUrl}}/api/v1/videos/{{testVideoId}}
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Video deletion succeeds", function() {
    pm.response.to.have.status(204);
});
```

### Test 5.10: Delete Video - Already Deleted (Idempotency)

```javascript
// Request (try to delete same video again)
DELETE {{baseUrl}}/api/v1/videos/{{testVideoId}}
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Deleting already-deleted video returns 404", function() {
    pm.response.to.have.status(404);
});
```

---

## 06 - Search Edge Cases

### Test 6.1: Search - Valid Query

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/search?q=test
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Search returns results", function() {
    pm.response.to.have.status(200);
});

pm.test("Search results are in array", function() {
    const body = pm.response.json();
    pm.expect(body.data).to.be.an('array');
});
```

### Test 6.2: Search - Empty Query

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/search?q=
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Empty search query is rejected", function() {
    pm.response.to.have.status(400);
});

pm.test("Error indicates missing query", function() {
    const body = pm.response.json();
    pm.expect(body.error.code).to.equal("MISSING_QUERY");
});
```

### Test 6.3: Search - Very Long Query

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/search?q={{"a".repeat(1000)}}
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Very long query is handled (rejected or truncated)", function() {
    if (pm.response.code === 400) {
        const body = pm.response.json();
        pm.expect(body.error.code).to.be.oneOf(["QUERY_TOO_LONG", "INVALID_QUERY"]);
    } else if (pm.response.code === 200) {
        console.warn("WARNING: Very long search query was accepted");
    }
});
```

### Test 6.4: Search - Special Characters

```javascript
// Request
GET {{baseUrl}}/api/v1/videos/search?q=%3Cscript%3Ealert('xss')%3C/script%3E
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Search with special characters doesn't break", function() {
    pm.expect(pm.response.code).to.be.oneOf([200, 400]);
});
```

---

## 07 - Concurrency Tests

### Test 7.1: Concurrent Upload Initiations

```javascript
// Script to run 10 simultaneous upload initiation requests
const requests = [];
for (let i = 0; i < 10; i++) {
    requests.push(new Promise((resolve, reject) => {
        pm.sendRequest({
            url: pm.environment.get("baseUrl") + "/api/v1/upload/initiate",
            method: 'POST',
            header: {
                'Authorization': 'Bearer ' + pm.collectionVariables.get("accessToken"),
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
    pm.test("All concurrent uploads initiated successfully", function() {
        pm.expect(responses.every(r => r.code === 201)).to.be.true;
    });

    pm.test("All session IDs are unique", function() {
        const sessionIds = responses.map(r => r.json().data.session_id);
        const uniqueIds = new Set(sessionIds);
        pm.expect(uniqueIds.size).to.equal(10);
    });

    pm.test("No race conditions or conflicts", function() {
        // All responses should have valid structure
        responses.forEach(r => {
            pm.expect(r.json().data.session_id).to.exist;
            pm.expect(r.json().data.total_chunks).to.be.a('number');
        });
    });
});
```

---

## 08 - Performance & Load Tests

### Test 8.1: Response Time Baseline

```javascript
pm.test("API response time is acceptable", function() {
    pm.expect(pm.response.responseTime).to.be.below(500);
});

pm.test("No slow query detected", function() {
    if (pm.response.responseTime > 1000) {
        console.warn("PERFORMANCE WARNING: Request took " + pm.response.responseTime + "ms");
    }
});
```

### Test 8.2: Pagination - Large Offset

```javascript
// Request
GET {{baseUrl}}/api/v1/videos?limit=100&offset=10000
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Large pagination offset handled", function() {
    pm.expect(pm.response.code).to.be.oneOf([200, 400]);
});

pm.test("Response time acceptable even with large offset", function() {
    pm.expect(pm.response.responseTime).to.be.below(2000);
});
```

---

## 09 - Cleanup

### Test 9.1: Delete All Test Videos

```javascript
// Request (in loop)
DELETE {{baseUrl}}/api/v1/videos/{{testVideoId}}
Authorization: Bearer {{accessToken}}

// Tests
pm.test("Cleanup successful", function() {
    pm.expect(pm.response.code).to.be.oneOf([204, 404]);
});
```

---

## Newman CLI Commands

### Run Full Collection

```bash
newman run Vidra Core_E2E.postman_collection.json \\
  --environment E2E_Environment.postman_environment.json \\
  --reporters cli,htmlextra \\
  --reporter-htmlextra-export newman-report.html \\
  --bail
```

### Run Specific Folder (Auth Tests Only)

```bash
newman run Vidra Core_E2E.postman_collection.json \\
  --environment E2E_Environment.postman_environment.json \\
  --folder "02 - Authentication Edge Cases" \\
  --reporters cli
```

### Run with Retry on Failure

```bash
newman run Vidra Core_E2E.postman_collection.json \\
  --environment E2E_Environment.postman_environment.json \\
  --reporters cli \\
  --delay-request 100 \\
  --timeout-request 30000 \\
  --max-retries 2
```

### Run with JSON Output (for CI/CD)

```bash
newman run Vidra Core_E2E.postman_collection.json \\
  --environment E2E_Environment.postman_environment.json \\
  --reporters cli,json \\
  --reporter-json-export newman-results.json
```

---

## CI/CD Integration

### Parse Newman JSON Results

```bash
#!/bin/bash
# parse-newman-results.sh

RESULTS_FILE="newman-results.json"

if [ ! -f "$RESULTS_FILE" ]; then
    echo "ERROR: Newman results file not found"
    exit 1
fi

TOTAL=$(jq '.run.stats.assertions.total' $RESULTS_FILE)
FAILED=$(jq '.run.stats.assertions.failed' $RESULTS_FILE)
PASSED=$(jq '.run.stats.assertions.passed' $RESULTS_FILE)

echo "Total Assertions: $TOTAL"
echo "Passed: $PASSED"
echo "Failed: $FAILED"

if [ "$FAILED" -gt 0 ]; then
    echo "FAILURE: $FAILED assertions failed"
    jq '.run.executions[] | select(.assertions[].error) | {request: .item.name, error: .assertions[].error.message}' $RESULTS_FILE
    exit 1
fi

echo "SUCCESS: All tests passed"
exit 0
```

---

**End of Test Scenarios**

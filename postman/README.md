# Postman Test Files

This directory contains test files and Postman collections for comprehensive API testing.

## Collections Overview

| Collection | Endpoints | Purpose |
|------------|-----------|---------|
| **athena-auth.postman_collection.json** | Auth, Avatar, Videos | Authentication, avatar uploads, video CRUD |
| **athena-uploads.postman_collection.json** ✨ NEW | Uploads, Encoding | Chunked uploads, resume, encoding status |
| **athena-analytics.postman_collection.json** ✨ NEW | Analytics, Views | View tracking, analytics, trending |
| **athena-imports.postman_collection.json** ✨ NEW | Imports | External video imports |

---

## Quick Start

### 1. Install Newman (Postman CLI)

```bash
npm install -g newman
```

### 2. Run Collections

```bash
# Authentication & Basic Features
newman run athena-auth.postman_collection.json -e athena.local.postman_environment.json

# Chunked Uploads & Encoding
newman run athena-uploads.postman_collection.json -e athena.local.postman_environment.json

# Analytics & View Tracking
newman run athena-analytics.postman_collection.json -e athena.local.postman_environment.json

# Video Imports
newman run athena-imports.postman_collection.json -e athena.local.postman_environment.json

# Run All Collections
./run-all-tests.sh
```

---

## Collection Details

### 1. Auth Collection (`athena-auth.postman_collection.json`)

**Coverage**: Authentication, avatar uploads, basic video operations

#### Test Categories

##### **Authentication** (8 tests)

- Register user
- Login (success and failure cases)
- Token refresh (success and error cases)
- Logout

##### **Avatar Upload Testing**

The collection includes comprehensive avatar upload tests covering multiple image formats and security scenarios.

### Test Files

The following test files are used for avatar upload testing:

#### **Valid Image Formats**

- `avatar.png` - PNG format (original test file)
- `avatar.jpg` - JPEG format
- `avatar.webp` - WebP format (modern format)
- `avatar.gif` - GIF format (supports animation)
- `avatar.tiff` - TIFF format (high quality)
- `avatar.heic` - HEIC format (Apple's modern format)

#### **Security Test Files**

- `document.pdf` - PDF file (should be rejected - invalid extension)
- `malware.png` - Executable disguised as PNG (should be rejected - invalid content)

### Test Coverage

The avatar upload tests cover:

#### **✅ Positive Test Cases:**

1. **PNG Upload** - Basic PNG image upload with WebP conversion
2. **JPEG Upload** - JPEG image upload with WebP conversion
3. **WebP Upload** - Direct WebP image upload (no conversion needed)
4. **HEIC Upload** - Apple HEIC format with WebP conversion
5. **GIF Upload** - GIF image upload with WebP conversion
6. **TIFF Upload** - TIFF image upload with WebP conversion

#### **❌ Negative Test Cases:**

1. **Invalid Extension** - PDF file upload (should return 400)
2. **Malicious File** - Executable disguised as image (should return 400)
3. **Missing File** - No file provided (should return 400)
4. **No Authentication** - Missing auth token (should return 401)

### Expected Responses

#### **Successful Upload (200)**

```json
{
  "data": {
    "id": "user-uuid",
    "avatar_ipfs_cid": "bafybeiabc123...",
    "avatar_webp_ipfs_cid": "bafybeiweb456...",
    ...
  },
  "success": true
}
```

#### **IPFS Unavailable (503)**

Tests gracefully handle IPFS service unavailability and continue with warnings.

#### **Security Rejection (400)**

```json
{
  "error": {
    "message": "unsupported image format" | "invalid or corrupted image file"
  },
  "success": false
}
```

#### **Authentication Error (401)**

```json
{
  "error": {
    "message": "Missing or invalid authentication"
  },
  "success": false
}
```

### File Generation

Test files are generated using the Go test utilities:

```bash
go run scripts/create_postman_test_files.go postman
```

### Validation

Validate all test files are present and have correct signatures:

```bash
./scripts/validate_postman_files.sh postman
```

## Usage

1. **Install Newman** (Postman CLI):

   ```bash
   npm install -g newman
   ```

2. **Run Avatar Upload Tests**:

   ```bash
   newman run athena-auth.postman_collection.json \
     -e athena.local.postman_environment.json \
     --folder "Auth" \
     --reporters cli,json \
     --reporter-json-export test-results.json
   ```

3. **Run Specific Avatar Tests**:

   ```bash
   newman run athena-auth.postman_collection.json \
     -e athena.local.postman_environment.json \
     --folder "Auth" \
     --grep "Upload Avatar" \
     --reporters cli
   ```

## Security Features

The avatar upload system includes multiple security layers:

1. **Extension Validation** - Only image extensions allowed
2. **MIME Type Validation** - Content-Type header verification
3. **File Content Validation** - Actual image format verification
4. **HEIC Special Handling** - File signature-based validation for HEIC
5. **Executable Detection** - Rejects executable files disguised as images
6. **Authentication Required** - All uploads require valid JWT tokens

These tests ensure the avatar upload system is both functional and secure against common attack vectors.

---

### 2. Uploads Collection (`athena-uploads.postman_collection.json`) ✨ NEW

**Coverage**: Chunked file uploads, upload session management, encoding status tracking

#### Features Tested

##### **Chunked Upload Workflow** (5 tests)

1. **Initiate Upload** - Create upload session with metadata
2. **Upload Chunk 0** - Upload first 5MB chunk
3. **Get Upload Status** - Check progress and received chunks
4. **Resume Upload Info** - Get list of uploaded/missing chunks
5. **Complete Upload** - Finalize and trigger encoding

##### **Encoding Status Tracking** (3 tests)

- Get encoding status by video ID
- Get encoding status by job ID
- Filter encoding jobs by status (pending, processing, completed, failed)

##### **Error Cases** (3 tests)

- Missing authentication (401)
- Complete with missing chunks (400)
- Invalid session ID (404)

#### Key Features

- ✅ Resume capability for interrupted uploads
- ✅ Progress tracking (percentage, chunks received)
- ✅ Real-time encoding status with variant information
- ✅ Session expiration handling (24 hours)

#### Environment Variables Used

- `upload_session_id` - Set automatically after initiate
- `upload_video_id` - Video UUID for the upload
- `encoding_job_id` - Encoding job identifier
- `total_chunks` - Expected number of chunks

---

### 3. Analytics Collection (`athena-analytics.postman_collection.json`) ✨ NEW

**Coverage**: View tracking, video analytics, trending algorithms, discovery features

#### Features Tested

##### **View Tracking** (3 tests)

1. **Generate Fingerprint** - Create unique viewer fingerprint
2. **Track View with Fingerprint** - Record view with deduplication
3. **Track View without Fingerprint** - Server-side fingerprint generation

##### **Video Analytics** (3 tests)

- Get analytics for monthly period (views, engagement, watch time, traffic sources)
- Get analytics for custom date range
- Get daily statistics for time-series charts

##### **Discovery** (4 tests)

- Get top videos (this week)
- Get top videos (all time)
- Get trending videos
- Get trending videos by category

##### **Error Cases** (3 tests)

- Track view for non-existent video (404)
- Get analytics without ownership (403)
- Get analytics without authentication (401)

#### Key Features

- ✅ Fingerprint-based view deduplication (30-minute window)
- ✅ Geographic distribution and device breakdown
- ✅ Traffic source analysis
- ✅ Trending algorithm with velocity and engagement scoring
- ✅ Watch time and completion rate metrics

#### Analytics Includes

- **Views**: Total, unique, trends, percent change
- **Engagement**: Likes, dislikes, comments, shares, like ratio
- **Watch Time**: Total seconds, average, completion rate
- **Traffic Sources**: Direct, search, external, suggested, embedded
- **Geography**: Country-level view distribution
- **Devices**: Desktop, mobile, tablet, TV breakdown

#### Environment Variables Used

- `viewer_fingerprint` - Set after fingerprint generation
- `test_video_id` - Video UUID for testing (set manually or from upload)

---

### 4. Imports Collection (`athena-imports.postman_collection.json`) ✨ NEW

**Coverage**: External video imports, job management, SSRF protection

#### Features Tested

##### **Import Workflow** (5 tests)

1. **Create Import** - Start import from external URL
2. **List All Imports** - View all user's import jobs
3. **List Imports by Status** - Filter by pending/downloading/completed/failed
4. **Get Import Status** - Track progress with detailed info
5. **Cancel Import** - Cancel pending or in-progress job

##### **Error Cases** (5 tests)

- Create import without authentication (401)
- Create import with invalid URL (400)
- Create import with private IP - SSRF protection (400)
- Get non-existent import (404)
- Cancel already-completed import (400)

#### Key Features

- ✅ Progress tracking (download percentage, bytes transferred)
- ✅ SSRF protection (blocks private IPs, localhost, RFC1918)
- ✅ Rate limiting (10 imports/minute)
- ✅ Support for direct URLs, YouTube, Vimeo, PeerTube
- ✅ Automatic privacy settings and metadata

#### Import Status Values

- `pending` - Waiting to start
- `downloading` - Downloading from source
- `processing` - Processing/transcoding video
- `completed` - Successfully imported (video_id available)
- `failed` - Import failed (error_message available)
- `cancelled` - Cancelled by user

#### Environment Variables Used

- `import_job_id` - Set after creating import
- `access_token` - JWT token (required for all import operations)

---

## Environment Variables

All collections use the `athena.local.postman_environment.json` file:

```json
{
  "baseUrl": "http://localhost:8080",
  "access_token": "",
  "refresh_token": "",
  "test_video_id": "",
  "upload_session_id": "",
  "import_job_id": "",
  "viewer_fingerprint": ""
}
```

**Note**: `access_token` is automatically set after successful login in the auth collection.

---

## Running Tests in CI/CD

### GitHub Actions Example

```yaml
- name: Run Postman Tests
  run: |
    npm install -g newman
    newman run postman/athena-auth.postman_collection.json -e postman/test-env.json --reporters cli,junit
    newman run postman/athena-uploads.postman_collection.json -e postman/test-env.json --reporters cli,junit
    newman run postman/athena-analytics.postman_collection.json -e postman/test-env.json --reporters cli,junit
    newman run postman/athena-imports.postman_collection.json -e postman/test-env.json --reporters cli,junit
```

### Run All Collections Script

Create `run-all-tests.sh`:

```bash
#!/bin/bash
set -e

collections=(
  "athena-auth.postman_collection.json"
  "athena-uploads.postman_collection.json"
  "athena-analytics.postman_collection.json"
  "athena-imports.postman_collection.json"
)

for collection in "${collections[@]}"; do
  echo "Running $collection..."
  newman run "$collection" \
    -e athena.local.postman_environment.json \
    --reporters cli,json \
    --reporter-json-export "results-${collection%.json}.json"
done

echo "All collections completed!"
```

---

## Test Coverage Summary

| Collection | Tests | Endpoints Covered |
|------------|-------|-------------------|
| **Auth** | 61 | Auth (4), Avatar (10), Videos (12) |
| **Uploads** | 11 | Chunked uploads (5), Encoding (3), Errors (3) |
| **Analytics** | 13 | Views (3), Analytics (3), Discovery (4), Errors (3) |
| **Imports** | 10 | Import workflow (5), Errors (5) |
| **TOTAL** | **95** | **~50 unique endpoints** |

---

## Security Testing Highlights

### Auth Collection

- ✅ Magic byte validation for image uploads
- ✅ Extension vs content mismatch detection
- ✅ Executable file rejection
- ✅ Token expiration and refresh
- ✅ Unauthorized access prevention

### Uploads Collection

- ✅ Session expiration (24 hours)
- ✅ Chunk integrity validation
- ✅ Authentication requirements
- ✅ File size limits enforcement

### Analytics Collection

- ✅ View deduplication with fingerprinting
- ✅ Owner-only analytics access (403 for non-owners)
- ✅ Anonymous access for public endpoints

### Imports Collection

- ✅ **SSRF Protection** - Blocks private IPs, localhost, link-local
- ✅ **Rate Limiting** - 10 imports per minute
- ✅ URL validation and sanitization
- ✅ File size limits

---

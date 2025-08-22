# Postman Test Files

This directory contains test files and Postman collections for comprehensive API testing.

## Avatar Upload Testing

The Postman collection includes comprehensive avatar upload tests covering multiple image formats and security scenarios.

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
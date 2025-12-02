# Quick Reference - Security Fixes Implementation Guide

**For:** Backend Developers
**Time Required:** ~1 day
**Difficulty:** Medium

---

## Critical Fix #1: SSRF Protection

### File to Create: `/root/athena/internal/usecase/import/url_validator.go`

```go
package importuc

import (
    "fmt"
    "net"
    "net/url"
    "strings"
)

var BlockedNetworks = []string{
    "10.0.0.0/8",
    "172.16.0.0/12",
    "192.168.0.0/16",
    "127.0.0.0/8",
    "169.254.0.0/16",
    "::1/128",
    "fe80::/10",
    "fc00::/7",
}

func ValidateImportURL(urlStr string) error {
    // 1. Parse URL
    parsedURL, err := url.Parse(urlStr)
    if err != nil {
        return fmt.Errorf("invalid URL format: %w", err)
    }

    // 2. Protocol whitelist - ONLY HTTPS
    if parsedURL.Scheme != "https" {
        return fmt.Errorf("only HTTPS protocol allowed, got: %s", parsedURL.Scheme)
    }

    // 3. URL length limit
    if len(urlStr) > 2048 {
        return fmt.Errorf("URL exceeds maximum length of 2048 characters")
    }

    // 4. Block localhost
    hostname := parsedURL.Hostname()
    if isLocalhost(hostname) {
        return fmt.Errorf("localhost URLs are not allowed")
    }

    // 5. Resolve and check IP
    ips, err := net.LookupIP(hostname)
    if err != nil {
        return fmt.Errorf("failed to resolve hostname: %w", err)
    }

    for _, ip := range ips {
        if isBlockedIP(ip) {
            return fmt.Errorf("URL resolves to blocked IP range: %s", ip)
        }
    }

    return nil
}

func isLocalhost(hostname string) bool {
    lowercase := strings.ToLower(hostname)
    return lowercase == "localhost" ||
        lowercase == "localhost.localdomain" ||
        hostname == "127.0.0.1" ||
        hostname == "::1" ||
        hostname == "0.0.0.0"
}

func isBlockedIP(ip net.IP) bool {
    for _, cidr := range BlockedNetworks {
        _, network, _ := net.ParseCIDR(cidr)
        if network.Contains(ip) {
            return true
        }
    }
    return false
}
```

### File to Modify: `/root/athena/internal/httpapi/handlers/video/import_handlers.go`

Add this validation in the `CreateImport` function BEFORE line 89:

```go
// Line 88: After req.TargetPrivacy check, ADD THIS:

// SECURITY: Validate URL to prevent SSRF attacks
if err := ValidateImportURL(req.SourceURL); err != nil {
    writeError(w, http.StatusBadRequest, "invalid import URL", err)
    return
}
```

---

## Critical Fix #2: File Size Validation

### File to Create: `/root/athena/internal/usecase/import/file_validator.go`

```go
package importuc

import (
    "context"
    "fmt"
    "net/http"
)

const MaxImportFileSize = 5 * 1024 * 1024 * 1024 // 5GB

func ValidateRemoteFileSize(ctx context.Context, url string) error {
    req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }

    client := &http.Client{
        Timeout: 10 * time.Second,
    }

    resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("failed to fetch headers: %w", err)
    }
    defer resp.Body.Close()

    contentLength := resp.ContentLength
    if contentLength < 0 {
        return fmt.Errorf("unable to determine file size")
    }

    if contentLength > MaxImportFileSize {
        return fmt.Errorf("file size %d bytes exceeds maximum allowed size of %d bytes",
            contentLength, MaxImportFileSize)
    }

    return nil
}
```

### Use in Import Service

In your import service, call this before starting download:

```go
// Before downloading
if err := ValidateRemoteFileSize(ctx, importReq.SourceURL); err != nil {
    return nil, domain.ErrImportFileTooLarge
}
```

---

## Critical Fix #3: Input Sanitization

### File to Modify: `/root/athena/internal/httpapi/handlers/social/comments.go`

Add HTML escaping in `CreateComment` function:

```go
import "html"

// Line 52-56: Update validation section
if len(req.Body) == 0 || len(req.Body) > 10000 {
    shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
    return
}

// ADD THIS: Sanitize comment body
req.Body = html.EscapeString(req.Body)
```

### File to Modify: `/root/athena/internal/httpapi/handlers/video/import_handlers.go`

Add privacy validation:

```go
// Line 76-78: Replace with strict validation
validPrivacy := map[string]bool{
    "public":   true,
    "unlisted": true,
    "private":  true,
}

if req.TargetPrivacy == "" {
    req.TargetPrivacy = string(domain.PrivacyPrivate)
} else if !validPrivacy[req.TargetPrivacy] {
    writeError(w, http.StatusBadRequest, "invalid privacy value", nil)
    return
}
```

---

## Testing Your Fixes

### Step 1: Run Unit Tests

```bash
cd /root/athena

# Test URL validator
go test ./internal/usecase/import -v -run TestValidateImportURL

# Test file validator
go test ./internal/usecase/import -v -run TestValidateRemoteFileSize

# Test all
go test ./... -short
```

### Step 2: Run Postman Tests

```bash
# Install Newman if not already installed
npm install -g newman

# Run security tests
newman run postman/athena-edge-cases-security.postman_collection.json \
  -e postman/test-local.postman_environment.json \
  --folder "01 - SSRF Protection Tests"

# Should see all tests PASS
```

### Step 3: Manual Testing

```bash
# Test 1: Try to import from private IP (should FAIL)
curl -X POST http://localhost:8080/api/v1/videos/imports \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "source_url": "http://192.168.1.1/video.mp4",
    "target_privacy": "private"
  }'

# Expected: 400 Bad Request with error about blocked IP

# Test 2: Try to import from AWS metadata (should FAIL)
curl -X POST http://localhost:8080/api/v1/videos/imports \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "source_url": "http://169.254.169.254/latest/meta-data/",
    "target_privacy": "private"
  }'

# Expected: 400 Bad Request

# Test 3: Valid HTTPS URL (should SUCCEED)
curl -X POST http://localhost:8080/api/v1/videos/imports \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "source_url": "https://sample-videos.com/video.mp4",
    "target_privacy": "private"
  }'

# Expected: 201 Created
```

---

## Unit Tests to Add

### File to Create: `/root/athena/internal/usecase/import/url_validator_test.go`

```go
package importuc

import (
    "testing"
)

func TestValidateImportURL(t *testing.T) {
    tests := []struct {
        name    string
        url     string
        wantErr bool
    }{
        {"Valid HTTPS URL", "https://example.com/video.mp4", false},
        {"Private IP 192.168", "http://192.168.1.1/video.mp4", true},
        {"Private IP 10.x", "http://10.0.0.1/video.mp4", true},
        {"Private IP 172.16", "http://172.16.0.1/video.mp4", true},
        {"AWS Metadata", "http://169.254.169.254/latest/meta-data/", true},
        {"Localhost", "http://localhost/video.mp4", true},
        {"127.0.0.1", "http://127.0.0.1/video.mp4", true},
        {"FTP Protocol", "ftp://example.com/video.mp4", true},
        {"File Protocol", "file:///etc/passwd", true},
        {"HTTP (not HTTPS)", "http://example.com/video.mp4", true},
        {"Too Long URL", "https://example.com/" + strings.Repeat("a", 3000), true},
        {"Empty URL", "", true},
        {"Malformed URL", "not-a-url", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateImportURL(tt.url)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidateImportURL() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

---

## Checklist

Before committing your changes:

- [ ] Created `url_validator.go` with SSRF protection
- [ ] Created `file_validator.go` with size validation
- [ ] Modified `import_handlers.go` to call validators
- [ ] Modified `comments.go` to sanitize inputs
- [ ] Created unit tests for validators
- [ ] All unit tests pass (`go test ./...`)
- [ ] Postman security tests pass
- [ ] Manual testing confirms blocks work
- [ ] Code reviewed by another developer
- [ ] Updated relevant documentation

---

## Common Pitfalls

### ❌ DON'T DO THIS:

```go
// Bad: Only checking protocol, not IP
if !strings.HasPrefix(url, "https://") {
    return error
}
```

### ✅ DO THIS:

```go
// Good: Parse, validate protocol, AND resolve IP
parsedURL, _ := url.Parse(url)
if parsedURL.Scheme != "https" {
    return error
}
// ... then check resolved IP
```

---

### ❌ DON'T DO THIS:

```go
// Bad: Checking hostname, but not resolved IP
if hostname == "localhost" {
    return error
}
```

### ✅ DO THIS:

```go
// Good: Check hostname AND resolve to IP
if isLocalhost(hostname) {
    return error
}
ips, _ := net.LookupIP(hostname)
for _, ip := range ips {
    if isBlockedIP(ip) {
        return error
    }
}
```

---

## Performance Notes

- URL validation adds ~5-10ms per request (DNS lookup)
- File size check adds ~50-100ms per request (HEAD request)
- Total overhead: ~100ms max
- Acceptable for security critical operation

---

## Monitoring

After deploying, monitor these metrics:

```sql
-- Count blocked SSRF attempts
SELECT COUNT(*)
FROM import_logs
WHERE error LIKE '%blocked IP%'
  AND created_at > NOW() - INTERVAL '24 hours';

-- Count oversized file rejections
SELECT COUNT(*)
FROM import_logs
WHERE error LIKE '%file size%'
  AND created_at > NOW() - INTERVAL '24 hours';
```

Set up alerts if:
- More than 10 SSRF attempts per hour (possible attack)
- More than 100 size rejections per hour (possible DoS)

---

## Questions?

See full analysis: `/root/athena/BREAKING_CHANGES_ANALYSIS.md`
See tests: `/root/athena/postman/athena-edge-cases-security.postman_collection.json`
See summary: `/root/athena/EXECUTIVE_SUMMARY.md`

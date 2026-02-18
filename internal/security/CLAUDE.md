# Security Module - Claude Guidelines

## Overview

This module contains all security-critical code: SSRF protection, virus scanning, cryptographic operations, and input validation.

## SSRF Protection

### Core Files

- `url_validator.go` - URL validation with SSRF prevention
- `validation.go` - Higher-level validation utilities

### Blocked IP Ranges

All private/reserved ranges are blocked:

- RFC1918: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
- Loopback: `127.0.0.0/8`, `::1/128`
- Link-local: `169.254.0.0/16`, `fe80::/10`
- Cloud metadata: `169.254.169.254` (AWS/GCP/Azure)

### Obfuscation Detection

The validator detects bypasses:

- Octal IPs: `0177.0.0.1`
- Hex IPs: `0x7f.0.0.1`
- Integer IPs: `2130706433`
- IPv4-mapped IPv6: `::ffff:192.168.1.1`
- DNS rebinding: Double-resolution with delay

### Usage Pattern

```go
validator := security.NewURLValidator()
if err := validator.ValidateURL(rawURL); err != nil {
    return fmt.Errorf("SSRF blocked: %w", err)
}
```

## Virus Scanning (ClamAV)

### Workflow

1. File uploaded to temp location
2. Stream to ClamAV daemon for scanning
3. **Clean**: proceed to processing
4. **Infected**: quarantine + return 422
5. **Error**: reject (strict mode) or log warning

### Key Files

- `virus_scanner.go` - ClamAV client with retry logic
- Migration `057_add_virus_scan_log.sql` - Audit logging

### Configuration

```bash
CLAMAV_ADDRESS=localhost:3310
CLAMAV_TIMEOUT=300              # 5 min for large videos
CLAMAV_FALLBACK_MODE=strict     # ALWAYS use strict in production
QUARANTINE_DIR=/app/quarantine
```

### Fallback Modes

- `strict`: Reject if scanner unavailable (recommended)
- `warn`: Log warning, allow upload (dev only)
- `allow`: Silent allow (NEVER in production)

## Cryptographic Operations

### Key Files

- `hsm_interface.go` - HSM abstraction layer
- `software_hsm.go` - Fallback with AES-256-GCM + Argon2id
- `wallet_encryption.go` - Envelope encryption for seeds
- `activitypub_key_encryption.go` - Federation key encryption
- `hls_signing.go` - HMAC-SHA256 for stream tokens

### Required Environment

```bash
ACTIVITYPUB_KEY_ENCRYPTION_KEY=<32-byte-base64-encoded-key>
# Generate: openssl rand -base64 32
```

## HTML Sanitization

Use `html_sanitizer.go` with appropriate policy:

| Function | Use Case |
|----------|----------|
| `SanitizeHTML()` | Strip all HTML |
| `SanitizeHTMLWithBasicFormatting()` | Allow safe formatting |
| `SanitizeCommentHTML()` | Comments (nofollow links) |
| `SanitizeMarkdown()` | Markdown content |

## Blocked File Types

Always reject (see `validation.go`):

- Executables: `.exe`, `.msi`, `.dll`, `.so`, `.dylib`
- Scripts: `.bat`, `.ps1`, `.sh`, `.py`, `.js`, `.jar`
- App bundles: `.apk`, `.ipa`, `.app`, `.dmg`
- Macro Office: `.docm`, `.xlsm`, `.pptm`
- Active media: `.svg` (XSS risk), `.swf`

## Testing

```bash
# Run all security tests
go test ./internal/security/... -v

# SSRF-specific tests
go test ./internal/security/... -run 'SSRF|URLValidator'

# Virus scanner tests (requires testdata/eicar.txt)
go test ./internal/security/... -run VirusScanner
```

## Gosec Static Analysis

Gosec is configured in three places (keep in sync):

- `.golangci.yml` - Primary config for `make lint`
- `.github/workflows/security-tests.yml` - CI workflow with SARIF upload
- `.pre-commit-config.yaml` - Pre-commit hooks

### Excluded Rules

| Rule | Reason | Alternative |
|------|--------|-------------|
| G101 | Hardcoded credentials | Secret scanning in CI |
| G104 | Unhandled errors | errcheck linter |
| G115 | Integer overflow | False positives in Go 1.22+ |

### Path-Specific Exclusions

| Path | Rule | Reason |
|------|------|--------|
| `internal/torrent/` | G401 | SHA1 required by BitTorrent protocol |
| `internal/usecase/import/` | G107 | URLs validated by SSRF protection |
| `internal/activitypub/` | G107 | Federation requires external HTTP |
| `internal/importer/` | G107 | yt-dlp import requires external URLs |
| `internal/storage/` | G304 | Paths validated by storage layer |

### Inline Suppression

Use `#nosec` with justification comment:

```go
// #nosec G304 - path validated by validateFilePath()
file, err := os.Open(path)
```

## Security Review Checklist

When modifying this module:

1. [ ] All user inputs validated before use
2. [ ] No string concatenation in SQL queries
3. [ ] All network calls have timeouts
4. [ ] Secrets not logged anywhere
5. [ ] Error messages don't leak internal details
6. [ ] Tests cover bypass attempts
7. [ ] Run `make lint` (includes gosec)

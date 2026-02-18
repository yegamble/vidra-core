# Security Module Testing Guide

This document describes the test-driven development (TDD) approach for the virus scanning and file type blocking infrastructure.

## Overview

The security module implements two critical security features:

1. **Virus Scanning**: ClamAV integration for malware detection
2. **File Type Blocking**: Extension and magic byte validation

## Test Structure

### Virus Scanner Tests (`virus_scanner_test.go`)

**Test Categories:**

1. **Initialization Tests** (3 tests)
   - Default configuration
   - Custom timeout
   - Invalid address handling

2. **Connection & Fallback Tests** (2 tests)
   - Graceful degradation when ClamAV unavailable
   - Environment variable configuration

3. **File Scanning Tests** (5 tests)
   - Clean file detection
   - EICAR test virus detection
   - Stream scanning
   - Large file scanning
   - Concurrent scans

4. **Timeout & Cancellation Tests** (2 tests)
   - Scan timeout handling
   - Context cancellation

5. **Quarantine Tests** (4 tests)
   - Infected file quarantine
   - Directory permissions
   - Audit logging
   - Cleanup of old files

6. **Integration Tests** (4 tests)
   - Upload workflow integration
   - FFmpeg pre-processing scan
   - IPFS pre-pinning scan
   - User notification

7. **Performance Tests** (4 benchmarks)
   - Small file scan throughput
   - Large file scan performance
   - Concurrent scan scalability
   - Memory usage validation

**Total: 20+ test cases + 4 benchmarks**

### File Type Blocker Tests (`file_type_blocker_test.go`)

**Test Categories:**

1. **Extension Blocking Tests** (4 test groups)
   - Executables (.exe, .dll, .so, etc.)
   - Scripts (.sh, .bat, .ps1, .py, etc.)
   - Macro documents (.docm, .xlsm, etc.)
   - Dangerous formats (.svg, .swf, .iso, etc.)

2. **Magic Byte Validation Tests** (4 tests)
   - Extension/magic mismatch detection
   - Polyglot file detection
   - Double extension tricks
   - Files without extensions

3. **Archive Validation Tests** (6 tests)
   - ZIP nesting depth limits
   - File count limits
   - ZIP bomb detection
   - Encrypted archive rejection
   - Archives containing blocked types
   - Valid archive acceptance

4. **Allowed File Tests** (4 test groups)
   - Legitimate videos (MP4, MOV, WebM)
   - Legitimate images (JPEG, PNG, GIF)
   - Legitimate documents (PDF, DOCX)
   - Legitimate audio (MP3, WAV, AAC)

5. **Edge Cases** (5 tests)
   - Case-insensitive extensions
   - Multiple extensions
   - Empty files
   - Null bytes
   - File size limits

**Total: 15+ test cases covering 40+ file types**

## Running Tests

### Local Development (Requires ClamAV)

```bash
# Install ClamAV
# Ubuntu/Debian:
sudo apt-get install clamav clamav-daemon

# macOS:
brew install clamav

# Start ClamAV daemon
sudo systemctl start clamav-daemon  # Linux
sudo clamd                          # macOS

# Run all security tests
go test -v ./internal/security/...

# Run only virus scanner tests
go test -v ./internal/security/virus_scanner_test.go

# Run only file type blocker tests
go test -v ./internal/security/file_type_blocker_test.go

# Run benchmarks
go test -bench=. -benchmem ./internal/security/...

# Skip integration tests (when ClamAV unavailable)
go test -short ./internal/security/...
```

### Docker Testing (Recommended)

```bash
# Start ClamAV test container
docker compose --profile test up -d clamav-test

# Wait for ClamAV to be ready (may take 2-3 minutes for signature loading)
docker compose --profile test logs -f clamav-test

# Run tests against containerized ClamAV
CLAMAV_ADDRESS=localhost:3310 go test -v ./internal/security/...

# Run full integration test suite
docker compose --profile test up --abort-on-container-exit

# Cleanup
docker compose --profile test down -v
```

### CI/CD Integration

```bash
# GitHub Actions / GitLab CI
docker compose --profile test up --abort-on-container-exit --exit-code-from app-test
```

## Test Fixtures

All test data is located in `/testdata/virus_scanner/`:

| File | Purpose | Size |
|------|---------|------|
| `clean_file.txt` | Clean file test | < 1KB |
| `eicar.txt` | EICAR test virus | < 1KB |
| `large_clean.bin` | Performance testing | 100MB |
| `clean_video.mp4` | Video file test | < 1KB |
| `nested.zip` | Archive nesting test | < 1KB |
| `blocked_types/*` | Blocked file types | Various |

See `/testdata/virus_scanner/README.md` for details.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAMAV_ADDRESS` | `localhost:3310` | ClamAV daemon address |
| `CLAMAV_TIMEOUT` | `30` | Scan timeout (seconds) |
| `CLAMAV_FALLBACK_MODE` | `strict` | Behavior when ClamAV unavailable |
| `TEST_QUARANTINE_DIR` | `/tmp/quarantine` | Quarantine directory for tests |

## TDD Workflow

This module follows strict TDD principles:

1. **RED**: Write failing tests first (current state)
2. **GREEN**: Implement minimal code to pass tests
3. **REFACTOR**: Improve code while keeping tests green

### Current State: RED Phase

All tests are currently **FAILING** because:

- `VirusScanner` struct not implemented
- `FileTypeBlocker` struct not implemented
- Mock implementations return nil/empty values

### Next Steps (Implementation Phase)

1. Implement `VirusScanner` in `/internal/security/virus_scanner.go`
2. Implement `FileTypeBlocker` in `/internal/security/file_type_blocker.go`
3. Run tests to verify implementation
4. Refactor for performance and maintainability

## Dependencies

### Go Packages

```go
import (
    "github.com/dutchcoders/go-clamd"      // ClamAV client
    "github.com/stretchr/testify/assert"   // Assertions
    "github.com/stretchr/testify/require"  // Required assertions
)
```

Install:

```bash
go get github.com/dutchcoders/go-clamd
go get github.com/stretchr/testify
```

### External Services

- **ClamAV**: Virus scanning engine (required for integration tests)
- **Docker**: Container runtime (optional, for isolated testing)

## Performance Expectations

Based on test requirements:

| Metric | Target | Test |
|--------|--------|------|
| Small file scan (< 1KB) | < 10ms | `BenchmarkVirusScanner_ScanSmallFile` |
| Large file scan (100MB) | < 5s | `BenchmarkVirusScanner_ScanLargeFile` |
| Concurrent scans (10x) | No deadlock | `TestVirusScanner_ConcurrentScans` |
| Memory overhead | < 50MB for 100MB file | `TestVirusScanner_MemoryUsage` |

## Security Considerations

### EICAR Test File

The EICAR test file (`eicar.txt`) is NOT real malware. It's a standard test file:

- Developed by EICAR (European Institute for Computer Antivirus Research)
- Detected by all antivirus software as "EICAR-Test-File"
- Safe to store in version control
- Used worldwide for antivirus testing

### Blocked File Types

The following file types are ALWAYS blocked per CLAUDE.md:

**Executables**: `.exe`, `.msi`, `.com`, `.scr`, `.dll`, `.bin`, `.elf`, `.dylib`, `.so`

**Scripts**: `.bat`, `.cmd`, `.ps1`, `.vbs`, `.js`, `.jar`, `.sh`, `.bash`, `.py`, `.pl`, `.rb`, `.php`

**Macro Documents**: `.docm`, `.dotm`, `.xlsm`, `.xltm`, `.pptm`, `.ppam`

**Dangerous Formats**: `.svg`, `.swf`, `.iso`, `.img`, `.vhd`, `.apk`, `.ipa`, `.lnk`, `.reg`, etc.

See CLAUDE.md for complete list.

## Troubleshooting

### ClamAV Connection Failed

```
Error: dial tcp 127.0.0.1:3310: connect: connection refused
```

**Solutions:**

- Ensure ClamAV daemon is running: `systemctl status clamav-daemon`
- Check port binding: `netstat -tlnp | grep 3310`
- Use Docker container: `docker compose --profile test up clamav-test`
- Run tests in short mode to skip integration: `go test -short`

### ClamAV Signature Update

```
Error: Can't connect to clamd: No such file or directory
```

**Solutions:**

- Update virus signatures: `sudo freshclam`
- Wait for Docker container initialization (2-3 minutes)
- Check ClamAV logs: `docker compose --profile test logs clamav-test`

### Test Timeout

```
panic: test timed out after 10m0s
```

**Solutions:**

- Increase test timeout: `go test -timeout 20m`
- Check ClamAV is responding: `docker compose --profile test ps`
- Verify network connectivity to ClamAV

## Code Coverage

Generate coverage report:

```bash
go test -coverprofile=coverage.out ./internal/security/...
go tool cover -html=coverage.out -o coverage.html
```

**Coverage Targets:**

- Overall: > 80%
- Critical paths (scanning, blocking): > 95%
- Error handling: > 90%

## Contributing

When adding new tests:

1. Follow existing test naming conventions
2. Add table-driven tests for multiple scenarios
3. Include both positive and negative test cases
4. Document test fixtures in `/testdata/virus_scanner/README.md`
5. Update this guide with new test categories

## References

- CLAUDE.md: Project security requirements
- [ClamAV Documentation](https://docs.clamav.net/)
- [EICAR Test File](https://www.eicar.org/download-anti-malware-testfile/)
- [go-clamd Library](https://github.com/dutchcoders/go-clamd)

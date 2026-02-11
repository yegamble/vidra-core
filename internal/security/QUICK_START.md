# Security Module Quick Start

## What Was Delivered

**TASK 1.4**: Comprehensive TDD test suite for virus scanning and file type blocking

- ✅ **1,372 lines** of test code
- ✅ **19** virus scanner test functions
- ✅ **3** performance benchmarks
- ✅ **22** file type blocker test functions
- ✅ **7** test fixtures including EICAR test virus
- ✅ ClamAV Docker integration
- ✅ Complete documentation

**Status**: RED Phase (tests failing, awaiting implementation)

---

## Quick Commands

### Run Tests (Docker - Recommended)
```bash
./scripts/run-security-tests.sh
```

### Run Tests (Manual)
```bash
# Start ClamAV
docker compose --profile test up -d clamav-test

# Wait 2-3 minutes for ClamAV to load signatures
docker compose --profile test logs -f clamav-test

# Run virus scanner tests
CLAMAV_ADDRESS=localhost:3310 go test -v ./internal/security/virus_scanner_test.go

# Run file type blocker tests
go test -v ./internal/security/file_type_blocker_test.go
```

### Run Without ClamAV
```bash
go test -short ./internal/security/...
```

### Generate Coverage
```bash
./scripts/run-security-tests.sh -c
open coverage.html
```

### Run Benchmarks
```bash
./scripts/run-security-tests.sh -b
```

---

## File Locations

| File | Location |
|------|----------|
| Virus scanner tests | `/internal/security/virus_scanner_test.go` |
| File type blocker tests | `/internal/security/file_type_blocker_test.go` |
| Test fixtures | `/testdata/virus_scanner/` |
| Docker config | `/docker-compose.yml` (`test` profile) |
| Documentation | `/internal/security/TESTING.md` |
| Test runner | `/scripts/run-security-tests.sh` |

---

## What Gets Tested

### Virus Scanner
- ✅ ClamAV connection & initialization
- ✅ Clean file scanning
- ✅ EICAR test virus detection
- ✅ Large file streaming (100MB)
- ✅ Concurrent scans (10 simultaneous)
- ✅ Timeout handling
- ✅ Graceful fallback
- ✅ Quarantine with audit trail
- ✅ Integration with upload/FFmpeg/IPFS

### File Type Blocker
- ✅ Block executables (.exe, .dll, .so, etc.)
- ✅ Block scripts (.bat, .sh, .ps1, .py, etc.)
- ✅ Block macro documents (.docm, .xlsm, etc.)
- ✅ Block dangerous formats (.svg, .swf, .iso, etc.)
- ✅ Magic byte validation (no spoofing)
- ✅ Polyglot file detection
- ✅ ZIP bomb protection
- ✅ Archive nesting limits
- ✅ Allow legitimate files (videos, images, docs)

---

## Expected Behavior (RED Phase)

**All tests should FAIL** because implementation doesn't exist yet:

```
--- FAIL: TestVirusScanner_Initialize (0.00s)
    Error: Expected value not to be nil.

--- FAIL: TestFileTypeBlocker_RejectExecutables (0.00s)
    Error: received unexpected value: false
```

This is **correct** in TDD! Tests first, implementation next.

---

## Next Steps (GREEN Phase)

1. Implement `/internal/security/virus_scanner.go`
2. Implement `/internal/security/file_type_blocker.go`
3. Run tests until they pass
4. Refactor for optimization

---

## Key Test Files

### Clean Files (Should Pass)
- `testdata/virus_scanner/clean_file.txt`
- `testdata/virus_scanner/clean_video.mp4`
- `testdata/virus_scanner/large_clean.bin`

### Infected Files (Should Detect)
- `testdata/virus_scanner/eicar.txt` (EICAR test virus)

### Blocked Files (Should Reject)
- `testdata/virus_scanner/blocked_types/*.exe|.bat|.ps1|.sh|.py`

### Archive Tests (Should Validate)
- `testdata/virus_scanner/nested.zip` (excessive nesting)

---

## Environment Variables

```bash
export CLAMAV_ADDRESS=localhost:3310
export CLAMAV_TIMEOUT=60
export CLAMAV_FALLBACK_MODE=strict
export TEST_QUARANTINE_DIR=/tmp/quarantine
```

---

## Dependencies

```bash
go get github.com/dutchcoders/go-clamd
go get github.com/stretchr/testify
```

---

## Troubleshooting

### "connection refused" on port 3310
ClamAV isn't running. Start it:
```bash
docker compose --profile test up -d clamav-test
```

### "test timed out"
ClamAV is still loading signatures. Wait 2-3 minutes.

### Tests skip in -short mode
This is expected. Use Docker for full integration tests.

---

## Documentation

- **Full Guide**: `/internal/security/TESTING.md`
- **Fixtures**: `/testdata/virus_scanner/README.md`
- **Summary**: `/TASK_1.4_SUMMARY.md`

---

**Happy Testing!** 🧪

*Remember: RED → GREEN → REFACTOR*

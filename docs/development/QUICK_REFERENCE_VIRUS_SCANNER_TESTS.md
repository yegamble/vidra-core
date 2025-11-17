# Virus Scanner Security Tests - Quick Reference Card

## Critical Commands

### Before Fix (Should FAIL)
```bash
go test -v ./internal/security -run TestVirusScanner_ExhaustedReaderVulnerability
# Expected: FAIL with "CRITICAL SECURITY VULNERABILITY"
```

### After Fix (Should PASS)
```bash
go test -v ./internal/security -run TestVirusScanner
# Expected: All 70 tests PASS
```

### Race Detection
```bash
go test -race -v ./internal/security -run TestVirusScanner_Concurrent
# Expected: No race conditions detected
```

---

## Test Quick Reference

| Test Name | Purpose | Critical Check |
|-----------|---------|----------------|
| `ExhaustedReaderVulnerability` | **PRIMARY** - Expose exhausted reader bug | Infected ≠ Clean |
| `NonSeekableReaderRetry` | Non-seekable streams fail safely | Infected ≠ Clean |
| `SeekableReaderRetrySuccess` | Files retry correctly | Virus detected |
| `ZeroByteStream` | Empty stream handling | Never Infected |
| `StreamFailsMidRead` | Partial read failures | Partial ≠ Clean |
| `ConcurrentStreamScans` | 10 concurrent scans | No races |
| `LargeStreamMemoryUsage` | 10MB stream buffering | Mem < 50MB |
| `NetworkErrorRetry` | Network failure handling | Safe retry/fail |
| `DifferentErrorTypes` | Various error scenarios | Errors → Error |
| `BusinessLogic_CleanFilesPassthrough` | Clean files work | No false + |
| `BusinessLogic_InfectedFilesBlocked` | EICAR detection | Virus found |
| `BusinessLogic_ErrorHandlingConsistency` | Error modes work | Strict fails |

---

## The Security Invariant

```
IF infected THEN status ≠ Clean
```

**Never acceptable**: Infected file marked as Clean
**Always acceptable**: Infected detected OR safe error

---

## Files

| File | Purpose | Size |
|------|---------|------|
| `virus_scanner_test.go` | Test implementation | 1412 lines |
| `SECURITY_ANALYSIS_VIRUS_SCANNER.md` | Detailed analysis | 14KB |
| `TEST_EXECUTION_GUIDE.md` | Step-by-step guide | 15KB |
| `VIRUS_SCANNER_TEST_SUMMARY.md` | Executive summary | 12KB |

---

## Quick Validation

```bash
# 1. Compile tests
go test -c ./internal/security

# 2. Run primary vulnerability test
go test -v ./internal/security -run ExhaustedReader

# 3. Run all security tests
go test -v ./internal/security -run TestVirusScanner

# 4. Check coverage
go test ./internal/security -coverprofile=coverage.out
go tool cover -func=coverage.out | grep ScanStream
```

---

## Expected Outcomes

### Before Fix
```
FAIL: TestVirusScanner_ExhaustedReaderVulnerability
  CRITICAL SECURITY VULNERABILITY: Infected EICAR file marked as CLEAN!
```

### After Fix (Option A - Detection)
```
PASS: TestVirusScanner_ExhaustedReaderVulnerability
  Expected behavior: virus detected correctly
```

### After Fix (Option B - Safe Failure)
```
PASS: TestVirusScanner_ExhaustedReaderVulnerability
  Expected behavior: scan failed with error (safe)
```

---

## Memory & Performance

| Metric | Target | Test |
|--------|--------|------|
| 10MB stream memory | < 50MB | `LargeStreamMemoryUsage` |
| Concurrent scans | No races | `ConcurrentStreamScans` |
| Network retries | Safe fail | `NetworkErrorRetry` |

---

## Contact

- **Tests**: `/Users/yosefgamble/github/athena/internal/security/virus_scanner_test.go:167-871`
- **Impl**: `/Users/yosefgamble/github/athena/internal/security/virus_scanner.go:254-352`
- **Docs**: See `SECURITY_ANALYSIS_VIRUS_SCANNER.md`

**Remember**: No infected file can ever be marked clean.

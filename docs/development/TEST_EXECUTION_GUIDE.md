# Virus Scanner Security Test Execution Guide

## Overview

This guide provides instructions for running the comprehensive security test suite added to validate the P1 virus scanner retry logic vulnerability fix.

**Total Tests Added**: 11 critical security tests + 3 business logic validation tests
**Test File**: `/Users/yosefgamble/github/athena/internal/security/virus_scanner_test.go`
**Lines**: 167-871 (new security tests)

---

## Quick Start

### Prerequisites

1. **ClamAV Server Running** (for integration tests):

   ```bash
   docker run -d -p 3310:3310 --name clamav clamav/clamav:latest

   # Wait for ClamAV to initialize (30-60 seconds)
   docker logs -f clamav
   ```

2. **Test Data** (EICAR files):
   The tests use the EICAR test string embedded in code. No external files needed.

---

## Test Execution Commands

### 1. Run All Security Tests (Recommended)

```bash
cd /Users/yosefgamble/github/athena
go test -v ./internal/security -run TestVirusScanner
```

**Expected Output**: All tests should pass AFTER the fix is implemented.
**Before Fix**: `TestVirusScanner_ExhaustedReaderVulnerability` should FAIL, exposing the bug.

---

### 2. Run ONLY Critical Vulnerability Tests

These tests specifically target the exhausted reader vulnerability:

```bash
go test -v ./internal/security -run "TestVirusScanner_(Exhausted|NonSeekable|Seekable)"
```

**Tests Executed**:

- `TestVirusScanner_ExhaustedReaderVulnerability` (PRIMARY)
- `TestVirusScanner_NonSeekableReaderRetry`
- `TestVirusScanner_SeekableReaderRetrySuccess`

**Success Criteria**: No infected file marked as `ScanStatusClean`.

---

### 3. Run Business Logic Validation Tests

Ensure the fix doesn't break existing functionality:

```bash
go test -v ./internal/security -run "TestVirusScanner_BusinessLogic"
```

**Tests Executed**:

- `TestVirusScanner_BusinessLogic_CleanFilesPassthrough`
- `TestVirusScanner_BusinessLogic_InfectedFilesBlocked`
- `TestVirusScanner_BusinessLogic_ErrorHandlingConsistency`

**Success Criteria**:

- Clean files still pass ✓
- Infected files still blocked ✓
- Error handling unchanged ✓

---

### 4. Run Edge Case Tests

Test boundary conditions and error scenarios:

```bash
go test -v ./internal/security -run "TestVirusScanner_(ZeroByte|FailsMid|DifferentError)"
```

**Tests Executed**:

- `TestVirusScanner_ZeroByteStream`
- `TestVirusScanner_StreamFailsMidRead`
- `TestVirusScanner_DifferentErrorTypes`

**Success Criteria**: All error scenarios fail safely (no false clean results).

---

### 5. Run Concurrency & Performance Tests

Validate thread safety and memory usage:

```bash
go test -v ./internal/security -run "TestVirusScanner_(Concurrent.*Stream|.*StreamMemory|NetworkError)"
```

**Tests Executed**:

- `TestVirusScanner_ConcurrentStreamScans`
- `TestVirusScanner_LargeStreamMemoryUsage`
- `TestVirusScanner_NetworkErrorRetry`

**Success Criteria**:

- No race conditions
- Memory usage < 50MB for 10MB file
- Network retries work or fail safely

---

### 6. Run with Race Detection

Critical for concurrent scanning validation:

```bash
go test -race -v ./internal/security -run TestVirusScanner_Concurrent
```

**Purpose**: Detect data races in concurrent scanning scenarios.
**Expected**: No race conditions detected.

---

### 7. Run Short Tests Only

Skip integration tests (no ClamAV required):

```bash
go test -short -v ./internal/security
```

**Note**: Most security tests require ClamAV and will be skipped.

---

### 8. Generate Coverage Report

See which code paths are tested:

```bash
go test ./internal/security -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
open coverage.html
```

**Target Coverage**:

- `ScanStream` method: 100%
- Retry logic: 100%
- Error handling: 100%

---

## Test-by-Test Breakdown

### Primary Vulnerability Test

**Test**: `TestVirusScanner_ExhaustedReaderVulnerability`

**What it tests**:

- Simulates non-seekable reader (HTTP body) that fails on first attempt
- Reader becomes exhausted after initial read
- Retry logic attempts to scan exhausted reader
- **CRITICAL**: Infected EICAR file must NEVER be marked clean

**Expected Behavior BEFORE Fix**:

```
FAIL: CRITICAL SECURITY VULNERABILITY: Infected EICAR file marked as CLEAN!
```

**Expected Behavior AFTER Fix**:

```
PASS: Expected behavior: virus detected correctly
  OR
PASS: Expected behavior: scan failed with error (safe)
```

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_ExhaustedReaderVulnerability
```

---

### Non-Seekable Reader Test

**Test**: `TestVirusScanner_NonSeekableReaderRetry`

**What it tests**:

- Multiple scenarios with non-seekable readers
- Infected EICAR content in exhausted reader
- Clean content in exhausted reader
- Validates retry behavior for both cases

**Success Criteria**:

- Infected content: Status ≠ Clean (Error or Infected OK)
- Clean content: Any status except Infected

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_NonSeekableReaderRetry
```

---

### Seekable Reader Test

**Test**: `TestVirusScanner_SeekableReaderRetrySuccess`

**What it tests**:

- File-based (seekable) readers work correctly
- Retries succeed by seeking back to start
- EICAR detection after retry

**Success Criteria**:

- Status = Infected
- VirusName contains "EICAR"

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_SeekableReaderRetrySuccess
```

---

### Zero-Byte Stream Test

**Test**: `TestVirusScanner_ZeroByteStream`

**What it tests**:

- Empty stream handling
- Boundary condition (0 bytes)

**Success Criteria**:

- Status ≠ Infected (empty can't be infected)
- Clean or Error acceptable

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_ZeroByteStream
```

---

### Mid-Read Failure Test

**Test**: `TestVirusScanner_StreamFailsMidRead`

**What it tests**:

- Stream that fails after reading partial EICAR content
- Simulates network interruption mid-upload

**Success Criteria**:

- Status ≠ Clean (partial infected read must not pass)
- Error or Infected acceptable

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_StreamFailsMidRead
```

---

### Concurrent Stream Scans Test

**Test**: `TestVirusScanner_ConcurrentStreamScans`

**What it tests**:

- 10 concurrent scans (5 infected, 5 clean)
- Race condition detection
- Concurrent retry safety

**Success Criteria**:

- No infected files marked clean
- No race conditions (run with `-race`)
- All goroutines complete successfully

**Command**:

```bash
go test -race -v ./internal/security -run TestVirusScanner_ConcurrentStreamScans
```

---

### Large Stream Memory Test

**Test**: `TestVirusScanner_LargeStreamMemoryUsage`

**What it tests**:

- Memory usage for 10MB stream
- Validates buffering doesn't cause memory explosion

**Success Criteria**:

- Memory increase < 50MB (allows 5x overhead for buffering)

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_LargeStreamMemoryUsage
```

---

### Network Error Retry Test

**Test**: `TestVirusScanner_NetworkErrorRetry`

**What it tests**:

- Transient network errors during scan
- Retry succeeds after network recovery

**Success Criteria**:

- After retry: Infected detected OR safe failure
- No false clean results

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_NetworkErrorRetry
```

---

### Different Error Types Test

**Test**: `TestVirusScanner_DifferentErrorTypes`

**What it tests**:

- Timeout errors (DeadlineExceeded)
- EOF errors (immediate end)
- Permission errors

**Success Criteria**:

- All errors result in Error status
- Never Clean on error

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_DifferentErrorTypes
```

---

### Business Logic: Clean Files Test

**Test**: `TestVirusScanner_BusinessLogic_CleanFilesPassthrough`

**What it tests**:

- Clean content still passes after fix
- bytes.Reader and strings.Reader work
- No false positives

**Success Criteria**:

- Clean data: Status ≠ Infected

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_BusinessLogic_CleanFilesPassthrough
```

---

### Business Logic: Infected Files Test

**Test**: `TestVirusScanner_BusinessLogic_InfectedFilesBlocked`

**What it tests**:

- EICAR variants still detected
- No regression in virus detection

**Success Criteria**:

- All EICAR variants: Status = Infected
- VirusName contains "EICAR"

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_BusinessLogic_InfectedFilesBlocked
```

---

### Business Logic: Error Handling Test

**Test**: `TestVirusScanner_BusinessLogic_ErrorHandlingConsistency`

**What it tests**:

- FallbackModeStrict behavior unchanged
- Errors prevent processing
- No false clean on persistent failure

**Success Criteria**:

- Strict mode: Error status on failure
- Never Clean on scan failure

**Command**:

```bash
go test -v ./internal/security -run TestVirusScanner_BusinessLogic_ErrorHandlingConsistency
```

---

## Interpreting Test Results

### Expected Output (BEFORE Fix)

```
=== RUN   TestVirusScanner_ExhaustedReaderVulnerability
--- FAIL: TestVirusScanner_ExhaustedReaderVulnerability (0.15s)
    virus_scanner_test.go:248: CRITICAL SECURITY VULNERABILITY: Infected EICAR file marked as CLEAN!
        This indicates exhausted reader was scanned as empty file.
        Status=Clean, VirusName="", FallbackUsed=false, Error=<nil>
```

**Interpretation**: The bug is exposed. Exhausted reader caused false clean result.

---

### Expected Output (AFTER Fix - Success)

```
=== RUN   TestVirusScanner_ExhaustedReaderVulnerability
    virus_scanner_test.go:271: Expected behavior: virus detected correctly
--- PASS: TestVirusScanner_ExhaustedReaderVulnerability (0.12s)
```

**Interpretation**: Fix working. Virus detected after buffering/retry.

---

### Expected Output (AFTER Fix - Safe Failure)

```
=== RUN   TestVirusScanner_ExhaustedReaderVulnerability
    virus_scanner_test.go:264: Expected behavior: scan failed with error (safe): virus scan failed: cannot retry non-seekable reader
--- PASS: TestVirusScanner_ExhaustedReaderVulnerability (0.08s)
```

**Interpretation**: Fix working. Safe failure (no false clean).

---

### Failure Modes to Investigate

#### False Clean (CRITICAL BUG)

```
FAIL: CRITICAL SECURITY VULNERABILITY: Infected EICAR file marked as CLEAN!
```

**Action**: Fix not working. Exhausted reader still bypassing scan.

#### Unexpected Error

```
FAIL: unexpected error: context deadline exceeded
```

**Action**: May be environment issue (ClamAV slow/unavailable). Retry or check ClamAV.

#### Memory Exceeded

```
FAIL: Memory usage should be reasonable even with buffering
    Expected: < 50000000
    Actual:   125000000
```

**Action**: Buffering implementation needs optimization.

---

## Continuous Integration

### GitHub Actions Example

```yaml
name: Security Tests

on: [push, pull_request]

jobs:
  virus-scanner-security:
    runs-on: ubuntu-latest

    services:
      clamav:
        image: clamav/clamav:latest
        ports:
          - 3310:3310

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Wait for ClamAV
        run: |
          timeout 60 bash -c 'until nc -z localhost 3310; do sleep 1; done'

      - name: Run Security Tests
        run: |
          go test -v ./internal/security -run TestVirusScanner

      - name: Run with Race Detection
        run: |
          go test -race -v ./internal/security -run TestVirusScanner_Concurrent

      - name: Coverage Report
        run: |
          go test ./internal/security -coverprofile=coverage.out
          go tool cover -func=coverage.out
```

---

## Test Coverage Summary

| Category | Tests | Critical Assertions |
|----------|-------|---------------------|
| **Vulnerability Detection** | 3 | Infected ≠ Clean |
| **Edge Cases** | 3 | Safe failure modes |
| **Concurrency** | 2 | No race conditions |
| **Business Logic** | 3 | No regression |
| **Total** | **11** | **9 critical + 2 performance** |

---

## Validation Checklist

### Pre-Fix Validation

- [ ] `TestVirusScanner_ExhaustedReaderVulnerability` FAILS (confirms bug exists)
- [ ] Failure message contains "CRITICAL SECURITY VULNERABILITY"
- [ ] Failure shows `Status=Clean` for infected content
- [ ] All other tests pass (existing functionality works)

### Post-Fix Validation

- [ ] All 11 new security tests PASS
- [ ] `TestVirusScanner_ExhaustedReaderVulnerability` PASSES
- [ ] No test shows infected content as Clean
- [ ] Memory tests within limits (< 50MB for 10MB file)
- [ ] Race detection clean (no races)
- [ ] Business logic tests pass (no regression)
- [ ] Coverage report shows retry logic 100% covered

### Production Readiness

- [ ] All tests pass in CI/CD pipeline
- [ ] Performance benchmarks acceptable
- [ ] ClamAV integration tested
- [ ] Monitoring/alerting configured
- [ ] Rollback plan documented

---

## Troubleshooting

### ClamAV Not Running

**Error**: `connection refused`

**Solution**:

```bash
docker run -d -p 3310:3310 --name clamav clamav/clamav:latest
docker logs -f clamav  # Wait for "clamd started"
```

### Tests Timing Out

**Error**: `context deadline exceeded`

**Solution**:

- Increase timeout in test config
- Check ClamAV performance
- Reduce test file sizes

### False Positives/Negatives

**Error**: Clean file marked infected OR infected file marked clean

**Solution**:

- Update ClamAV signatures: `docker exec clamav freshclam`
- Verify EICAR test string matches exactly
- Check ClamAV version compatibility

### Memory Tests Failing

**Error**: Memory usage exceeded

**Solution**:

- Run GC before measurement: `runtime.GC()`
- Increase threshold if buffering needed
- Check for memory leaks in fix

---

## Next Steps After Fix

1. **Deploy to Staging**:

   ```bash
   go test -v ./internal/security
   # All tests pass → deploy
   ```

2. **Monitor Production**:
   - Virus detection rate (should be stable)
   - Scan error rate (may increase slightly)
   - Memory usage patterns
   - Retry frequency

3. **Audit Logs**:
   - Check for any `ScanStatusClean` with subsequent infection
   - Verify all infected files quarantined
   - Monitor false positive reports

4. **Performance Tuning**:
   - Adjust buffer sizes if needed
   - Optimize retry delays
   - Scale ClamAV if scan queue grows

---

## Contact & Support

- **Test Issues**: Check `/Users/yosefgamble/github/athena/internal/security/virus_scanner_test.go`
- **Implementation**: See `/Users/yosefgamble/github/athena/internal/security/virus_scanner.go`
- **Security Analysis**: See `/Users/yosefgamble/github/athena/SECURITY_ANALYSIS_VIRUS_SCANNER.md`

**Remember**: An infected file must NEVER be marked clean. All tests enforce this invariant.

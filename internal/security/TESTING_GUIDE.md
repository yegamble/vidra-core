# Comprehensive Virus Scanner Testing Guide

## Overview

This guide provides detailed instructions for testing the virus scanner security fix (P1 vulnerability SEC-2025-001). The test suite validates edge cases, breaking scenarios, performance benchmarks, and API contract compliance.

---

## Test Categories

### 1. Edge Case Tests

Tests that validate boundary conditions and unusual input scenarios.

| Test ID | Test Name | Description | Expected Result |
|---------|-----------|-------------|-----------------|
| EC-001 | Exactly 10MB File | Upload file at exact buffer boundary | Accept and scan successfully |
| EC-002 | 10MB + 1 Byte File | Upload file just over boundary | Accept with additional chunk |
| EC-003 | Network Interruption | ClamAV connection dropped mid-scan | Retry and reject on failure |
| EC-004 | Slow Connection Timeout | Very slow network connection | Timeout gracefully with error |
| EC-005 | Malformed Content-Type | Invalid HTTP header injection | Sanitize or reject with 400 |
| EC-006 | Chunked Transfer Encoding | Upload via chunked encoding | Process correctly |
| EC-007 | Zero-Byte File | Empty file upload | Accept but flag for review |
| EC-008 | Null Bytes in Filename | Filename with embedded null | Sanitize filename |

### 2. Breaking Scenarios (P1 Vulnerability Tests)

Critical security tests that validate the vulnerability fix.

| Test ID | Attack Vector | Exploitation Method | Expected Behavior | Security Impact |
|---------|---------------|---------------------|-------------------|-----------------|
| BS-001 | Network Error Bypass | Kill ClamAV during scan | **REJECT** file with error | Critical - Prevents infected file acceptance |
| BS-002 | Retry Exhaustion | All retry attempts fail | **REJECT** with scan failure | Critical - No fallback to unsafe mode |
| BS-003 | Concurrent Infected Uploads | 10+ simultaneous EICAR uploads | **REJECT ALL** without race conditions | High - Prevents concurrent bypass |
| BS-004 | Resource Exhaustion | Many large file uploads | Rate limit or queue, no crashes | Medium - Prevents DoS |
| BS-005 | Fallback Mode Abuse | Configure warn/allow mode | **ENFORCE strict mode** in production | Critical - Prevents configuration bypass |
| BS-006 | Timing Attack | Delay scan completion | Timeout enforcement | Medium - Prevents slowloris-style attacks |

### 3. Security Validation Tests

Tests that verify malware detection and file type blocking.

| Test ID | Test Case | File Type | Expected Detection |
|---------|-----------|-----------|-------------------|
| SV-001 | EICAR Test Virus | .txt, .com | Detected as "EICAR-Test-File", rejected |
| SV-002 | Executable File | .exe, .dll, .so | Blocked before scan |
| SV-003 | Script File | .sh, .bat, .ps1 | Blocked before scan |
| SV-004 | Macro Document | .docm, .xlsm | Blocked before scan |
| SV-005 | Nested Archive | .zip (nested 10+ levels) | Rejected as zip bomb |
| SV-006 | Polyglot File | Multi-format file | MIME type mismatch detected |
| SV-007 | Double Extension | file.pdf.exe | Blocked as executable |
| SV-008 | SVG with Scripts | .svg with embedded JS | Blocked as dangerous format |

### 4. Performance Tests

Benchmarks to ensure scanning doesn't degrade performance.

| Test ID | Metric | Target | Acceptance Criteria |
|---------|--------|--------|---------------------|
| PT-001 | Small File Scan (< 1KB) | < 10ms | P95 latency under threshold |
| PT-002 | Medium File Scan (10MB) | < 2s | P95 latency under threshold |
| PT-003 | Large File Scan (100MB) | < 5s | P95 latency under threshold |
| PT-004 | Memory Overhead (100MB file) | < 50MB | Streaming implementation prevents memory bloat |
| PT-005 | CPU Usage During Scan | < 80% | Does not monopolize CPU |
| PT-006 | Concurrent Scan Throughput | 10 parallel scans | No deadlocks, all complete successfully |
| PT-007 | Response Time Under Load | < 30s at 100 req/s | Acceptable degradation under load |

### 5. API Contract Validation

Tests that verify error responses and HTTP compliance.

| Test ID | Scenario | Expected Status Code | Expected Headers | Response Format |
|---------|----------|---------------------|------------------|-----------------|
| AC-001 | Virus Detected | 403 Forbidden | X-Content-Type-Options: nosniff | `{"success": false, "error": {...}}` |
| AC-002 | Scan Failure | 500 or 503 | Retry-After (if 503) | `{"success": false, "error": {...}}` |
| AC-003 | Blocked File Type | 400 or 415 | - | `{"success": false, "error": {...}}` |
| AC-004 | Rate Limited | 429 Too Many Requests | Retry-After | `{"success": false, "error": {...}}` |
| AC-005 | File Too Large | 413 Payload Too Large | - | `{"success": false, "error": {...}}` |
| AC-006 | Successful Upload | 201 Created | Location header | `{"success": true, "data": {...}}` |

---

## Test Execution

### Quick Start (Recommended)

```bash
# 1. Start services
docker compose up -d

# 2. Wait for ClamAV to be ready (2-3 minutes for signature loading)
docker compose logs -f clamav

# 3. Run comprehensive test suite
cd postman
./run-virus-scanner-tests.sh
```

### Manual Testing

#### Unit Tests

```bash
# Run all virus scanner unit tests
go test -v ./internal/security/virus_scanner_test.go -short

# Run specific test
go test -v ./internal/security/virus_scanner_test.go -run TestVirusScanner_ScanCleanFile

# Run with coverage
go test -coverprofile=coverage.out ./internal/security/...
go tool cover -html=coverage.out
```

#### Integration Tests (Requires ClamAV)

```bash
# Start ClamAV container
docker run -d --name clamav -p 3310:3310 clamav/clamav

# Wait for ClamAV to be ready
timeout 300 bash -c 'until docker exec clamav clamdscan --version; do sleep 10; done'

# Run integration tests
CLAMAV_ADDRESS=localhost:3310 go test -v ./internal/security/virus_scanner_test.go

# Stop ClamAV
docker stop clamav && docker rm clamav
```

#### Postman E2E Tests

```bash
# Install Newman
npm install -g newman newman-reporter-htmlextra

# Create test user and get token
# (This is automated in run-virus-scanner-tests.sh)

# Run Postman collection
newman run postman/athena-virus-scanner-tests.postman_collection.json \
    --environment postman/test-local.postman_environment.json \
    --env-var "baseUrl=http://localhost:8080" \
    --env-var "access_token=YOUR_TOKEN_HERE" \
    --reporters cli,htmlextra \
    --reporter-htmlextra-export postman/newman/virus-scanner-report.html
```

#### Breaking Scenario Tests

```bash
# Test 1: Network interruption (automated)
./postman/run-virus-scanner-tests.sh

# Test 2: Manual network interruption
# Terminal 1: Start upload
curl -X POST http://localhost:8080/api/v1/uploads/direct \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/octet-stream" \
    -H "X-Filename: eicar.txt" \
    --data-binary "X5O!P%@AP[4\PZX54(P^)7CC)7}\$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!\$H+H*"

# Terminal 2: Kill ClamAV mid-scan
sleep 0.5 && docker stop clamav

# Expected: Upload should fail with scan error (500/503)
# If upload succeeds (201), P1 vulnerability still exists!
```

#### Performance Benchmarks

```bash
# Run Go benchmarks
go test -bench=. -benchmem -benchtime=10s ./internal/security/virus_scanner_test.go

# Run load test with Apache Bench
ab -n 1000 -c 10 -T application/json \
    -H "Authorization: Bearer $TOKEN" \
    -p test-payload.json \
    http://localhost:8080/api/v1/uploads/initiate

# Monitor resource usage
docker stats

# Check memory usage during large file scan
go test -memprofile=mem.prof ./internal/security/virus_scanner_test.go -run TestVirusScanner_MemoryUsage
go tool pprof -http=:8081 mem.prof
```

---

## CI/CD Integration

### GitHub Actions

The comprehensive test suite runs automatically on:

- Every push to `main` or `develop`
- Every pull request to `main`
- Changes to virus scanner or upload code
- Manual workflow dispatch

**Workflow File**: `.github/workflows/virus-scanner-tests.yml`

**Jobs**:
1. **Unit Tests** - Fast unit tests without ClamAV
2. **Integration Tests** - Full tests with ClamAV service
3. **Edge Case Tests** - Newman E2E tests with edge cases
4. **Breaking Scenarios** - P1 vulnerability validation
5. **Performance Benchmarks** - Performance regression testing
6. **Security Audit** - gosec security scanner

**Viewing Results**:
```bash
# View workflow runs
gh workflow view "Virus Scanner Security Tests"

# Download artifacts
gh run download <run-id>

# View logs
gh run view <run-id> --log
```

### Local CI Simulation

```bash
# Run same tests as CI locally
make test-virus-scanner-ci

# Or manually:
docker compose --profile test up --abort-on-container-exit
```

---

## Test Data Management

### Test Files Location

All test data is in `/postman/test-files/security/`:

| File | Size | Purpose |
|------|------|---------|
| `eicar.txt` | 70 bytes | EICAR test virus (NOT real malware) |
| `eicar.com` | 70 bytes | EICAR as .com file |
| `clean-small.txt` | < 1KB | Small clean file |
| `10mb-exact.bin` | 10MB | Boundary test (exact buffer limit) |
| `10mb-plus-one.bin` | 10MB+1B | Boundary test (over limit) |
| `100mb-test.bin` | 100MB | Performance test (large file) |

### Generating Test Files

```bash
# EICAR test virus (standard test file)
echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > eicar.txt

# Random data files
dd if=/dev/urandom of=10mb-exact.bin bs=1M count=10
dd if=/dev/urandom of=100mb-test.bin bs=1M count=100

# Clean text file
echo "This is a clean test file" > clean-small.txt
```

### IMPORTANT: EICAR Test File

The EICAR test file is **NOT real malware**. It's a standard test file:

- Developed by the European Institute for Computer Antivirus Research
- Detected by all antivirus software as "EICAR-Test-File"
- **Safe to store in version control**
- Used worldwide for antivirus testing
- Contains no executable code
- Simply triggers AV pattern matching

**Reference**: https://www.eicar.org/download-anti-malware-testfile/

---

## Validation Checklist

### Pre-Deployment Testing

- [ ] All unit tests passing (`go test ./internal/security/...`)
- [ ] All integration tests passing (with ClamAV)
- [ ] Postman E2E tests passing (all scenarios)
- [ ] Breaking scenario tests passing (P1 vulnerability validated)
- [ ] Performance benchmarks met (< 10ms small, < 5s large)
- [ ] Memory overhead acceptable (< 50MB for 100MB file)
- [ ] No race conditions in concurrent tests
- [ ] API error responses consistent and secure
- [ ] Security audit clean (gosec, no critical issues)

### Post-Deployment Validation

- [ ] ClamAV health check endpoint responding
- [ ] EICAR test virus correctly detected and rejected
- [ ] Clean files correctly accepted and processed
- [ ] Blocked file types rejected before scanning
- [ ] Scan failures logged to `virus_scan_log` table
- [ ] Infected files quarantined (if enabled)
- [ ] Monitoring dashboards showing scan metrics
- [ ] Alert rules configured for scan failures
- [ ] Runbook created for ClamAV outages

---

## Troubleshooting

### Common Issues

#### ClamAV Connection Failed

**Symptom**: `dial tcp 127.0.0.1:3310: connect: connection refused`

**Solutions**:
1. Check if ClamAV is running: `docker ps | grep clamav`
2. Check port binding: `netstat -tlnp | grep 3310`
3. View ClamAV logs: `docker logs <clamav-container-id>`
4. Wait for signature loading (can take 2-3 minutes)
5. Restart ClamAV: `docker restart <clamav-container-id>`

#### Tests Timeout

**Symptom**: `panic: test timed out after 10m0s`

**Solutions**:
1. Increase timeout: `go test -timeout 20m`
2. Check ClamAV is responding: `echo PING | nc localhost 3310`
3. Reduce test file sizes for faster execution
4. Skip large file tests: `go test -short`

#### EICAR Not Detected

**Symptom**: EICAR test file not detected as virus

**Solutions**:
1. Update ClamAV signatures: `docker exec <clamav> freshclam`
2. Verify ClamAV version: `docker exec <clamav> clamdscan --version`
3. Test manually: `docker exec <clamav> clamdscan /path/to/eicar.txt`
4. Check fallback mode: Ensure `CLAMAV_FALLBACK_MODE=strict`

#### Performance Degradation

**Symptom**: Scan times exceed thresholds

**Solutions**:
1. Check CPU/memory: `docker stats`
2. Update ClamAV signatures: Old signatures slow down scans
3. Tune ClamAV settings: Adjust `MaxThreads`, `MaxQueue`
4. Scale ClamAV instances: Run multiple ClamAV containers
5. Enable caching: Implement scan result caching

#### False Positives

**Symptom**: Clean files incorrectly flagged as infected

**Solutions**:
1. Update ClamAV signatures: `freshclam`
2. Check file integrity: Verify file isn't actually infected
3. Add to whitelist: Configure ClamAV whitelist if needed
4. Report false positive: Submit to ClamAV team

---

## Monitoring and Alerting

### Key Metrics to Monitor

1. **Scan Success Rate**
   ```sql
   SELECT
     COUNT(CASE WHEN scan_result = 'clean' THEN 1 END)::float / COUNT(*) * 100 as success_rate
   FROM virus_scan_log
   WHERE scanned_at > NOW() - INTERVAL '5 minutes';
   ```

2. **Scan Failures**
   ```sql
   SELECT COUNT(*) as failures
   FROM virus_scan_log
   WHERE scan_result = 'error'
     AND scanned_at > NOW() - INTERVAL '1 minute';
   ```

3. **Infected Files**
   ```sql
   SELECT file_path, virus_name, user_id, scanned_at
   FROM virus_scan_log
   WHERE scan_result = 'infected'
     AND scanned_at > NOW() - INTERVAL '1 hour'
   ORDER BY scanned_at DESC;
   ```

4. **Average Scan Duration**
   ```sql
   SELECT AVG(scan_duration_ms) as avg_duration_ms
   FROM virus_scan_log
   WHERE scanned_at > NOW() - INTERVAL '1 hour';
   ```

### Alert Thresholds

| Metric | Threshold | Action |
|--------|-----------|--------|
| Scan success rate | < 95% in 5 min | Check ClamAV health |
| Scan failures | > 10 in 1 min | Investigate ClamAV/network |
| Infected files | > 5 in 1 hour | Review user behavior patterns |
| Avg scan duration | > 10s | Check ClamAV performance |
| Memory usage | > 2GB | Restart ClamAV |

---

## Best Practices

### Development

1. **Always Test Locally First**: Run full test suite before pushing
2. **Use Short Tests During Development**: `go test -short` for quick feedback
3. **Test Edge Cases**: Don't just test happy path
4. **Simulate Failures**: Test network errors, timeouts, race conditions
5. **Benchmark Changes**: Run performance tests after code changes

### Code Review

1. **Security-Critical Code Review**: All virus scanner changes require security team review
2. **Test Coverage**: Require 80%+ coverage for security modules
3. **Breaking Scenario Tests**: Add new breaking tests for each vulnerability fix
4. **Performance Impact**: Benchmark before/after for any scanner changes

### Deployment

1. **Staged Rollout**: Deploy to staging → canary → production
2. **Monitor Metrics**: Watch scan success rate during rollout
3. **Rollback Plan**: Be ready to revert if issues detected
4. **Health Checks**: Verify ClamAV integration before serving traffic

---

## References

- **Project Security**: [CLAUDE.md](/CLAUDE.md)
- **Vulnerability Assessment**: [VULNERABILITY_ASSESSMENT.md](VULNERABILITY_ASSESSMENT.md)
- **Testing Documentation**: [TESTING.md](TESTING.md)
- **ClamAV Docs**: https://docs.clamav.net/
- **EICAR Test File**: https://www.eicar.org/
- **Newman CLI**: https://learning.postman.com/docs/running-collections/using-newman-cli/
- **Go Testing**: https://golang.org/pkg/testing/

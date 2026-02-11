# Virus Scanner Testing - Quick Reference

## 🚀 Quick Start

```bash
# 1. Start services with ClamAV
docker compose up -d

# 2. Wait for ClamAV (2-3 minutes)
docker compose logs -f clamav

# 3. Run all tests
cd postman && ./run-virus-scanner-tests.sh
```

---

## 📁 Key Files

| File | Purpose | Location |
|------|---------|----------|
| **Postman Collection** | E2E tests with edge cases | `/postman/athena-virus-scanner-tests.postman_collection.json` |
| **Test Runner Script** | Automated test execution | `/postman/run-virus-scanner-tests.sh` |
| **GitHub Workflow** | CI/CD integration | `/.github/workflows/virus-scanner-tests.yml` |
| **Vulnerability Report** | Security analysis | `/internal/security/VULNERABILITY_ASSESSMENT.md` |
| **Testing Guide** | Comprehensive docs | `/internal/security/TESTING_GUIDE.md` |
| **Test Report** | Executive summary | `/VIRUS_SCANNER_TEST_REPORT.md` |

---

## 🧪 Common Test Commands

### Unit Tests
```bash
# All unit tests
go test -v ./internal/security/virus_scanner_test.go -short

# With coverage
go test -coverprofile=coverage.out ./internal/security/...
go tool cover -html=coverage.out
```

### Integration Tests (Requires ClamAV)
```bash
# Start ClamAV
docker run -d --name clamav -p 3310:3310 clamav/clamav

# Run tests
CLAMAV_ADDRESS=localhost:3310 go test -v ./internal/security/virus_scanner_test.go
```

### E2E Tests
```bash
# Full automated suite
./postman/run-virus-scanner-tests.sh

# Just Postman collection
newman run postman/athena-virus-scanner-tests.postman_collection.json \
    --environment postman/test-local.postman_environment.json
```

### Performance Benchmarks
```bash
go test -bench=. -benchmem -benchtime=10s ./internal/security/virus_scanner_test.go
```

---

## 🔒 Breaking Scenario Tests (P1 Vulnerability)

### Test 1: Network Interruption
```bash
# Upload EICAR test file
curl -X POST http://localhost:8080/api/v1/uploads/direct \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/octet-stream" \
    -H "X-Filename: eicar.txt" \
    --data-binary 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*'

# Expected: 403 Forbidden (virus detected)
# If 201 Created: P1 VULNERABILITY EXISTS!
```

### Test 2: ClamAV Unavailable
```bash
# Stop ClamAV
docker stop clamav

# Try upload
curl -X POST http://localhost:8080/api/v1/uploads/direct \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/octet-stream" \
    --data-binary "test file"

# Expected: 500/503 (scan service unavailable)
# If 201 Created: FALLBACK VULNERABILITY EXISTS!

# Restart ClamAV
docker start clamav
```

---

## 📊 Test Categories

| Category | Count | Files |
|----------|-------|-------|
| **Edge Cases** | 8 tests | Boundary conditions, malformed input |
| **Breaking Scenarios** | 6 tests | P1 vulnerability validation |
| **Security Validation** | 8 tests | EICAR, file blocking, archives |
| **Performance Tests** | 7 tests | Latency, memory, concurrency |
| **API Contract** | 6 tests | Error formats, status codes |
| **TOTAL** | **35 tests** | 100% pass rate required |

---

## 🎯 Test Success Criteria

### Security
- ✅ EICAR test virus detected and rejected
- ✅ Infected files rejected even with network errors
- ✅ No fallback mode bypass possible
- ✅ Blocked file types rejected before scan
- ✅ No race conditions in concurrent uploads

### Performance
- ✅ Small file scan (< 1KB): < 10ms
- ✅ Medium file scan (10MB): < 2s
- ✅ Large file scan (100MB): < 5s
- ✅ Memory overhead: < 50MB for 100MB file
- ✅ Concurrent scans: No deadlocks

### API Contract
- ✅ Virus detected: 403 Forbidden
- ✅ Scan failure: 500/503 with Retry-After
- ✅ Blocked file: 400/415
- ✅ Rate limited: 429 with Retry-After
- ✅ All errors: Consistent JSON format

---

## 🔍 Monitoring Queries

### Recent Scan Failures
```sql
SELECT * FROM virus_scan_log
WHERE scan_result = 'error'
  AND scanned_at > NOW() - INTERVAL '1 hour'
ORDER BY scanned_at DESC;
```

### Infected Files Detected
```sql
SELECT user_id, file_path, virus_name, scanned_at
FROM virus_scan_log
WHERE scan_result = 'infected'
  AND scanned_at > NOW() - INTERVAL '24 hours'
ORDER BY scanned_at DESC;
```

### Scan Success Rate
```sql
SELECT
  COUNT(CASE WHEN scan_result = 'clean' THEN 1 END)::float / COUNT(*) * 100 as success_rate
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '5 minutes';
```

---

## 🚨 Alert Thresholds

| Metric | Threshold | Action |
|--------|-----------|--------|
| Scan success rate | < 95% | Check ClamAV health |
| Scan failures | > 10/min | Investigate network/ClamAV |
| Infected files | > 5/hour | Review user patterns |
| Avg scan duration | > 10s | Check ClamAV performance |

---

## 🛠️ Troubleshooting

### ClamAV Connection Failed
```bash
# Check if running
docker ps | grep clamav

# Check logs
docker logs <clamav-container-id>

# Restart ClamAV
docker restart <clamav-container-id>

# Wait for ready
timeout 300 bash -c 'until docker exec <clamav-id> clamdscan --version; do sleep 10; done'
```

### Tests Timeout
```bash
# Increase timeout
go test -timeout 20m

# Skip long tests
go test -short

# Run specific test
go test -run TestVirusScanner_ScanCleanFile
```

### EICAR Not Detected
```bash
# Update signatures
docker exec <clamav-id> freshclam

# Test manually
docker exec <clamav-id> clamdscan /path/to/eicar.txt

# Check fallback mode
echo $CLAMAV_FALLBACK_MODE  # Should be 'strict'
```

---

## 📦 Test Data Files

| File | Size | Purpose |
|------|------|---------|
| `eicar.txt` | 70B | EICAR test virus (safe) |
| `10mb-exact.bin` | 10MB | Boundary test |
| `10mb-plus-one.bin` | 10MB+1B | Over boundary |
| `100mb-test.bin` | 100MB | Performance test |
| `clean-small.txt` | < 1KB | Clean file test |

**Generate**:
```bash
# EICAR (standard test virus - NOT real malware)
echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > eicar.txt

# Random data files
dd if=/dev/urandom of=10mb-exact.bin bs=1M count=10
dd if=/dev/urandom of=100mb-test.bin bs=1M count=100
```

---

## 🔐 Security Configuration

### Production Settings (REQUIRED)
```bash
VIRUS_SCAN_ENABLED=true
CLAMAV_FALLBACK_MODE=strict  # CRITICAL!
VIRUS_SCAN_MANDATORY=true
REJECT_ON_SCAN_WARNING=true
CLAMAV_MAX_RETRIES=3
CLAMAV_TIMEOUT=300
```

### Test Settings
```bash
CLAMAV_ADDRESS=localhost:3310
QUARANTINE_DIR=/tmp/quarantine
CLAMAV_AUDIT_LOG=/var/log/virus-scans.log
```

---

## 🎭 CI/CD

### GitHub Actions Workflow
- **Trigger**: Push to main/develop, PRs, manual dispatch
- **Jobs**: Unit → Integration → Edge Cases → Breaking → Performance → Audit
- **Runtime**: ~25 minutes
- **View**: `gh workflow view "Virus Scanner Security Tests"`

### Run Locally (Same as CI)
```bash
docker compose --profile test up --abort-on-container-exit
```

---

## 📚 Documentation

| Document | Description |
|----------|-------------|
| [VULNERABILITY_ASSESSMENT.md](VULNERABILITY_ASSESSMENT.md) | P1 vulnerability details, fix, impact |
| [TESTING_GUIDE.md](TESTING_GUIDE.md) | Comprehensive testing documentation |
| [/VIRUS_SCANNER_TEST_REPORT.md](/VIRUS_SCANNER_TEST_REPORT.md) | Executive summary, test results |
| [TESTING.md](TESTING.md) | TDD approach, test structure |
| [QUICK_START.md](QUICK_START.md) | Fast setup guide |

---

## ✅ Pre-Deployment Checklist

- [ ] All unit tests passing
- [ ] All integration tests passing
- [ ] Postman E2E tests passing
- [ ] Breaking scenario tests passing (P1 validated)
- [ ] Performance benchmarks met
- [ ] `CLAMAV_FALLBACK_MODE=strict` confirmed
- [ ] Monitoring dashboards configured
- [ ] Alert rules set up
- [ ] Runbook created for ClamAV outages

---

## 🆘 Emergency Response

### If Infected File Accepted

1. **Immediate**:
   ```bash
   # Stop uploads
   export VIRUS_SCAN_ENABLED=false
   docker compose restart app
   ```

2. **Investigate**:
   ```sql
   SELECT * FROM virus_scan_log
   WHERE scan_result IN ('warning', 'clean')
     AND metadata->>'fallback_used' = 'true';
   ```

3. **Quarantine**:
   ```bash
   ./scripts/emergency-quarantine.sh
   ```

4. **Notify**: Alert security team and affected users

5. **Fix**: Deploy hotfix or revert to known good version

---

## 📞 Contacts

- **Security Team**: security@example.com
- **On-Call**: See PagerDuty rotation
- **Docs**: See links above
- **Support**: Slack #security-incidents

---

**Last Updated**: 2025-01-16
**Version**: 1.0
**Status**: ✅ Production Ready

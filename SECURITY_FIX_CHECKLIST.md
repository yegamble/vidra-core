# Security Fix Checklist: Virus Scanner P1 Vulnerability

**Ticket**: P1-SECURITY-001
**Vulnerability**: Stream exhaustion bypass in virus scanning retry logic
**Severity**: CRITICAL
**Target**: Deploy within 24 hours

---

## Pre-Deployment Checklist

### Code Changes

- [ ] **Modify `/internal/security/virus_scanner.go`**
  - [ ] Add `bytes.Buffer` to buffer entire stream before scanning
  - [ ] Calculate SHA256 hash during buffering
  - [ ] Create fresh `bytes.NewReader()` for each retry attempt
  - [ ] Add pre-scan and post-scan hash comparison
  - [ ] Implement memory limits (512MB max buffer size)
  - [ ] Add structured logging with stream hash
  - [ ] Add `StreamHash`, `PreScanHash`, `PostScanHash` fields to `ScanResult`

- [ ] **Add Database Logging Methods**
  - [ ] Implement `logScanSuccess()` method
  - [ ] Implement `logScanFailure()` method
  - [ ] Implement `triggerSecurityAlert()` method
  - [ ] Implement `quarantineStream()` method (optional but recommended)

- [ ] **Update Configuration**
  - [ ] Add `db *sql.DB` field to `VirusScanner` struct
  - [ ] Add `metrics *Metrics` field to `VirusScanner` struct
  - [ ] Update `NewVirusScanner()` constructor to accept these dependencies

### Testing

- [ ] **Unit Tests**
  - [ ] Test stream retry with infected content (EICAR)
  - [ ] Test stream retry with clean content
  - [ ] Test hash consistency across retries
  - [ ] Test fail-closed behavior (reject on scan failure)
  - [ ] Test memory limit enforcement
  - [ ] Test integrity check (pre/post hash comparison)

- [ ] **Integration Tests**
  - [ ] Test with actual ClamAV daemon
  - [ ] Test network failure scenarios
  - [ ] Test concurrent uploads during scan failures
  - [ ] Test database logging persistence
  - [ ] Test quarantine directory creation and permissions

- [ ] **Security Tests**
  - [ ] Upload EICAR during simulated ClamAV outage
  - [ ] Verify infected files are rejected (not marked clean)
  - [ ] Verify audit logs capture all scan attempts
  - [ ] Test large file uploads near memory limit
  - [ ] Verify TOCTOU mitigation (hash verification)

### Database

- [ ] **Verify Migration Applied**
  ```bash
  atlas migrate status --dir "file://migrations" \
    --url "postgres://user:pass@localhost:5432/athena?sslmode=disable"
  ```
  - [ ] Confirm `virus_scan_log` table exists
  - [ ] Confirm indexes created
  - [ ] Confirm constraints applied

- [ ] **Test Database Logging**
  ```sql
  -- After running tests, verify logs captured
  SELECT * FROM virus_scan_log ORDER BY scanned_at DESC LIMIT 10;

  -- Check for scan failures
  SELECT scan_result, COUNT(*) FROM virus_scan_log GROUP BY scan_result;
  ```

### Configuration

- [ ] **Update Environment Variables**
  ```bash
  # Production environment
  CLAMAV_ADDRESS=clamav:3310
  CLAMAV_TIMEOUT=300
  CLAMAV_MAX_RETRIES=3
  CLAMAV_RETRY_DELAY=1
  CLAMAV_FALLBACK_MODE=strict  # CRITICAL: Must be 'strict' in production
  CLAMAV_AUTO_QUARANTINE=true
  QUARANTINE_DIR=/var/quarantine
  CLAMAV_AUDIT_LOG=/var/log/athena/virus_scan_audit.log
  QUARANTINE_RETENTION_DAYS=30
  ```

- [ ] **Verify ClamAV Health**
  ```bash
  # Check ClamAV is running
  docker-compose ps clamav

  # Check ClamAV signatures updated
  docker-compose exec clamav freshclam

  # Test ClamAV connection
  echo "PING" | nc localhost 3310
  # Should respond: PONG
  ```

### Monitoring

- [ ] **Deploy Prometheus Metrics**
  - [ ] `virus_scan_duration_seconds` histogram
  - [ ] `virus_scan_retries_total` counter
  - [ ] `malware_detections_total` counter
  - [ ] `virus_scan_failures_total` counter
  - [ ] `quarantine_operations_total` counter

- [ ] **Configure Alerts**
  ```yaml
  # AlertManager rules
  - alert: HighMalwareDetectionRate
    expr: rate(malware_detections_total[5m]) > 0.1
    for: 5m
    severity: critical

  - alert: VirusScannerDown
    expr: up{job="clamav"} == 0
    for: 1m
    severity: critical

  - alert: ExcessiveScanFailures
    expr: rate(virus_scan_failures_total[5m]) > 0.5
    for: 5m
    severity: warning
  ```

- [ ] **Test Alert Routing**
  - [ ] Upload EICAR test file
  - [ ] Verify `HighMalwareDetectionRate` alert fires
  - [ ] Confirm alert reaches PagerDuty/Slack/Email

### Security Review

- [ ] **Code Review Completed**
  - [ ] Security team reviewed changes
  - [ ] No hardcoded credentials
  - [ ] Error messages don't leak sensitive info
  - [ ] Logging excludes file contents/PII
  - [ ] All inputs validated

- [ ] **Threat Model Updated**
  - [ ] Document fix in security documentation
  - [ ] Update attack surface analysis
  - [ ] Review for similar vulnerabilities in codebase

### Documentation

- [ ] **Update Documentation**
  - [ ] Add security incident to changelog
  - [ ] Document new configuration options
  - [ ] Update deployment runbook
  - [ ] Create incident response playbook

- [ ] **Team Communication**
  - [ ] Brief engineering team on vulnerability
  - [ ] Train on secure stream handling
  - [ ] Add to security best practices wiki

---

## Deployment Steps

### 1. Backup & Rollback Plan

```bash
# Backup current database
pg_dump -h localhost -U athena -d athena > backup_pre_security_fix_$(date +%Y%m%d).sql

# Tag current production version
git tag production-pre-security-fix-$(date +%Y%m%d)
git push origin production-pre-security-fix-$(date +%Y%m%d)

# Prepare rollback script
cat > rollback.sh <<EOF
#!/bin/bash
set -e
echo "Rolling back to previous version..."
kubectl set image deployment/athena-api athena-api=athena-api:production-pre-security-fix
kubectl rollout status deployment/athena-api
echo "Rollback complete"
EOF
chmod +x rollback.sh
```

### 2. Deploy to Staging

```bash
# Build new image
docker build -t athena-api:security-fix-v1 .

# Push to registry
docker push registry.example.com/athena-api:security-fix-v1

# Deploy to staging
kubectl set image deployment/athena-api athena-api=registry.example.com/athena-api:security-fix-v1 -n staging

# Wait for rollout
kubectl rollout status deployment/athena-api -n staging
```

### 3. Staging Validation

```bash
# Test clean file upload
curl -X POST https://staging.example.com/api/v1/uploads \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@testdata/clean_video.mp4"

# Test EICAR upload (should be rejected)
curl -X POST https://staging.example.com/api/v1/uploads \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@testdata/eicar.txt"
# Expected: 400 Bad Request, "malware detected"

# Check virus_scan_log table
psql -h staging-db -U athena -c "SELECT * FROM virus_scan_log ORDER BY scanned_at DESC LIMIT 5;"

# Verify metrics endpoint
curl https://staging.example.com/metrics | grep virus_scan
```

### 4. Production Deployment

```bash
# Enable maintenance mode (optional)
kubectl scale deployment/athena-api --replicas=0 -n production

# Apply database migration
atlas migrate apply --dir "file://migrations" \
  --url "postgres://user:pass@prod-db:5432/athena?sslmode=disable"

# Deploy new version
kubectl set image deployment/athena-api athena-api=registry.example.com/athena-api:security-fix-v1 -n production

# Scale up
kubectl scale deployment/athena-api --replicas=3 -n production

# Monitor rollout
kubectl rollout status deployment/athena-api -n production

# Disable maintenance mode
# (or wait for health checks to pass)
```

### 5. Post-Deployment Validation

```bash
# Health check
curl https://api.example.com/health
# Expected: {"status": "ok"}

# Check ClamAV connectivity
kubectl exec -it deployment/athena-api -n production -- nc -zv clamav 3310
# Expected: Connection successful

# Upload test file
curl -X POST https://api.example.com/api/v1/uploads \
  -H "Authorization: Bearer $PROD_TOKEN" \
  -F "file=@testdata/clean_video.mp4"
# Expected: 200 OK

# Check logs for errors
kubectl logs -f deployment/athena-api -n production | grep -i error

# Monitor metrics
curl https://api.example.com/metrics | grep virus_scan_duration_seconds
```

### 6. Monitoring (First 24 Hours)

- [ ] **Hour 0-1**: Watch for immediate errors
  - Check application logs
  - Monitor error rates
  - Verify uploads succeeding

- [ ] **Hour 1-4**: Monitor resource usage
  - Check memory usage (buffering impact)
  - Monitor CPU usage
  - Verify ClamAV not overloaded

- [ ] **Hour 4-24**: Long-term stability
  - Check for memory leaks
  - Monitor scan duration trends
  - Review audit logs for anomalies

---

## Rollback Criteria

Immediately rollback if:

- [ ] Error rate > 5%
- [ ] Memory usage > 90%
- [ ] ClamAV connection failures > 10%
- [ ] Upload success rate < 95%
- [ ] Critical bugs reported

### Rollback Procedure

```bash
# Execute rollback script
./rollback.sh

# Or manual rollback
kubectl rollout undo deployment/athena-api -n production

# Verify rollback
kubectl rollout status deployment/athena-api -n production

# Check service health
curl https://api.example.com/health
```

---

## Post-Deployment Tasks

### Immediate (Day 1)

- [ ] Monitor alerts for malware detections
- [ ] Review virus_scan_log for anomalies
- [ ] Verify no false positives reported
- [ ] Update status page (if public incident)

### Short-term (Week 1)

- [ ] Analyze scan performance metrics
- [ ] Review quarantined files
- [ ] Generate security report for stakeholders
- [ ] Schedule team retrospective

### Long-term (Month 1)

- [ ] External security audit
- [ ] Penetration testing
- [ ] Update security training materials
- [ ] Plan Phase 2 enhancements (multi-engine scanning, etc.)

---

## Communication Plan

### Internal Communication

**Engineering Team**:
```
Subject: URGENT - Security Fix Deployment - Virus Scanner

Team,

We've identified and fixed a critical P1 security vulnerability in the virus scanner
that could allow malware to bypass scanning under certain conditions.

WHAT: Fix for stream retry logic that previously could scan empty payloads
WHEN: Deploying to production TODAY at [TIME]
IMPACT: Brief deployment window (~15 minutes), no user-facing changes
ACTION REQUIRED: Monitor alerts, report any anomalies immediately

Details: See SECURITY_ASSESSMENT_VIRUS_SCANNER_P1.md

[Your Name]
```

**Leadership**:
```
Subject: Security Vulnerability Remediation - Status Update

Leadership team,

We've identified and are remediating a critical security vulnerability in our file
upload system. Key points:

- RISK: Malware could bypass virus scanning in specific failure scenarios
- IMPACT: Potential for infected files to be stored/distributed
- REMEDIATION: Fix developed, tested, deploying today
- USER IMPACT: None - transparent fix
- COMPLIANCE: Enhanced audit logging added, incident documented

No evidence of exploitation. Proactive fix based on code review.

Full report available on request.

[Your Name]
```

### External Communication (If Required)

**Customer Notice** (Only if evidence of exploitation):
```
Subject: Security Update - Enhanced File Scanning

Dear Valued Customer,

We've deployed a security update to enhance our file scanning capabilities.
This update improves our malware detection system and adds additional
safety measures.

- Your account and data remain secure
- No action required on your part
- Enhanced protection now active

If you have any questions, please contact support@example.com

Thank you for your trust,
[Company] Security Team
```

---

## Success Criteria

Deployment is successful when:

- [x] All tests pass in staging
- [x] Production deployment completes without errors
- [x] Upload functionality working normally
- [x] Virus scanning functioning correctly
- [x] Audit logs capturing scan events
- [x] Alerts configured and firing correctly
- [x] No increase in error rates
- [x] Memory usage within acceptable limits
- [x] Team trained on new security measures

---

## Sign-off

**Required Approvals**:

- [ ] **Engineering Lead**: _____________________ Date: _______
- [ ] **Security Team**: _____________________ Date: _______
- [ ] **DevOps Lead**: _____________________ Date: _______
- [ ] **CTO/CISO**: _____________________ Date: _______

**Deployment Authorization**:

Once all approvals received, authorized to proceed with production deployment.

**Post-Deployment Verification**:

- [ ] **Deployment Lead**: _____________________ Date: _______ Time: _______
  - Confirmed: Production deployment successful
  - Confirmed: All health checks passing
  - Confirmed: Monitoring active

---

**Document Version**: 1.0
**Last Updated**: 2025-11-16
**Next Review**: Post-deployment retrospective

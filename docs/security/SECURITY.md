# Security Policy

## Reporting Security Vulnerabilities

If you discover a security vulnerability in Athena, please report it by creating a private security advisory on GitHub or by contacting the maintainers directly at <security@athena-project.com>.

**Do not** disclose security vulnerabilities in public issues or pull requests.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 1.0   | :x:                |

## Security Advisories

### CVE-ATHENA-2025-001: Virus Scanner Retry Logic Bypass

**Date Reported**: 2025-01-16
**Date Fixed**: 2025-01-16
**Severity**: HIGH (CVSS 7.5)
**Status**: FIXED

#### Summary

A vulnerability was discovered in the virus scanning retry logic that could allow infected files to bypass malware detection under specific network conditions. When ClamAV connection retries were exhausted without receiving a valid scan response, the fallback mode configuration was incorrectly applied, potentially allowing infected files through in non-strict mode.

#### Vulnerability Details

**Affected Component**: `/internal/security/virus_scanner.go` - ScanFile() and ScanStream() methods
**Affected Versions**: All versions prior to commit [COMMIT_HASH]
**Attack Vector**: Network/Remote
**Prerequisites**: ClamAV service degradation or network instability + `CLAMAV_FALLBACK_MODE` set to `warn` or `allow`

**Technical Description**:

The retry loop implementation (lines 158-196 in virus_scanner.go) had a logic flaw:

```go
for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
    // ... retry logic ...

    responses, err := s.client.ScanStream(file, make(chan bool))
    if err != nil {
        scanErr = err
        continue  // Retry on error
    }

    // Get first response
    for resp := range responses {
        response = resp
        break
    }

    if response != nil {
        scanErr = nil
        break  // Exit retry loop with valid response
    }
    // BUG: If response == nil but no error, loop continues
    // After max retries, falls through to fallback mode handling
}

// Fallback mode handling (lines 200-224)
if scanErr != nil {
    // Apply fallback mode (strict/warn/allow)
    // VULNERABILITY: Also triggers when response == nil after exhausted retries
}
```

**Exploitation Scenario**:

1. Attacker uploads infected file
2. Attacker causes ClamAV service degradation (DoS, network partition, resource exhaustion)
3. Scanner retry loop exhausts attempts without receiving valid response
4. `scanErr` remains set, `response` is nil
5. Fallback mode handling incorrectly allows file in `warn` or `allow` modes
6. Infected file bypasses scanning and proceeds to processing/storage

**Impact Assessment**:

- **Confidentiality**: LOW - No direct data exposure
- **Integrity**: HIGH - Malware could be uploaded and distributed
- **Availability**: MEDIUM - Malware could impact system resources
- **Overall**: HIGH (CVSS Base Score: 7.5)

#### Fix Implementation

**Commit**: [COMMIT_HASH]
**Pull Request**: #[PR_NUMBER]

**Changes**:

1. Added explicit nil check after retry loop exhaustion:

```go
// After retry loop
result.ScanDuration = time.Since(start)

// Handle scan errors (network/connection issues)
if scanErr != nil {
    log.Error().Err(scanErr).Msg("ClamAV scan failed after retries")
    // Apply fallback mode only for connection errors
    switch s.config.FallbackMode { ... }
}

// ADDED: Explicit check for missing response
if response == nil {
    result.Status = ScanStatusError
    return result, fmt.Errorf("no scan response received")
}
```

2. Clarified fallback mode semantics:
   - Fallback mode applies ONLY to connection/network errors
   - Missing scan responses always return error
   - Infected scan results always reject file (never subject to fallback)

3. Enhanced logging for audit trail:
   - Log all retry attempts with attempt number
   - Log fallback mode activations separately
   - Log reason for scan failures

**Testing**:

Added comprehensive test coverage in `virus_scanner_test.go`:

- TestVirusScanner_ConnectionFallback: Verifies fallback mode behavior
- TestVirusScanner_DetectEICAR: Ensures infected files always rejected
- TestVirusScanner_ScanTimeout: Tests timeout handling
- TestVirusScanner_ConcurrentScans: Validates thread safety

#### Remediation Steps

**For Deployed Instances**:

1. **Immediate**: Update to patched version
2. **Audit**: Review `virus_scan_log` table for suspicious entries:

```sql
SELECT * FROM virus_scan_log
WHERE scan_result = 'warning'
  AND scanned_at > '2024-01-01'  -- Adjust date range
ORDER BY scanned_at DESC;
```

3. **Re-scan**: Quarantine and re-scan files uploaded during vulnerability window:

```sql
-- Identify potentially affected uploads
SELECT vsl.*, v.title, v.file_path
FROM virus_scan_log vsl
JOIN videos v ON v.id = vsl.video_id
WHERE vsl.scan_result IN ('warning', 'error')
  AND vsl.scanned_at BETWEEN '[DEPLOYMENT_DATE]' AND '[PATCH_DATE]';
```

4. **Configuration**: Ensure production uses strict mode:

```bash
# /etc/athena/.env or environment variables
CLAMAV_FALLBACK_MODE=strict
```

5. **Monitoring**: Enable alerts for scan failures:

```sql
-- Monitor for increased scan failures
SELECT DATE(scanned_at) as date,
       scan_result,
       COUNT(*) as count
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '7 days'
GROUP BY DATE(scanned_at), scan_result
ORDER BY date DESC;
```

**For Developers**:

1. Update local development environment:

```bash
git pull origin main
make deps
make migrate
```

2. Verify ClamAV configuration:

```bash
# Ensure ClamAV is running
docker compose ps clamav

# Test scanner connectivity
curl -X POST http://localhost:3310/scan
```

3. Run security test suite:

```bash
make test-security
# or
go test -v ./internal/security/...
```

#### Mitigation Factors

The following factors reduce the exploitability and impact:

1. **Default Configuration**: Default `CLAMAV_FALLBACK_MODE=strict` rejects uploads when scanner unavailable
2. **Network Isolation**: ClamAV typically runs on same host/network as application (low latency)
3. **Health Monitoring**: Production deployments should monitor ClamAV availability
4. **Audit Logging**: All scan results logged to `virus_scan_log` table for forensic analysis
5. **Quarantine System**: Infected files automatically quarantined even if processing continues
6. **Short Window**: Exploitation requires sustained ClamAV degradation during upload

#### Recommended Security Hardening

1. **Production Configuration**:

```bash
# .env.production
CLAMAV_FALLBACK_MODE=strict       # Never bypass scanning
CLAMAV_TIMEOUT=300                # 5min timeout for large files
CLAMAV_MAX_RETRIES=3              # Limited retries
CLAMAV_AUTO_QUARANTINE=true       # Auto-quarantine infected files
QUARANTINE_DIR=/var/quarantine    # Isolated filesystem mount
QUARANTINE_RETENTION_DAYS=90      # Extended retention for forensics
CLAMAV_AUDIT_LOG=/var/log/athena/virus_scan.log  # Dedicated audit log
```

2. **ClamAV Service Hardening**:

```yaml
# docker-compose.yml
services:
  clamav:
    image: clamav/clamav:latest
    container_name: clamav
    restart: unless-stopped
    volumes:
      - clamav-data:/var/lib/clamav
    healthcheck:
      test: ["CMD", "clamdscan", "--ping"]
      interval: 30s
      timeout: 10s
      retries: 3
    resources:
      limits:
        memory: 2G
      reservations:
        memory: 1G
    networks:
      - athena-backend  # Isolated network
```

3. **Monitoring & Alerting**:

```yaml
# prometheus alerts
- alert: ClamAVDown
  expr: up{job="clamav"} == 0
  for: 5m
  annotations:
    summary: "ClamAV service unavailable"

- alert: VirusScanFailureRate
  expr: rate(virus_scan_failures_total[5m]) > 0.1
  annotations:
    summary: "High virus scan failure rate"
```

4. **Network Security**:
   - Isolate ClamAV on dedicated network segment
   - Firewall rules restricting ClamAV to application server only
   - Rate limit uploads to prevent DoS attacks on scanner

5. **Operational Procedures**:
   - Weekly ClamAV signature database updates (automated via freshclam)
   - Monthly quarantine review and cleanup
   - Quarterly security audits of scan logs
   - Incident response plan for detected malware

#### References

- **ClamAV Documentation**: <https://docs.clamav.net/>
- **go-clamd Library**: <https://github.com/dutchcoders/go-clamd>
- **EICAR Test Files**: <https://www.eicar.org/download-anti-malware-testfile/>
- **OWASP File Upload Cheat Sheet**: <https://cheatsheetseries.owasp.org/cheatsheets/File_Upload_Cheat_Sheet.html>

#### Timeline

- **2025-01-16 09:00 UTC**: Vulnerability discovered during security code review
- **2025-01-16 10:30 UTC**: Fix implemented and tested
- **2025-01-16 12:00 UTC**: Security advisory drafted
- **2025-01-16 14:00 UTC**: Patch released to main branch
- **2025-01-16 16:00 UTC**: Advisory published

#### Credits

Discovered and fixed by: Athena Security Team

---

## General Security Measures

### Authentication & Authorization

- JWT-based authentication with short-lived access tokens (15min)
- Refresh tokens with rotation (7 days)
- Two-Factor Authentication (2FA) with TOTP (RFC 6238)
- 10 backup codes for account recovery
- OAuth2 with PKCE for third-party integrations
- Rate limiting on authentication endpoints

### Data Protection

- Passwords hashed with bcrypt (cost 12)
- Sensitive data encrypted at rest (AES-256-GCM)
- TLS 1.2+ enforced for all connections
- Database connection encryption with SSL/TLS
- End-to-end encryption option for user messaging

### Input Validation

- Strict MIME type validation (magic bytes + extension)
- File size limits enforced (10GB for videos)
- Content Security Policy (CSP) headers
- SQL injection prevention via parameterized queries
- XSS protection with output encoding

### Infrastructure Security

- Docker image scanning with Trivy
- Dependency vulnerability scanning (govulncheck)
- Automated security updates
- Least-privilege principle for service accounts
- Network segmentation and firewall rules

### Monitoring & Incident Response

- Structured security logging (audit trail)
- Real-time alerting for suspicious activity
- Automated virus scanning with quarantine
- Incident response runbooks
- Regular security audits and penetration testing

## Security Best Practices for Developers

1. **Never commit secrets**: Use `.env.example` with placeholder values
2. **Validate all inputs**: Trust no user data
3. **Use parameterized queries**: Prevent SQL injection
4. **Apply least privilege**: Minimal permissions for services
5. **Keep dependencies updated**: Regular `go get -u`
6. **Review security advisories**: Monitor CVEs for dependencies
7. **Run security tests**: `make test-security` before commits
8. **Follow secure coding guidelines**: OWASP Top 10 awareness

## Security Testing

Run the full security test suite:

```bash
# All security-related tests
make test-security

# Virus scanner tests
go test -v ./internal/security/...

# Authentication tests
go test -v ./internal/auth/...

# Input validation tests
go test -v ./internal/validation/...

# Integration security tests
go test -v -tags=security ./tests/integration/...
```

## Compliance

Athena follows security best practices aligned with:

- OWASP Top 10 (2021)
- NIST Cybersecurity Framework
- CWE/SANS Top 25 Most Dangerous Software Errors
- GDPR data protection requirements

## Contact

For security-related inquiries:

- **Email**: <security@athena-project.com> (for vulnerability reports)
- **GitHub Security Advisories**: [Create Private Advisory](https://github.com/yegamble/athena/security/advisories/new)
- **Public Discussions**: [GitHub Discussions](https://github.com/yegamble/athena/discussions) (non-sensitive topics only)

## Acknowledgments

We appreciate responsible disclosure and will credit security researchers who report vulnerabilities following our disclosure policy.

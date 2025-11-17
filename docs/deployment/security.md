# Security Configuration Guide

## Overview

This guide covers security best practices and configuration for production deployment of Athena.

## Environment Security

### Secret Generation

```bash
# Generate strong secrets
openssl rand -hex 32  # For JWT_SECRET
openssl rand -hex 32  # For ENCRYPTION_KEY
openssl rand -hex 16  # For API keys

# Generate passwords
openssl rand -base64 32  # For database passwords
```

### Environment Variables

Never commit secrets to version control. Use:
- Environment files (`.env`) with restricted permissions
- Secret management systems (Vault, AWS Secrets Manager)
- Kubernetes secrets
- Docker secrets

```bash
# Secure .env file
chmod 600 .env.production
chown $USER:$USER .env.production
```

## Application Security

### Security Headers

The application automatically sets these headers:

```go
// Configured in middleware/security.go
X-Frame-Options: DENY
X-Content-Type-Options: nosniff
X-XSS-Protection: 1; mode=block
Content-Security-Policy: default-src 'self'
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
```

### Rate Limiting

Configure rate limits per endpoint:

```bash
# Global rate limit
RATE_LIMIT_REQUESTS_PER_MINUTE=60
RATE_LIMIT_BURST=10

# Specific endpoints
RATE_LIMIT_LOGIN=5/minute
RATE_LIMIT_UPLOAD=10/hour
RATE_LIMIT_API=100/minute
```

### CORS Configuration

```bash
# CORS settings
ENABLE_CORS=true
CORS_ORIGINS=https://yourdomain.com,https://app.yourdomain.com
CORS_METHODS=GET,POST,PUT,DELETE,OPTIONS
CORS_HEADERS=Content-Type,Authorization
CORS_CREDENTIALS=true
CORS_MAX_AGE=86400
```

## Database Security

### User Privileges

```sql
-- Create application user with minimal privileges
CREATE USER athena_app WITH PASSWORD 'strong_password';
GRANT CONNECT ON DATABASE athena TO athena_app;
GRANT USAGE ON SCHEMA public TO athena_app;

-- Grant only necessary table permissions
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO athena_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON videos TO athena_app;
GRANT SELECT, INSERT, UPDATE ON channels TO athena_app;
-- Continue for other tables...

-- Create read-only user for analytics
CREATE USER athena_readonly WITH PASSWORD 'strong_password';
GRANT CONNECT ON DATABASE athena TO athena_readonly;
GRANT USAGE ON SCHEMA public TO athena_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO athena_readonly;

-- Revoke unnecessary privileges
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE athena FROM PUBLIC;
```

### Connection Security

```bash
# Force SSL connections
DATABASE_URL=postgres://user:pass@host:5432/athena?sslmode=require

# Certificate validation
DATABASE_URL=postgres://user:pass@host:5432/athena?sslmode=verify-full&sslcert=client.crt&sslkey=client.key&sslrootcert=ca.crt
```

### SQL Injection Prevention

Always use parameterized queries:

```go
// Good - parameterized
db.Query("SELECT * FROM users WHERE id = $1", userID)

// Bad - string concatenation
db.Query("SELECT * FROM users WHERE id = " + userID)
```

## Authentication & Authorization

### JWT Configuration

```bash
# JWT settings
JWT_SECRET=<64-character-hex-string>  # Never share or commit
JWT_ACCESS_TOKEN_EXPIRY=15m           # Short-lived access tokens
JWT_REFRESH_TOKEN_EXPIRY=7d           # Longer refresh tokens
JWT_ISSUER=athena.yourdomain.com
JWT_AUDIENCE=athena-api
```

### Password Policy

```bash
# Password requirements
PASSWORD_MIN_LENGTH=12
PASSWORD_REQUIRE_UPPERCASE=true
PASSWORD_REQUIRE_LOWERCASE=true
PASSWORD_REQUIRE_NUMBER=true
PASSWORD_REQUIRE_SPECIAL=true
PASSWORD_BCRYPT_COST=12
```

### Session Management

```bash
# Session configuration
SESSION_TIMEOUT=24h
SESSION_IDLE_TIMEOUT=2h
SESSION_SECURE_COOKIE=true
SESSION_HTTP_ONLY=true
SESSION_SAME_SITE=strict
```

## File Upload Security

### MIME Type Validation

```bash
# Allowed MIME types
ALLOWED_VIDEO_TYPES=video/mp4,video/webm,video/quicktime
ALLOWED_IMAGE_TYPES=image/jpeg,image/png,image/webp
ALLOWED_SUBTITLE_TYPES=text/vtt,application/x-subrip

# File size limits
MAX_VIDEO_SIZE=5GB
MAX_IMAGE_SIZE=10MB
MAX_SUBTITLE_SIZE=1MB
```

### File Sanitization & Virus Scanning

All uploaded files undergo mandatory ClamAV virus scanning before processing.

```bash
# ClamAV Connection
CLAMAV_ADDRESS=clamav:3310         # ClamAV daemon address (host:port)
CLAMAV_TIMEOUT=300                  # Scan timeout in seconds (5min default)
CLAMAV_MAX_RETRIES=3                # Connection retry attempts
CLAMAV_RETRY_DELAY=1                # Delay between retries (seconds)

# Fallback Mode (Critical Security Setting)
CLAMAV_FALLBACK_MODE=strict         # strict|warn|allow
  # PRODUCTION MUST USE: strict
  # strict: Reject uploads if ClamAV unavailable (RECOMMENDED)
  # warn: Log warning but allow (DEV/TEST ONLY - security risk)
  # allow: Silently bypass scanning (NEVER USE - critical vulnerability)

# Quarantine Settings
QUARANTINE_DIR=/var/quarantine      # Isolated directory for infected files
CLAMAV_AUTO_QUARANTINE=true         # Auto-quarantine infected files
QUARANTINE_RETENTION_DAYS=30        # Days to keep quarantined files (forensics)
CLAMAV_AUDIT_LOG=/var/log/athena/virus_scan.log  # Audit trail
```

#### ClamAV Deployment (Docker)

```yaml
# docker-compose.yml
services:
  clamav:
    image: clamav/clamav:latest
    container_name: athena-clamav
    restart: unless-stopped
    volumes:
      - clamav-signatures:/var/lib/clamav  # Virus signature database
      - ./quarantine:/quarantine:ro        # Read-only quarantine mount
    environment:
      - CLAMAV_NO_FRESHCLAM=false          # Enable auto-updates
    healthcheck:
      test: ["CMD", "clamdscan", "--ping", "1"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 120s  # Allow time for initial signature download
    deploy:
      resources:
        limits:
          memory: 2G      # ClamAV requires 1.5-2GB RAM
        reservations:
          memory: 1G
    networks:
      - athena-backend  # Isolated network

  app:
    depends_on:
      clamav:
        condition: service_healthy
    environment:
      - CLAMAV_ADDRESS=clamav:3310
      - CLAMAV_FALLBACK_MODE=strict
```

#### Virus Scanner Monitoring

```bash
# Check ClamAV health
docker exec athena-clamav clamdscan --ping

# Monitor scan logs
tail -f /var/log/athena/virus_scan.log

# Database query for scan statistics
psql -U athena_user -d athena -c "
  SELECT
    scan_result,
    COUNT(*) as total,
    COUNT(DISTINCT user_id) as unique_users,
    AVG(scan_duration_ms) as avg_duration_ms
  FROM virus_scan_log
  WHERE scanned_at > NOW() - INTERVAL '24 hours'
  GROUP BY scan_result
  ORDER BY total DESC;
"

# Check for quarantined files
ls -lh /var/quarantine/

# Alert on high failure rate (Prometheus)
# virus_scan_failures_total / virus_scan_total > 0.1
```

#### Security Considerations

**Critical**: Always use `CLAMAV_FALLBACK_MODE=strict` in production to prevent infected file uploads during scanner outages.

**CVE-ATHENA-2025-001 Mitigation**:
- Ensure ClamAV version is up-to-date
- Monitor scanner availability with health checks
- Set up alerts for scan failures or fallback mode activations
- Review audit logs regularly for suspicious patterns
- Test disaster recovery procedures (ClamAV failover)

See [SECURITY.md](../../SECURITY.md) for detailed vulnerability disclosure and remediation steps.

### Upload Restrictions

```go
// Block dangerous extensions
var blockedExtensions = []string{
    ".exe", ".dll", ".scr", ".bat", ".cmd", ".com",
    ".pif", ".application", ".gadget", ".msi", ".jar",
    ".vb", ".vbs", ".ws", ".wsf", ".wsc", ".wsh",
    ".ps1", ".ps1xml", ".ps2", ".ps2xml", ".psc1", ".psc2",
}
```

## Network Security

### Firewall Rules

```bash
# UFW configuration
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow from 10.0.0.0/8 to any port 5432  # PostgreSQL from private network
ufw allow from 10.0.0.0/8 to any port 6379  # Redis from private network
ufw enable
```

### TLS/SSL Configuration

```nginx
# Strong cipher configuration
ssl_protocols TLSv1.2 TLSv1.3;
ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
ssl_prefer_server_ciphers off;
ssl_session_cache shared:SSL:10m;
ssl_session_timeout 1d;
ssl_session_tickets off;
ssl_stapling on;
ssl_stapling_verify on;
```

## Logging & Auditing

### Security Logging

```bash
# Audit logging
ENABLE_AUDIT_LOG=true
AUDIT_LOG_FILE=/var/log/athena/audit.log
AUDIT_LOG_LEVEL=info

# Events to log
LOG_LOGIN_ATTEMPTS=true
LOG_FAILED_AUTH=true
LOG_PRIVILEGE_CHANGES=true
LOG_DATA_ACCESS=true
LOG_ADMIN_ACTIONS=true
```

### Log Sanitization

```go
// Remove sensitive data from logs
func sanitizeLog(message string) string {
    // Remove passwords
    message = regexp.MustCompile(`password["\s:=]+[^\s"]+`).ReplaceAllString(message, "password=***")
    // Remove tokens
    message = regexp.MustCompile(`token["\s:=]+[^\s"]+`).ReplaceAllString(message, "token=***")
    // Remove API keys
    message = regexp.MustCompile(`api[_-]?key["\s:=]+[^\s"]+`).ReplaceAllString(message, "api_key=***")
    return message
}
```

## Vulnerability Management

### Dependency Scanning

```bash
# Go vulnerability check
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# Docker image scanning
docker scan athena:latest

# Trivy scanning
trivy image athena:latest
```

### Security Updates

```bash
# Update Go modules
go get -u ./...
go mod tidy

# Update base images
docker pull alpine:latest
docker build --pull -t athena:latest .

# System updates
apt update && apt upgrade -y
```

## Incident Response

### Security Monitoring

```bash
# Failed login attempts
grep "authentication failed" /var/log/athena/app.log | tail -20

# Suspicious API usage
grep "rate limit exceeded" /var/log/athena/app.log | tail -20

# Database intrusion attempts
grep "SQL injection" /var/log/athena/security.log
```

### Emergency Procedures

```bash
# Block suspicious IP
ufw deny from 192.168.1.100

# Rotate compromised secrets
./scripts/rotate-secrets.sh

# Enable emergency mode (read-only)
redis-cli set emergency_mode true

# Disable user account
psql -c "UPDATE users SET is_active = false WHERE email = 'compromised@example.com'"
```

## Security Checklist

### Pre-Deployment

- [ ] All secrets generated with sufficient entropy
- [ ] Environment variables properly configured
- [ ] Database users have minimal required privileges
- [ ] TLS/SSL certificates valid and properly configured
- [ ] Firewall rules configured and enabled
- [ ] Security headers enabled
- [ ] Rate limiting configured
- [ ] Input validation implemented
- [ ] File upload restrictions in place

### Post-Deployment

- [ ] Security monitoring enabled
- [ ] Log aggregation configured
- [ ] Backup encryption enabled
- [ ] Vulnerability scanning scheduled
- [ ] Security updates automated
- [ ] Incident response plan documented
- [ ] Security audit performed
- [ ] Penetration testing completed

### Ongoing

- [ ] Regular security updates
- [ ] Dependency vulnerability scanning
- [ ] Log review and analysis
- [ ] Security training for team
- [ ] Regular security audits
- [ ] Incident response drills
- [ ] Documentation updates

## Compliance

### GDPR Compliance

```bash
# Data privacy settings
ENABLE_GDPR_COMPLIANCE=true
DATA_RETENTION_DAYS=365
ENABLE_RIGHT_TO_ERASURE=true
ENABLE_DATA_PORTABILITY=true
```

### Data Encryption

```bash
# Encryption at rest
ENABLE_ENCRYPTION_AT_REST=true
ENCRYPTION_KEY=<32-byte-hex-key>

# Field-level encryption
ENCRYPT_PII_FIELDS=true
PII_ENCRYPTION_KEY=<separate-32-byte-key>
```

## Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Go Security Guidelines](https://golang.org/doc/security)
- [PostgreSQL Security](https://www.postgresql.org/docs/current/security.html)
- [Docker Security Best Practices](https://docs.docker.com/engine/security/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
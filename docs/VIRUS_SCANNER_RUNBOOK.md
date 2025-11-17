# Virus Scanner Operational Runbook

## Overview

This runbook provides operational procedures for monitoring, maintaining, and troubleshooting the ClamAV virus scanning system in Athena.

## Table of Contents

1. [Health Monitoring](#health-monitoring)
2. [Common Issues](#common-issues)
3. [Incident Response](#incident-response)
4. [Maintenance Procedures](#maintenance-procedures)
5. [Performance Tuning](#performance-tuning)
6. [Security Hardening](#security-hardening)

---

## Health Monitoring

### Check ClamAV Service Status

```bash
# Docker environment
docker ps | grep clamav
docker exec athena-clamav clamdscan --ping

# Systemd environment
systemctl status clamav-daemon

# Check daemon responsiveness
echo "PING" | nc localhost 3310
# Expected response: PONG
```

### Monitor Scan Performance

```sql
-- Scan statistics (last 24 hours)
SELECT
  scan_result,
  COUNT(*) as total_scans,
  AVG(scan_duration_ms) as avg_duration_ms,
  MAX(scan_duration_ms) as max_duration_ms,
  COUNT(DISTINCT user_id) as unique_users
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '24 hours'
GROUP BY scan_result
ORDER BY total_scans DESC;

-- Scan failure rate (should be < 1%)
SELECT
  ROUND(100.0 * SUM(CASE WHEN scan_result IN ('error', 'warning') THEN 1 ELSE 0 END) / COUNT(*), 2) as failure_rate_pct
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '1 hour';

-- Infected file detections (alert if > 0)
SELECT
  scanned_at,
  user_id,
  file_path,
  virus_name,
  quarantined,
  quarantine_path
FROM virus_scan_log
WHERE scan_result = 'infected'
  AND scanned_at > NOW() - INTERVAL '7 days'
ORDER BY scanned_at DESC;
```

### Check Quarantine Directory

```bash
# List quarantined files
ls -lh /var/quarantine/

# Disk usage
du -sh /var/quarantine/

# Count of files
find /var/quarantine -type f | wc -l

# Files by age
find /var/quarantine -type f -mtime +30 -ls  # Older than 30 days
```

### ClamAV Signature Updates

```bash
# Check signature database version
docker exec athena-clamav freshclam --version

# View last update time
docker exec athena-clamav cat /var/lib/clamav/daily.cvd | head -c 512

# Force signature update
docker exec athena-clamav freshclam

# Check update logs
docker logs athena-clamav | grep freshclam
```

---

## Common Issues

### Issue: ClamAV Service Unavailable

**Symptoms**: HTTP 503 errors on uploads, "ClamAV unavailable" in logs

**Diagnosis**:
```bash
# Check if ClamAV container is running
docker ps | grep clamav

# Check ClamAV logs
docker logs athena-clamav --tail 100

# Test connectivity
telnet localhost 3310
```

**Resolution**:
```bash
# Restart ClamAV
docker restart athena-clamav

# Wait for initialization (can take 60-120 seconds)
docker logs -f athena-clamav

# Verify service is ready
docker exec athena-clamav clamdscan --ping
```

**Prevention**:
- Set up health checks in docker-compose.yml
- Monitor ClamAV memory usage (needs 1.5-2GB RAM)
- Enable auto-restart: `restart: unless-stopped`

---

### Issue: Scan Timeouts

**Symptoms**: "scan timeout" errors, slow upload processing

**Diagnosis**:
```sql
-- Check for slow scans
SELECT
  file_path,
  file_size,
  scan_duration_ms,
  scan_duration_ms / (file_size / 1024.0 / 1024.0) as ms_per_mb
FROM virus_scan_log
WHERE scan_duration_ms > 300000  -- > 5 minutes
  AND scanned_at > NOW() - INTERVAL '24 hours'
ORDER BY scan_duration_ms DESC
LIMIT 20;
```

**Resolution**:
```bash
# Increase timeout for large files
export CLAMAV_TIMEOUT=600  # 10 minutes

# Check ClamAV CPU/memory usage
docker stats athena-clamav

# Increase ClamAV resources if needed
# Edit docker-compose.yml:
# resources:
#   limits:
#     memory: 3G
#     cpus: '2.0'
```

---

### Issue: High False Positive Rate

**Symptoms**: Legitimate files quarantined, user complaints

**Diagnosis**:
```sql
-- Review recent quarantines
SELECT
  virus_name,
  COUNT(*) as occurrences,
  STRING_AGG(DISTINCT file_path, ', ') as sample_files
FROM virus_scan_log
WHERE scan_result = 'infected'
  AND scanned_at > NOW() - INTERVAL '7 days'
GROUP BY virus_name
ORDER BY occurrences DESC;
```

**Resolution**:
```bash
# Test file with multiple engines
docker exec athena-clamav clamdscan /path/to/file

# Submit false positive to ClamAV team
# https://www.clamav.net/reports/fp

# Temporary workaround: whitelist specific signature
# Edit /etc/clamav/clamd.conf
# ExcludePath ^/path/to/false/positive
```

---

### Issue: Memory Exhaustion

**Symptoms**: ClamAV OOM kills, scan failures

**Diagnosis**:
```bash
# Check memory usage
docker stats athena-clamav --no-stream

# Review OOM kills in system logs
dmesg | grep -i "out of memory"
journalctl -u docker | grep clamav | grep -i oom
```

**Resolution**:
```yaml
# docker-compose.yml - Increase memory limits
services:
  clamav:
    deploy:
      resources:
        limits:
          memory: 3G
        reservations:
          memory: 2G

# Also configure ClamAV's own limits
# /etc/clamav/clamd.conf
# MaxScanSize 500M
# MaxFileSize 100M
# StreamMaxLength 100M
```

---

## Incident Response

### Malware Detection Incident

**When**: Infected file detected (scan_result = 'infected')

**Immediate Actions**:

1. **Verify Detection**:
```bash
# Review scan log entry
docker exec athena-clamav clamdscan /quarantine/[filename]

# Check virus signature details
docker exec athena-clamav sigtool --find [virus_name]
```

2. **Identify Affected User**:
```sql
SELECT
  u.username,
  u.email,
  vsl.file_path,
  vsl.virus_name,
  vsl.scanned_at
FROM virus_scan_log vsl
JOIN users u ON u.id = vsl.user_id
WHERE vsl.id = '[scan_log_id]';
```

3. **Review User Activity**:
```sql
-- Check for other uploads from same user
SELECT * FROM virus_scan_log
WHERE user_id = '[user_id]'
  AND scanned_at > NOW() - INTERVAL '30 days'
ORDER BY scanned_at DESC;

-- Check user's other videos
SELECT id, title, created_at, processing_status
FROM videos
WHERE user_id = '[user_id]'
ORDER BY created_at DESC;
```

4. **Quarantine Verification**:
```bash
# Ensure file is quarantined and secured
ls -l /var/quarantine/[filename]
# Should have -r-------- permissions (400)

# Verify file cannot be accessed
stat /var/quarantine/[filename]
```

5. **Notify Security Team**:
```bash
# Send alert
curl -X POST https://alerts.company.com/security \
  -H "Content-Type: application/json" \
  -d '{
    "severity": "HIGH",
    "type": "malware_detected",
    "virus_name": "[virus_name]",
    "user_id": "[user_id]",
    "file_path": "[original_path]",
    "quarantine_path": "[quarantine_path]"
  }'
```

**Follow-up Actions**:

- Review user account for suspicious activity
- Consider temporary account suspension if repeated violations
- Analyze malware sample (in isolated sandbox)
- Update security rules if novel attack vector identified
- Document incident in security log

---

### Scanner Outage Incident

**When**: ClamAV unavailable for > 5 minutes

**Immediate Actions**:

1. **Check Fallback Mode**:
```bash
# Verify production uses strict mode
docker exec athena-app env | grep CLAMAV_FALLBACK_MODE
# MUST return: CLAMAV_FALLBACK_MODE=strict

# If not strict, update immediately
docker exec athena-app sh -c 'export CLAMAV_FALLBACK_MODE=strict'
```

2. **Assess Impact**:
```sql
-- Count uploads during outage
SELECT COUNT(*) as affected_uploads
FROM virus_scan_log
WHERE scan_result IN ('error', 'warning')
  AND scanned_at BETWEEN '[outage_start]' AND '[outage_end]';
```

3. **Restore Service**:
```bash
# Attempt restart
docker restart athena-clamav

# If restart fails, check resources
df -h  # Disk space
free -h  # Memory

# Force clean restart
docker stop athena-clamav
docker rm athena-clamav
docker compose up -d clamav
```

4. **Re-scan Affected Files** (if fallback mode allowed uploads):
```bash
# Script to re-scan files from outage window
#!/bin/bash
psql -U athena_user -d athena -t -A -c "
  SELECT DISTINCT file_path
  FROM virus_scan_log
  WHERE scan_result = 'warning'
    AND scanned_at BETWEEN '[outage_start]' AND '[outage_end]'
" | while read filepath; do
  echo "Re-scanning: $filepath"
  docker exec athena-clamav clamdscan "$filepath"
done
```

---

## Maintenance Procedures

### Daily Tasks

1. **Review Scan Logs**:
```sql
-- Daily scan summary
SELECT
  DATE(scanned_at) as date,
  scan_result,
  COUNT(*) as count
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '1 day'
GROUP BY DATE(scanned_at), scan_result
ORDER BY date DESC, count DESC;
```

2. **Check Quarantine Size**:
```bash
du -sh /var/quarantine/
# Alert if > 10GB
```

### Weekly Tasks

1. **Update Virus Signatures**:
```bash
# Signatures auto-update via freshclam, but verify
docker exec athena-clamav freshclam --show-progress

# Check signature counts
docker exec athena-clamav sigtool --info /var/lib/clamav/daily.cvd
```

2. **Review Scan Performance**:
```sql
-- Weekly scan metrics
SELECT
  DATE_TRUNC('week', scanned_at) as week,
  COUNT(*) as total_scans,
  AVG(scan_duration_ms) as avg_duration_ms,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY scan_duration_ms) as p95_duration_ms
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '4 weeks'
GROUP BY DATE_TRUNC('week', scanned_at)
ORDER BY week DESC;
```

### Monthly Tasks

1. **Quarantine Cleanup**:
```bash
# Run automated cleanup (configured in docker-compose)
docker exec athena-app /app/bin/cleanup-quarantine

# Or manual cleanup
find /var/quarantine -type f -mtime +90 -delete
```

2. **Capacity Planning**:
```sql
-- Monthly scan volume trends
SELECT
  DATE_TRUNC('month', scanned_at) as month,
  COUNT(*) as total_scans,
  SUM(file_size) / (1024.0*1024.0*1024.0) as total_gb_scanned,
  AVG(file_size) / (1024.0*1024.0) as avg_file_size_mb
FROM virus_scan_log
WHERE scanned_at > NOW() - INTERVAL '12 months'
GROUP BY DATE_TRUNC('month', scanned_at)
ORDER BY month DESC;
```

3. **Security Audit**:
```bash
# Review audit log
less /var/log/athena/virus_scan.log

# Check for unauthorized quarantine access
sudo ausearch -f /var/quarantine -i

# Verify file permissions
find /var/quarantine -type f ! -perm 400 -ls
```

---

## Performance Tuning

### Optimize Scan Speed

```conf
# /etc/clamav/clamd.conf
MaxThreads 4                    # Parallel scanning threads
MaxQueue 200                    # Queue depth
MaxConnectionQueueLength 50     # Connection backlog

# Skip certain file types (at your risk)
ExcludePath ^/tmp/thumbnails/   # Already processed thumbnails
```

### Database Indexing

```sql
-- Ensure optimal indexes exist
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_virus_scan_log_user_result
  ON virus_scan_log(user_id, scan_result)
  WHERE scan_result = 'infected';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_virus_scan_log_scanned_at_brin
  ON virus_scan_log USING BRIN(scanned_at);

-- Vacuum and analyze
VACUUM ANALYZE virus_scan_log;
```

### Resource Allocation

```yaml
# docker-compose.yml
services:
  clamav:
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 3G
        reservations:
          cpus: '1.0'
          memory: 2G
    # Pin to specific CPU cores for consistency
    cpuset: "2,3"
```

---

## Security Hardening

### Network Isolation

```yaml
# docker-compose.yml
networks:
  clamav-net:
    internal: true  # No external access

services:
  clamav:
    networks:
      - clamav-net

  app:
    networks:
      - clamav-net
      - public
```

### File System Isolation

```bash
# Mount quarantine as separate filesystem
# /etc/fstab
/dev/mapper/vg0-quarantine /var/quarantine ext4 noexec,nosuid,nodev 0 2

# Remount with restrictions
mount -o remount,noexec,nosuid,nodev /var/quarantine
```

### Audit Logging

```bash
# Enable auditd for quarantine directory
auditctl -w /var/quarantine -p wa -k quarantine_access

# View audit events
ausearch -k quarantine_access -i
```

### Signature Verification

```bash
# Verify signature database integrity
docker exec athena-clamav sigtool --info /var/lib/clamav/daily.cvd
docker exec athena-clamav sigtool --info /var/lib/clamav/main.cvd

# Check for signature tampering
find /var/lib/clamav -type f -name "*.cvd" -exec md5sum {} \;
# Compare with known-good checksums from ClamAV
```

---

## Alerting Rules (Prometheus)

```yaml
groups:
- name: virus_scanner
  interval: 30s
  rules:
  - alert: ClamAVDown
    expr: up{job="clamav"} == 0
    for: 5m
    annotations:
      summary: "ClamAV service unavailable"

  - alert: HighScanFailureRate
    expr: rate(virus_scan_failures_total[5m]) > 0.1
    for: 10m
    annotations:
      summary: "High virus scan failure rate"

  - alert: MalwareDetected
    expr: increase(virus_scan_infected_total[5m]) > 0
    annotations:
      summary: "Malware detected in uploaded file"
      severity: critical

  - alert: SlowScans
    expr: histogram_quantile(0.95, virus_scan_duration_seconds) > 300
    for: 15m
    annotations:
      summary: "95th percentile scan duration > 5 minutes"
```

---

## Emergency Contacts

- **Security Team**: security@company.com
- **On-Call Engineer**: Use PagerDuty rotation
- **ClamAV Vendor Support**: https://www.clamav.net/contact
- **Incident Slack**: #security-incidents

---

## References

- [SECURITY.md](../SECURITY.md) - CVE-ATHENA-2025-001 details
- [Security Deployment Guide](security.md) - Configuration reference
- [ClamAV Documentation](https://docs.clamav.net/)
- [Virus Scanner Implementation](../../internal/security/virus_scanner.go)

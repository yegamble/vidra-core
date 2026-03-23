# Operations Runbook

This runbook provides operational procedures for monitoring, troubleshooting, and maintaining the Vidra Core video platform in production.

For detailed monitoring setup instructions, see the [Monitoring Guide](MONITORING.md).

## Table of Contents

1. [Health Monitoring](#health-monitoring)
2. [Incident Response](#incident-response)
3. [Backup & Restore](#backup--restore)
4. [Scaling Guidelines](#scaling-guidelines)
5. [Common Issues](#common-issues)
6. [Maintenance Procedures](#maintenance-procedures)
7. [Emergency Contacts](#emergency-contacts)

---

## Health Monitoring

### Health Check Endpoints

**Liveness Probe**: `/health`

- **Purpose**: Verify service is responsive
- **Expected Response**: 200 OK
- **Timeout**: 5 seconds
- **Action on Failure**: Restart container/pod

**Readiness Probe**: `/ready`

- **Purpose**: Verify service can handle traffic
- **Expected Response**: 200 OK with dependency status
- **Timeout**: 10 seconds
- **Action on Failure**: Remove from load balancer

**Example Response**:

```json
{
  "status": "healthy",
  "checks": {
    "database": "healthy",
    "redis": "healthy",
    "ipfs": "healthy",
    "clamav": "healthy"
  },
  "queue_depth": 15,
  "uptime_seconds": 86400
}
```

### Monitoring Dashboards

**Key Metrics to Monitor**:

| Metric | Normal Range | Warning Threshold | Critical Threshold |
|--------|--------------|-------------------|-------------------|
| **HTTP Request Latency (p95)** | < 500ms | > 1s | > 3s |
| **Error Rate** | < 0.1% | > 1% | > 5% |
| **Database Connections** | < 15/25 | > 20/25 | > 24/25 |
| **Redis Memory** | < 70% | > 80% | > 90% |
| **IPFS Disk Usage** | < 80% | > 85% | > 90% |
| **Queue Depth** | < 100 | > 500 | > 1000 |
| **Transcode Workers** | 80-100% busy | All idle | All blocked |
| **ClamAV Availability** | 100% | < 99% | < 95% |

**Prometheus Queries**:

```promql
# HTTP request rate
rate(http_requests_total[5m])

# Error rate
rate(http_requests_total{status=~"5.."}[5m])
  / rate(http_requests_total[5m])

# Database connection pool usage
pg_connection_pool_active / pg_connection_pool_max

# Queue depth
video_processing_queue_depth

# Transcode success rate
rate(video_transcode_success_total[5m])
  / rate(video_transcode_total[5m])
```

### Log Aggregation

**Log Levels**:

- **DEBUG**: Development only
- **INFO**: Normal operations
- **WARN**: Potential issues (fallback modes, retries)
- **ERROR**: Failed operations (needs attention)
- **FATAL**: Service cannot continue

**Critical Log Patterns**:

```bash
# Database connection failures
level=error msg="database connection failed"

# Redis failures
level=error msg="redis: connection refused"

# IPFS unreachable
level=error msg="ipfs api unreachable"

# ClamAV unavailable (strict mode)
level=error msg="virus scanner unavailable" mode=strict

# Authentication failures spike
level=warn msg="authentication failed"
```

---

## Incident Response

### Severity Levels

**P0 - Critical** (Response: Immediate, Notification: All)

- Service completely down
- Data loss occurring
- Security breach active

**P1 - High** (Response: 15 minutes, Notification: On-call)

- Partial service outage
- Major feature unavailable
- High error rate (> 5%)

**P2 - Medium** (Response: 2 hours, Notification: Team)

- Degraded performance
- Single component failure with fallback
- Non-critical feature broken

**P3 - Low** (Response: Next business day, Notification: Slack)

- Cosmetic issues
- Minor performance degradation
- Low-impact bugs

### Incident Response Workflow

1. **Detect**
   - Monitoring alert triggered
   - User report received
   - Health check failure

2. **Assess**
   - Check dashboards
   - Review logs
   - Determine severity
   - Identify affected components

3. **Communicate**
   - Create incident channel
   - Notify stakeholders
   - Post status page update

4. **Mitigate**
   - Apply immediate workaround
   - Rollback if recent deploy
   - Scale resources if capacity issue
   - Enable degraded mode if needed

5. **Resolve**
   - Implement permanent fix
   - Verify resolution
   - Update status page
   - Close incident

6. **Post-Mortem**
   - Document timeline
   - Identify root cause
   - List action items
   - Schedule follow-up

### Common Incident Scenarios

#### Scenario 1: Database Connection Pool Exhausted

**Symptoms**:

- `503 Service Unavailable` errors
- `database connection pool exhausted` logs
- High request latency

**Diagnosis**:

```sql
-- Check active connections
SELECT count(*) FROM pg_stat_activity WHERE state = 'active';

-- Identify long-running queries
SELECT pid, now() - query_start AS duration, query
FROM pg_stat_activity
WHERE state = 'active' AND query_start < now() - interval '5 minutes'
ORDER BY duration DESC;
```

**Mitigation**:

1. Increase connection pool size (emergency)
2. Kill long-running queries
3. Restart application (if severe)

**Resolution**:

1. Optimize slow queries
2. Review connection leak in code
3. Tune pool settings
4. Add connection timeout

#### Scenario 2: IPFS Gateway Degradation

**Symptoms**:

- Slow video loading
- Timeout errors on `/videos/{id}/stream`
- `ipfs gateway timeout` logs

**Diagnosis**:

```bash
# Check IPFS daemon health
curl http://localhost:5001/api/v0/id

# Check gateway response time
time curl -I https://ipfs.io/ipfs/{CID}

# Check cluster peers
curl http://localhost:9094/peers
```

**Mitigation**:

1. Enable fallback to local delivery
2. Add temporary gateway to pool
3. Reduce IPFS timeout (fail faster)

**Resolution**:

1. Restart IPFS daemon
2. Clear gateway cache
3. Verify network connectivity
4. Re-pin critical content

#### Scenario 3: ClamAV Scanner Unavailable

**Symptoms**:

- Upload failures (if `CLAMAV_FALLBACK_MODE=strict`)
- `virus scanner unavailable` warnings
- Quarantine directory not updating

**Diagnosis**:

```bash
# Check ClamAV daemon
systemctl status clamav-daemon

# Test socket
echo "PING" | nc localhost 3310

# Check signature updates
freshclam --show-progress
```

**Mitigation**:

1. **Strict Mode**: Switch to `warn` temporarily (security review required)
2. Restart ClamAV daemon
3. Verify signature database

**Resolution**:

1. Fix ClamAV configuration
2. Allocate more memory (if OOM)
3. Update signatures
4. Return to `strict` mode
5. Scan backlog manually

#### Scenario 4: Queue Backup

**Symptoms**:

- High queue depth (> 1000)
- Videos stuck in processing
- Slow transcoding

**Diagnosis**:

```bash
# Check queue depth
curl http://localhost:8080/metrics | grep queue_depth

# Check worker status
curl http://localhost:8080/api/v1/admin/workers

# Check disk space
df -h /app/processed
```

**Mitigation**:

1. Scale up transcode workers
2. Pause non-critical jobs
3. Reject new uploads temporarily

**Resolution**:

1. Identify stuck jobs
2. Clear failed jobs
3. Optimize worker efficiency
4. Add capacity planning

---

## Backup & Restore

### Backup Schedule

**Database (PostgreSQL)**:

- **Full Backup**: Daily at 02:00 UTC
- **Incremental**: Every 6 hours
- **WAL Archiving**: Continuous
- **Retention**: 30 days

**Redis**:

- **RDB Snapshot**: Every 15 minutes
- **AOF**: Continuous
- **Retention**: 7 days

**IPFS**:

- **Pinset Export**: Daily
- **Retention**: 30 days
- **External Pins**: Pinata, Infura

**Application Data**:

- **Uploads**: Synced to S3 hourly
- **Processed Videos**: Synced to S3 daily
- **Retention**: 90 days

### Database Backup

**Manual Backup**:

```bash
# Full database dump
pg_dump -h localhost -U vidra_user -d vidra \
  -F c -b -v -f backup_$(date +%Y%m%d_%H%M%S).dump

# Compressed backup
pg_dump -h localhost -U vidra_user -d vidra \
  | gzip > backup_$(date +%Y%m%d_%H%M%S).sql.gz

# Schema-only backup
pg_dump -h localhost -U vidra_user -d vidra \
  --schema-only > schema_$(date +%Y%m%d).sql
```

**Automated Backup** (via cron):

```bash
# /etc/cron.d/vidra-backup
0 2 * * * postgres /usr/local/bin/backup-vidra-db.sh
```

**Verify Backup**:

```bash
# Test restore to temporary database
createdb test_restore
pg_restore -d test_restore backup_20250117.dump
psql test_restore -c "SELECT count(*) FROM videos;"
dropdb test_restore
```

### Database Restore

**Full Restore**:

```bash
# Stop application
systemctl stop vidra

# Drop existing database (CAUTION!)
dropdb vidra

# Recreate database
createdb vidra

# Restore from dump
pg_restore -d vidra backup_20250117.dump

# Verify migrations
goose -dir migrations postgres "$DATABASE_URL" status

# Start application
systemctl start vidra
```

**Point-in-Time Recovery** (PITR):

```bash
# Stop PostgreSQL
systemctl stop postgresql

# Restore base backup
tar -xzf base_backup.tar.gz -C /var/lib/postgresql/14/main

# Configure recovery
cat > /var/lib/postgresql/14/main/recovery.conf <<EOF
restore_command = 'cp /mnt/wal_archive/%f %p'
recovery_target_time = '2025-01-17 12:00:00'
EOF

# Start PostgreSQL (enters recovery mode)
systemctl start postgresql

# Monitor recovery
tail -f /var/log/postgresql/postgresql-14-main.log

# Promote when ready
sudo -u postgres psql -c "SELECT pg_wal_replay_resume();"
```

### IPFS Backup & Restore

**Export Pinset**:

```bash
# List all pins
ipfs pin ls --type=recursive > pinset_$(date +%Y%m%d).txt

# Export CIDs with metadata
ipfs pin ls --type=recursive | while read cid type; do
  echo "$cid,$(ipfs files stat --hash $cid 2>/dev/null || echo 'unknown')"
done > pinset_metadata_$(date +%Y%m%d).csv
```

**Restore Pinset**:

```bash
# Pin all CIDs from export
cat pinset_20250117.txt | awk '{print $1}' | while read cid; do
  ipfs pin add "$cid" --progress || echo "FAILED: $cid"
done

# Verify pins
ipfs pin ls --type=recursive | wc -l
```

**External Pinning** (Pinata):

```bash
# Pin to Pinata
curl -X POST "https://api.pinata.cloud/pinning/pinByHash" \
  -H "pinata_api_key: $PINATA_API_KEY" \
  -H "pinata_secret_api_key: $PINATA_SECRET_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"hashToPin\": \"$CID\"}"
```

---

## Scaling Guidelines

### Horizontal Scaling

**Application Servers**:

- **Current**: 2 instances
- **Scale Up When**: CPU > 70% or Request latency > 1s
- **Scale Down When**: CPU < 30% for 15 minutes
- **Max Instances**: 10
- **Min Instances**: 2

**Transcode Workers**:

- **Current**: 4 workers
- **Scale Up When**: Queue depth > 100 or CPU idle
- **Scale Down When**: Queue depth < 10
- **Max Workers**: 20
- **Min Workers**: 2

**IPFS Cluster**:

- **Current**: 3 peers
- **Scale Up When**: Storage > 85% or throughput degraded
- **Recommended**: Odd number of peers (3, 5, 7)
- **Max Peers**: 7

### Vertical Scaling

**Database**:

- **Current**: 4 vCPU, 8GB RAM
- **Indicators**: High CPU, slow queries, connection waits
- **Upgrade Path**: 8 vCPU, 16GB RAM → 16 vCPU, 32GB RAM

**Redis**:

- **Current**: 2GB RAM
- **Indicators**: Memory > 90%, evictions occurring
- **Upgrade Path**: 4GB → 8GB → 16GB

**Transcode Workers**:

- **Current**: 4 vCPU, 8GB RAM each
- **Indicators**: Transcoding time increasing
- **Upgrade Path**: 8 vCPU, 16GB RAM

### Kubernetes Scaling

**Horizontal Pod Autoscaler** (HPA):

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: vidra-api
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: vidra-api
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Pods
    pods:
      metric:
        name: http_request_duration_seconds
      target:
        type: AverageValue
        averageValue: "1"
```

**Vertical Pod Autoscaler** (VPA):

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: vidra-transcode-worker
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: transcode-worker
  updatePolicy:
    updateMode: "Auto"
  resourcePolicy:
    containerPolicies:
    - containerName: worker
      minAllowed:
        cpu: 2
        memory: 4Gi
      maxAllowed:
        cpu: 8
        memory: 16Gi
```

---

## Common Issues

### Issue: Videos Stuck in "Processing"

**Symptoms**:

- Videos remain in `processing` status indefinitely
- Transcode workers idle
- No error logs

**Diagnosis**:

```bash
# Check stuck videos (use docker exec or psql with DATABASE_URL)
psql -U vidra_user -d vidra -c "SELECT id, title, processing_status, created_at
  FROM videos WHERE processing_status = 'processing'
  AND created_at < now() - interval '1 hour';"

# Check Redis job status
redis-cli HGETALL video:processing:{video_id}

# Check worker logs
docker logs vidra-transcode-worker-1 --tail 100
```

**Resolution**:

```bash
# Reset stuck video
psql -U vidra_user -d vidra -c "UPDATE videos
  SET processing_status = 'pending'
  WHERE id = '{video_id}';"

# Retry processing
curl -X POST http://localhost:8080/api/v1/admin/videos/{video_id}/reprocess
```

### Issue: High Memory Usage

**Symptoms**:

- OOM kills
- Slow performance
- Swap usage increasing

**Diagnosis**:

```bash
# Check memory usage
free -h

# Top memory consumers
ps aux --sort=-%mem | head -10

# Check for memory leaks
pprof -http=:8081 http://localhost:8080/debug/pprof/heap
```

**Resolution**:

```bash
# Restart service
systemctl restart vidra

# Tune GOGC (garbage collection)
export GOGC=50  # More aggressive GC

# Add memory limits
docker run --memory=4g vidra
```

### Issue: Slow Video Playback

**Symptoms**:

- Buffering during playback
- High HLS segment load time
- CDN cache misses

**Diagnosis**:

```bash
# Test HLS delivery
time curl -o /dev/null http://localhost:8080/videos/{id}/master.m3u8

# Check IPFS gateway speed
time ipfs cat {CID} > /dev/null

# Check CDN cache hit ratio
curl -I http://cdn.example.com/videos/{id}/playlist.m3u8 | grep X-Cache
```

**Resolution**:

1. Verify CDN configuration
2. Enable local caching
3. Add IPFS gateway to pool
4. Pre-warm CDN cache

---

## Maintenance Procedures

### Database Maintenance

**Vacuum & Analyze** (Weekly):

```bash
# Vacuum all tables
psql -U vidra_user -d vidra -c "VACUUM ANALYZE;"

# Vacuum specific table
psql -U vidra_user -d vidra -c "VACUUM ANALYZE videos;"

# Check bloat
psql -U vidra_user -d vidra -c "SELECT schemaname, tablename,
  pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size
  FROM pg_tables ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC LIMIT 10;"
```

**Reindex** (Monthly):

```bash
# Reindex all indexes
psql -U vidra_user -d vidra -c "REINDEX DATABASE vidra;"

# Reindex specific index
psql -U vidra_user -d vidra -c "REINDEX INDEX idx_videos_processing_status;"
```

### Redis Maintenance

**Clear Expired Keys**:

```bash
# Scan for expired sessions
redis-cli --scan --pattern "session:*" | while read key; do
  redis-cli TTL "$key"
done

# Force eviction (if needed)
redis-cli CONFIG SET maxmemory-policy allkeys-lru
```

### IPFS Maintenance

**Garbage Collection** (Weekly):

```bash
# Run GC
ipfs repo gc

# Verify repo size
ipfs repo stat

# Check pinset size
ipfs pin ls --type=recursive | wc -l
```

**Cluster Health Check**:

```bash
# Check cluster peers
ipfs-cluster-ctl peers ls

# Verify pins
ipfs-cluster-ctl pin ls

# Recover failed pins
ipfs-cluster-ctl pin recover --all
```

### ClamAV Maintenance

**Update Signatures** (Daily):

```bash
# Update virus definitions
freshclam

# Verify signature count
clamscan --version

# Test scanner
echo "X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!\$H+H*" > eicar.txt
clamscan eicar.txt
```

---

## Emergency Contacts

### On-Call Rotation

| Role | Primary | Secondary |
|------|---------|-----------|
| **Backend** | +1-555-0100 | +1-555-0101 |
| **DevOps** | +1-555-0200 | +1-555-0201 |
| **Security** | +1-555-0300 | <security@vidra.com> |
| **Product** | +1-555-0400 | <product@vidra.com> |

### Escalation Path

1. **L1 - On-Call Engineer** (Response: Immediate)
2. **L2 - Team Lead** (Escalate if: > 30 min unresolved)
3. **L3 - Engineering Manager** (Escalate if: > 1 hour or P0)
4. **L4 - CTO** (Escalate if: Data loss or security breach)

### External Contacts

- **AWS Support**: +1-800-AWS-SUPPORT (P1 support plan)
- **Cloudflare Support**: Enterprise dashboard
- **Database DBA**: <dba@vidra.com>
- **Security Team**: <security@vidra.com> (PGP key available)

---

## Appendix

### Useful Commands

```bash
# Check service status
systemctl status vidra

# View logs (last 100 lines)
journalctl -u vidra -n 100 --no-pager

# Follow logs in real-time
journalctl -u vidra -f

# Check disk usage
du -sh /app/uploads /app/processed /app/ipfs

# Database connection count
psql -U vidra_user -d vidra -c "SELECT count(*) FROM pg_stat_activity;"

# Redis memory usage
redis-cli INFO memory | grep used_memory_human

# IPFS peer count
ipfs swarm peers | wc -l

# ClamAV daemon status
clamdscan --version
```

### Related Documentation

- [Deployment Guide](README.md)
- [Security Documentation](../security/)
- [Architecture Overview](../architecture/)
- [Virus Scanner Runbook](../VIRUS_SCANNER_RUNBOOK.md)

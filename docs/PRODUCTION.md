# Production Deployment Guide

This guide covers deploying Athena to production environments with security, monitoring, and scalability considerations.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Security Considerations](#security-considerations)
3. [Environment Setup](#environment-setup)
4. [Deployment](#deployment)
5. [Monitoring & Observability](#monitoring--observability)
6. [Backup & Recovery](#backup--recovery)
7. [Scaling](#scaling)
8. [Troubleshooting](#troubleshooting)
9. [Maintenance](#maintenance)

## Prerequisites

### System Requirements

- **CPU**: 4+ cores (8+ recommended for high traffic)
- **RAM**: 8GB minimum (16GB+ recommended)
- **Storage**: 100GB+ SSD (1TB+ for video storage)
- **Network**: 100Mbps+ bandwidth
- **OS**: Ubuntu 20.04+ or CentOS 8+

### Software Requirements

- Docker 20.10+
- Docker Compose 2.0+
- PostgreSQL 15+
- Redis 7+
- FFmpeg 4.0+
- Node.js 18+ (for monitoring tools)

### Network Requirements

- Port 80/443 (HTTP/HTTPS)
- Port 5432 (PostgreSQL - internal)
- Port 6379 (Redis - internal)
- Port 5001 (IPFS API - internal)

## Security Considerations

### Environment Variables

**Critical Security Variables:**
```bash
# Generate strong secrets
JWT_SECRET=$(openssl rand -hex 64)
HLS_SIGNING_SECRET=$(openssl rand -hex 32)
REDIS_PASSWORD=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 32)
```

**Production Security Checklist:**
- [ ] Change all default passwords
- [ ] Use strong JWT secrets (64+ characters)
- [ ] Enable SSL/TLS for all external connections
- [ ] Configure firewall rules
- [ ] Set up rate limiting
- [ ] Enable audit logging
- [ ] Regular security updates

### Network Security

**Firewall Configuration:**
```bash
# Allow only necessary ports
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP
ufw allow 443/tcp   # HTTPS
ufw enable
```

**SSL/TLS Configuration:**
```bash
# Generate SSL certificate (Let's Encrypt)
sudo certbot certonly --standalone -d yourdomain.com

# Configure Nginx SSL
cp nginx/ssl/cert.pem /etc/letsencrypt/live/yourdomain.com/fullchain.pem
cp nginx/ssl/key.pem /etc/letsencrypt/live/yourdomain.com/privkey.pem
```

### Container Security

**Docker Security Best Practices:**
- Run containers as non-root users
- Use specific image tags (not `latest`)
- Regular security scans
- Resource limits
- Read-only filesystems where possible

## Environment Setup

### 1. Clone and Configure

```bash
# Clone repository
git clone https://github.com/yegamble/athena.git
cd athena

# Copy and configure environment
cp .env.example .env
nano .env
```

### 2. Production Environment Variables

```bash
# Server Configuration
PORT=8080
LOG_LEVEL=info
LOG_FORMAT=json

# Database Configuration
DATABASE_URL=postgres://athena_user:strong_password@localhost:5432/athena?sslmode=require

# Redis Configuration
REDIS_URL=redis://:strong_redis_password@localhost:6379/0

# Security
JWT_SECRET=your_64_character_jwt_secret_here
HLS_SIGNING_SECRET=your_32_character_hls_secret_here

# Rate Limiting (Production)
RATE_LIMIT_REQUESTS=50
RATE_LIMIT_WINDOW=60

# Upload Limits
MAX_UPLOAD_SIZE=5368709120  # 5GB
MAX_PROCESSING_WORKERS=2

# Monitoring
ENABLE_ENCODING_SCHEDULER=true
```

### 3. Database Setup

```bash
# Create production database
sudo -u postgres createdb athena_prod
sudo -u postgres createuser athena_user
sudo -u postgres psql -c "ALTER USER athena_user WITH PASSWORD 'strong_password';"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE athena_prod TO athena_user;"

# Run migrations
make migrate-up
```

## Deployment

### Automated Deployment

```bash
# Deploy with all safety checks
./scripts/deploy.sh

# Deploy without tests (emergency)
./scripts/deploy.sh -t

# Deploy without backup (not recommended)
./scripts/deploy.sh -s
```

### Manual Deployment

```bash
# 1. Build and start services
docker compose -f docker-compose.prod.yml up -d

# 2. Wait for services to be healthy
docker compose -f docker-compose.prod.yml ps

# 3. Run migrations
docker compose -f docker-compose.prod.yml exec app make migrate-up

# 4. Verify deployment
curl -f http://localhost:8080/health
```

### Deployment Verification

```bash
# Health checks
curl http://localhost:8080/health
curl http://localhost:8080/ready

# Service status
docker compose -f docker-compose.prod.yml ps

# Logs
docker compose -f docker-compose.prod.yml logs -f app
```

## Monitoring & Observability

### Prometheus Metrics

The application exposes metrics at `/metrics` endpoint:

```bash
# View metrics
curl http://localhost:8080/metrics

# Key metrics to monitor:
# - http_requests_total
# - http_request_duration_seconds
# - athena_video_processing_queue_size
# - database_connections
# - redis_memory_usage
```

### Grafana Dashboard

Access Grafana at `http://yourdomain.com:3000`:
- Username: `admin`
- Password: Set via `GRAFANA_PASSWORD` environment variable

**Key Dashboards:**
- Application Performance
- Database Metrics
- Redis Metrics
- System Resources

### Log Management

**Structured Logging:**
```json
{
  "level": "info",
  "time": "2024-01-01T12:00:00Z",
  "message": "Request processed",
  "method": "POST",
  "path": "/api/v1/videos",
  "status_code": 201,
  "duration_ms": 150
}
```

**Log Aggregation:**
```bash
# Send logs to external service
docker compose -f docker-compose.prod.yml logs -f | \
  fluentd -c fluentd.conf
```

### Alerting

**Critical Alerts:**
- Service down
- High error rate (>5%)
- Database connection issues
- Disk space low (<10%)
- Memory usage high (>80%)

## Backup & Recovery

### Automated Backups

```bash
# Create full backup
./scripts/backup.sh

# Database only backup
./scripts/backup.sh -t db

# Backup with S3 upload
./scripts/backup.sh -s

# Custom retention (7 days)
./scripts/backup.sh -r 7
```

### Backup Schedule

**Recommended Schedule:**
```bash
# Daily database backup
0 2 * * * /opt/athena/scripts/backup.sh -t db -s

# Weekly full backup
0 3 * * 0 /opt/athena/scripts/backup.sh -s

# Monthly archive
0 4 1 * * /opt/athena/scripts/backup.sh -s -r 90
```

### Recovery Procedures

**Database Recovery:**
```bash
# Stop application
docker compose -f docker-compose.prod.yml down

# Restore database
psql -h localhost -U athena_user -d athena < backup_20240101_120000.sql

# Start application
docker compose -f docker-compose.prod.yml up -d
```

**Full System Recovery:**
```bash
# Restore from backup
tar -xzf backup_20240101_120000.tar.gz
cp -r backup_20240101_120000/files/* ./

# Restore database
psql -h localhost -U athena_user -d athena < backup_20240101_120000/database.sql

# Restart services
docker compose -f docker-compose.prod.yml up -d
```

## Scaling

### Horizontal Scaling

**Load Balancer Configuration:**
```nginx
upstream athena_backend {
    server app1:8080;
    server app2:8080;
    server app3:8080;
}
```

**Database Scaling:**
```bash
# Read replicas
DATABASE_URL=postgres://user:pass@master:5432/athena
DATABASE_READ_URL=postgres://user:pass@replica:5432/athena
```

### Vertical Scaling

**Resource Limits:**
```yaml
# docker-compose.prod.yml
services:
  app:
    deploy:
      resources:
        limits:
          memory: 2G
          cpus: '4.0'
        reservations:
          memory: 1G
          cpus: '2.0'
```

### Performance Tuning

**Database Optimization:**
```sql
-- Increase connection pool
ALTER SYSTEM SET max_connections = 200;

-- Optimize for video processing
ALTER SYSTEM SET shared_buffers = '256MB';
ALTER SYSTEM SET effective_cache_size = '1GB';
```

**Redis Optimization:**
```bash
# Redis configuration
maxmemory 2gb
maxmemory-policy allkeys-lru
save 900 1
save 300 10
save 60 10000
```

## Troubleshooting

### Common Issues

**Service Won't Start:**
```bash
# Check logs
docker compose -f docker-compose.prod.yml logs app

# Check resource usage
docker stats

# Verify environment variables
docker compose -f docker-compose.prod.yml config
```

**Database Connection Issues:**
```bash
# Test connection
psql $DATABASE_URL -c "SELECT 1;"

# Check PostgreSQL logs
docker compose -f docker-compose.prod.yml logs postgres

# Verify network connectivity
docker compose -f docker-compose.prod.yml exec app ping postgres
```

**High Memory Usage:**
```bash
# Check memory usage
docker stats

# Analyze memory usage
docker compose -f docker-compose.prod.yml exec app top

# Restart with more memory
docker compose -f docker-compose.prod.yml down
docker compose -f docker-compose.prod.yml up -d
```

### Performance Issues

**Slow Response Times:**
```bash
# Check database performance
docker compose -f docker-compose.prod.yml exec postgres psql -U athena_user -d athena -c "
SELECT query, mean_time, calls 
FROM pg_stat_statements 
ORDER BY mean_time DESC 
LIMIT 10;
"

# Check Redis performance
docker compose -f docker-compose.prod.yml exec redis redis-cli info memory
```

**Video Processing Issues:**
```bash
# Check FFmpeg
docker compose -f docker-compose.prod.yml exec app ffmpeg -version

# Check processing queue
curl http://localhost:8080/metrics | grep processing_queue

# Monitor processing logs
docker compose -f docker-compose.prod.yml logs -f app | grep processing
```

### Emergency Procedures

**Service Outage:**
```bash
# 1. Check service status
docker compose -f docker-compose.prod.yml ps

# 2. Restart services
docker compose -f docker-compose.prod.yml restart

# 3. Check logs for errors
docker compose -f docker-compose.prod.yml logs --tail=100 app

# 4. Rollback if necessary
git checkout HEAD~1
./scripts/deploy.sh
```

**Data Corruption:**
```bash
# 1. Stop all services
docker compose -f docker-compose.prod.yml down

# 2. Restore from latest backup
./scripts/backup.sh -r 1 | tail -1 | xargs -I {} tar -xzf {}

# 3. Restart services
docker compose -f docker-compose.prod.yml up -d
```

## Maintenance

### Regular Maintenance Tasks

**Daily:**
- Check service health
- Monitor error rates
- Review logs for issues
- Verify backup completion

**Weekly:**
- Update system packages
- Review performance metrics
- Clean up old logs
- Test backup restoration

**Monthly:**
- Security updates
- Performance optimization
- Capacity planning
- Disaster recovery testing

### Update Procedures

**Application Updates:**
```bash
# 1. Create backup
./scripts/backup.sh

# 2. Pull latest code
git pull origin main

# 3. Update dependencies
make deps

# 4. Run tests
make test

# 5. Deploy
./scripts/deploy.sh
```

**Infrastructure Updates:**
```bash
# Update Docker images
docker compose -f docker-compose.prod.yml pull

# Update system packages
sudo apt update && sudo apt upgrade -y

# Restart services
docker compose -f docker-compose.prod.yml restart
```

### Health Checks

**Automated Health Checks:**
```bash
#!/bin/bash
# health_check.sh

# Check application health
if ! curl -f http://localhost:8080/health; then
    echo "Application health check failed"
    exit 1
fi

# Check database connectivity
if ! docker compose -f docker-compose.prod.yml exec -T app pg_isready; then
    echo "Database health check failed"
    exit 1
fi

# Check Redis connectivity
if ! docker compose -f docker-compose.prod.yml exec -T redis redis-cli ping; then
    echo "Redis health check failed"
    exit 1
fi

echo "All health checks passed"
```

**Cron Job:**
```bash
# Add to crontab
*/5 * * * * /opt/athena/health_check.sh >> /var/log/athena-health.log 2>&1
```

---

## Support

For production support:
- Check logs: `docker compose -f docker-compose.prod.yml logs`
- Monitor metrics: `http://yourdomain.com:9090`
- Review documentation: [README.md](../README.md)
- Open issues: [GitHub Issues](https://github.com/yegamble/athena/issues)
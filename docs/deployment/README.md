# Deployment Guide

## Prerequisites

- Linux server (Ubuntu 22.04 LTS recommended)
- Docker & Docker Compose installed
- Domain name with SSL certificate
- PostgreSQL 15+ (managed or self-hosted)
- Redis 7+ (managed or self-hosted)
- IPFS node (optional, for decentralized storage)
- S3-compatible storage (optional, for cold storage)

## Quick Start

### Development Deployment

```bash
# Clone repository
git clone https://github.com/yegamble/athena.git
cd athena

# Configure environment
cp .env.example .env
# Edit .env with your settings

# Start services
docker compose up -d

# Check health
curl http://localhost:8080/health
```

### Production Deployment

See detailed guides:
- [Docker Deployment](docker.md) - Container-based deployment
- [Kubernetes Deployment](kubernetes.md) - Cloud-native deployment
- [Security Configuration](security.md) - Hardening guide
- [Database Setup](database.md) - PostgreSQL configuration
- [Monitoring Setup](monitoring.md) - Observability stack

## Environment Configuration

### Required Variables

```bash
# Core Configuration
NODE_ENV=production
LOG_LEVEL=info

# Database
DATABASE_URL=postgres://user:pass@host:5432/athena?sslmode=require
DATABASE_MAX_CONNECTIONS=25

# Redis
REDIS_URL=redis://:password@host:6379/0

# JWT Security
JWT_SECRET=<generate-with-openssl-rand-hex-32>
JWT_ACCESS_TOKEN_EXPIRY=15m
JWT_REFRESH_TOKEN_EXPIRY=7d

# Server
SERVER_PORT=8080
SERVER_HOST=0.0.0.0
```

### Optional Features

```bash
# IPFS Storage
IPFS_ENABLED=true
IPFS_API=http://ipfs:5001
IPFS_GATEWAY=https://gateway.ipfs.io

# S3 Storage
S3_ENABLED=true
S3_ENDPOINT=https://s3.amazonaws.com
S3_BUCKET=athena-videos
S3_ACCESS_KEY=<access-key>
S3_SECRET_KEY=<secret-key>

# Federation
FEDERATION_ENABLED=true
ATPROTO_HANDLE=your-instance.com
BLUESKY_ENABLED=true
```

## Database Setup

### 1. Create Database

```sql
CREATE DATABASE athena;
CREATE USER athena_app WITH PASSWORD 'strong_password';
GRANT ALL PRIVILEGES ON DATABASE athena TO athena_app;
```

### 2. Run Migrations

```bash
# Using Atlas
atlas migrate apply \
  --dir "file://migrations" \
  --url "$DATABASE_URL"

# Check status
atlas migrate status \
  --dir "file://migrations" \
  --url "$DATABASE_URL"
```

### 3. Create Indexes

```sql
-- Performance indexes
CREATE INDEX idx_videos_status ON videos(processing_status);
CREATE INDEX idx_videos_created ON videos(created_at DESC);
CREATE INDEX idx_users_email ON users(email);

-- Full-text search
CREATE INDEX idx_videos_search ON videos USING gin(
  to_tsvector('english', title || ' ' || description)
);
```

## SSL/TLS Configuration

### Using Let's Encrypt

```bash
# Install certbot
sudo apt update
sudo apt install certbot python3-certbot-nginx

# Generate certificate
sudo certbot --nginx -d yourdomain.com

# Auto-renewal
sudo systemctl enable certbot.timer
```

### Manual SSL Setup

Place certificates in `/etc/nginx/ssl/`:
- `cert.pem` - SSL certificate
- `key.pem` - Private key
- `chain.pem` - Certificate chain (optional)

## Reverse Proxy Setup

### NGINX Configuration

```nginx
server {
    listen 443 ssl http2;
    server_name yourdomain.com;

    ssl_certificate /etc/nginx/ssl/cert.pem;
    ssl_certificate_key /etc/nginx/ssl/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /api {
        proxy_pass http://localhost:8080;
        client_max_body_size 5G;
        proxy_read_timeout 600s;
    }
}
```

### Caddy Configuration

```caddyfile
yourdomain.com {
    reverse_proxy localhost:8080

    handle_path /api/* {
        reverse_proxy localhost:8080 {
            timeout 600s
        }
    }

    request_body {
        max_size 5GB
    }
}
```

## Health Checks

### Endpoints

- `/health` - Liveness check
- `/ready` - Readiness check (includes dependencies)
- `/metrics` - Prometheus metrics

### Monitoring Script

```bash
#!/bin/bash
# healthcheck.sh

check_health() {
    response=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health)
    if [ $response -ne 200 ]; then
        echo "Health check failed: $response"
        # Alert or restart service
    fi
}

check_health
```

## Backup Strategy

### Database Backup

```bash
# Backup script
#!/bin/bash
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_DIR="/backups"

# Database backup
pg_dump $DATABASE_URL > $BACKUP_DIR/db_$DATE.sql

# Compress
gzip $BACKUP_DIR/db_$DATE.sql

# Upload to S3 (optional)
aws s3 cp $BACKUP_DIR/db_$DATE.sql.gz s3://backups/
```

### Redis Backup

```bash
# Save snapshot
redis-cli BGSAVE

# Copy RDB file
cp /var/lib/redis/dump.rdb /backups/redis_$DATE.rdb
```

## Scaling

### Horizontal Scaling

1. **Application Servers**: Run multiple instances behind load balancer
2. **Database**: Set up read replicas for read-heavy workloads
3. **Redis**: Use Redis Sentinel or Cluster for HA
4. **Storage**: Distribute across IPFS cluster nodes

### Vertical Scaling

Recommended resources:
- **Application**: 2-4 vCPU, 4-8 GB RAM
- **Database**: 4 vCPU, 8-16 GB RAM, SSD storage
- **Redis**: 2 vCPU, 4 GB RAM
- **IPFS**: 2 vCPU, 4 GB RAM, ample storage

## Troubleshooting

### Common Issues

1. **Connection refused**
   - Check firewall rules
   - Verify service is running
   - Check bind addresses

2. **502 Bad Gateway**
   - Application not responding
   - Check logs: `docker compose logs app`
   - Verify health endpoint

3. **Database connection errors**
   - Check connection string
   - Verify network connectivity
   - Check max connections limit

4. **High memory usage**
   - Tune Go garbage collector: `GOGC=50`
   - Limit worker pool size
   - Check for memory leaks

### Debug Commands

```bash
# Check service status
docker compose ps

# View logs
docker compose logs -f app

# Database connections
psql -c "SELECT count(*) FROM pg_stat_activity;"

# Redis memory
redis-cli INFO memory

# System resources
htop
iostat -x 1
```

## Performance Tuning

### Database Optimization

```sql
-- Update statistics
ANALYZE;

-- Vacuum tables
VACUUM ANALYZE videos;

-- Check slow queries
SELECT query, calls, mean_exec_time
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT 10;
```

### Application Tuning

```bash
# Environment variables
GOMAXPROCS=4  # Match CPU cores
GOGC=100      # GC threshold
GOMEMLIMIT=4GiB  # Memory limit
```

### Redis Optimization

```bash
# redis.conf
maxmemory 2gb
maxmemory-policy allkeys-lru
save ""  # Disable persistence if not needed
```

## Security Checklist

- [ ] SSL/TLS enabled
- [ ] Firewall configured
- [ ] Secrets in environment variables
- [ ] Database user privileges restricted
- [ ] Rate limiting enabled
- [ ] Security headers configured
- [ ] Regular security updates
- [ ] Backup encryption enabled
- [ ] Audit logging configured
- [ ] Monitoring alerts set up

## Support

For deployment issues:
- Check [Troubleshooting Guide](troubleshooting.md)
- Review application logs
- Open an issue on [GitHub](https://github.com/yegamble/athena/issues)

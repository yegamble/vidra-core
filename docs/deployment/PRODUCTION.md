# Production Deployment Guide

## 🚀 Production Checklist

### Prerequisites

- [ ] Linux server (Ubuntu 22.04 LTS recommended)
- [ ] Docker & Docker Compose installed
- [ ] Domain name with SSL certificate
- [ ] PostgreSQL 15+ (managed or self-hosted)
- [ ] Redis 7+ (managed or self-hosted)
- [ ] IPFS node (optional, for decentralized storage)
- [ ] S3-compatible storage (for backup/cold storage)
- [ ] Monitoring infrastructure (Prometheus/Grafana)

## 🔒 Security Configuration

### 1. Environment Variables

Create a production `.env` file with strong secrets:

```bash
# Generate secure secrets
openssl rand -hex 32  # For JWT_SECRET
openssl rand -hex 32  # For ENCRYPTION_KEY
openssl rand -hex 16  # For API keys

# Production environment
NODE_ENV=production
LOG_LEVEL=info

# Database (use connection pooling)
DATABASE_URL=postgres://user:pass@host:5432/athena?sslmode=require&pool_max_conns=25
DATABASE_MAX_CONNECTIONS=25
DATABASE_MAX_IDLE=5
DATABASE_MAX_LIFETIME=5m

# Redis (with password)
REDIS_URL=redis://:password@host:6379/0
REDIS_MAX_RETRIES=3
REDIS_POOL_SIZE=10

# JWT Configuration
JWT_SECRET=<64-character-hex-string>
JWT_ACCESS_TOKEN_EXPIRY=15m
JWT_REFRESH_TOKEN_EXPIRY=7d

# Rate Limiting
RATE_LIMIT_REQUESTS_PER_MINUTE=60
RATE_LIMIT_BURST=10

# Upload Limits
MAX_UPLOAD_SIZE=5368709120  # 5GB
MAX_CHUNK_SIZE=33554432     # 32MB

# IPFS Configuration
IPFS_API=http://ipfs-node:5001
IPFS_GATEWAY=https://gateway.ipfs.io
IPFS_CLUSTER_API=http://ipfs-cluster:9094
ENABLE_IPFS_CLUSTER=true

# S3 Storage (for cold storage)
S3_ENDPOINT=https://s3.amazonaws.com
S3_BUCKET=athena-videos
S3_ACCESS_KEY=<access-key>
S3_SECRET_KEY=<secret-key>
S3_REGION=us-east-1

# Security Headers
ENABLE_SECURITY_HEADERS=true
ENABLE_CORS=true
CORS_ORIGINS=https://yourdomain.com
ENABLE_RATE_LIMITING=true

# Monitoring
ENABLE_METRICS=true
METRICS_PORT=9090
ENABLE_TRACING=true
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317

# ATProto Federation (Optional)
FEDERATION_ENABLED=false  # Set to true to enable federation
ATPROTO_ENABLED=false     # Set to true to enable ATProto
ATPROTO_HANDLE=your-instance.com
ATPROTO_DID=did:web:your-instance.com

# Bluesky Integration (Optional)
BLUESKY_ENABLED=false  # Set to true to enable Bluesky integration
BLUESKY_PDS_URL=https://bsky.social
BLUESKY_HANDLE=your-account.bsky.social
BLUESKY_APP_PASSWORD=<app-specific-password>  # Generate from Bluesky settings
BLUESKY_FIREHOSE_URL=wss://bsky.network
BLUESKY_FIREHOSE_RECONNECT_DELAY=5s
BLUESKY_FIREHOSE_MAX_RETRIES=10

# Federation Performance
FEDERATION_SYNC_INTERVAL=300  # Sync every 5 minutes
FEDERATION_MAX_CONCURRENT_SYNCS=5
FEDERATION_TIMEOUT=30s
```

### 2. Security Middleware

The application includes comprehensive security middleware:

- **Security Headers**: X-Frame-Options, CSP, HSTS, etc.
- **Rate Limiting**: Per-IP and per-user limits
- **Request Size Limiting**: Prevents DoS attacks
- **CORS**: Configurable origin restrictions
- **API Key Authentication**: Alternative to JWT for services
- **Request ID Tracking**: For debugging and audit logs

### 3. Database Security

```sql
-- Create application user with limited privileges
CREATE USER athena_app WITH PASSWORD 'strong_password';
GRANT CONNECT ON DATABASE athena TO athena_app;
GRANT USAGE ON SCHEMA public TO athena_app;
GRANT CREATE ON SCHEMA public TO athena_app;

-- Grant table permissions
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO athena_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO athena_app;

-- Create read-only user for analytics
CREATE USER athena_readonly WITH PASSWORD 'strong_password';
GRANT CONNECT ON DATABASE athena TO athena_readonly;
GRANT USAGE ON SCHEMA public TO athena_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO athena_readonly;
```

## 🐳 Docker Production Deployment

### 1. Production Docker Compose

```yaml
version: '3.8'

services:
  app:
    image: athena:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"  # Only bind to localhost
    environment:
      - NODE_ENV=production
    env_file:
      - .env.production
    volumes:
      - ./uploads:/app/uploads
      - ./logs:/app/logs
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 4G
        reservations:
          cpus: '1'
          memory: 2G
    logging:
      driver: "json-file"
      options:
        max-size: "100m"
        max-file: "3"

  postgres:
    image: postgres:15-alpine
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
      POSTGRES_DB: athena
      POSTGRES_USER: athena_app
    secrets:
      - db_password
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./init-db.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U athena_app"]
      interval: 10s
      timeout: 5s
      retries: 5
    deploy:
      resources:
        limits:
          cpus: '1'
          memory: 2G

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: redis-server --requirepass ${REDIS_PASSWORD} --appendonly yes
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "--raw", "incr", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 1G

  nginx:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./ssl:/etc/nginx/ssl:ro
      - ./uploads:/var/www/uploads:ro
    depends_on:
      - app

volumes:
  postgres_data:
  redis_data:

secrets:
  db_password:
    file: ./secrets/db_password.txt
```

### 2. NGINX Configuration

```nginx
upstream athena_backend {
    server app:8080 max_fails=3 fail_timeout=30s;
}

server {
    listen 80;
    server_name yourdomain.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name yourdomain.com;

    ssl_certificate /etc/nginx/ssl/cert.pem;
    ssl_certificate_key /etc/nginx/ssl/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;

    # Security headers
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # Rate limiting
    limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
    limit_req zone=api burst=20 nodelay;

    # API endpoints
    location /api {
        proxy_pass http://athena_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Timeouts for long uploads
        proxy_connect_timeout 600;
        proxy_send_timeout 600;
        proxy_read_timeout 600;
        send_timeout 600;

        # Upload size
        client_max_body_size 5G;
        client_body_buffer_size 32M;
    }

    # Static files (if any)
    location /uploads {
        alias /var/www/uploads;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
}
```

## 📊 Monitoring & Observability

### 1. Prometheus Metrics

The application exposes metrics at `/metrics`:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'athena'
    static_configs:
      - targets: ['app:9090']
    metrics_path: '/metrics'
```

Key metrics to monitor:

- Request latency (p50, p95, p99)
- Request rate and error rate
- Database connection pool stats
- Redis operation latency
- Upload/processing queue depth
- IPFS pinning success rate

### 2. Health Checks

- `/health` - Liveness probe
- `/ready` - Readiness probe (checks DB, Redis, IPFS)

## 🌐 ATProto Federation Setup (Optional)

ATProto federation enables cross-platform content syndication with Bluesky and other AT Protocol services.

### Federation Configuration Methods

#### 1. Environment Variables (Recommended for Production)

Configure federation through environment variables in your `.env` file:

```bash
# Enable federation features
FEDERATION_ENABLED=true
ATPROTO_ENABLED=true

# Instance identity
ATPROTO_HANDLE=your-instance.com
ATPROTO_DID=did:web:your-instance.com
PUBLIC_BASE_URL=https://your-instance.com

# Bluesky integration
BLUESKY_ENABLED=true
BLUESKY_PDS_URL=https://bsky.social
BLUESKY_HANDLE=your-account.bsky.social
BLUESKY_APP_PASSWORD=xxxx-xxxx-xxxx-xxxx  # Generate from Bluesky settings
BLUESKY_FIREHOSE_URL=wss://bsky.network

# Federation scheduler
ENABLE_FEDERATION_SCHEDULER=true
FEDERATION_SCHEDULER_INTERVAL_SECONDS=15
FEDERATION_SCHEDULER_BURST=1
FEDERATION_INGEST_INTERVAL_SECONDS=60
```

#### 2. Admin API Configuration

Configure federation settings via the admin API:

```bash
# Set instance DID
curl -X PUT \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true, "instanceDid": "did:web:your-instance.com", "instanceHandle": "your-instance.com"}' \
  https://your-instance.com/api/v1/admin/federation/config

# Configure Bluesky integration
curl -X PUT \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "blueskyEnabled": true,
    "blueskyHandle": "your-account.bsky.social",
    "blueskyAppPassword": "xxxx-xxxx-xxxx-xxxx",
    "autoPublish": true
  }' \
  https://your-instance.com/api/v1/admin/federation/config
```

### Federation Features

1. **DID Document**: Served at `/.well-known/atproto-did` for instance identification
2. **Automatic Post Creation**: Public videos are automatically posted to Bluesky
3. **Firehose Subscription**: Real-time updates from the Bluesky network
4. **Federated Timeline**: Aggregated content at `/api/v1/federation/timeline`
5. **Peer Management**: Add and manage federation peers through the admin API

### Managing Federation Peers

```bash
# Add a federation peer
curl -X POST \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "did": "did:plc:peer-instance",
    "handle": "peer.example.com",
    "serviceUrl": "https://peer.example.com"
  }' \
  https://your-instance.com/api/v1/admin/federation/peers

# List active peers
curl -H "Authorization: Bearer <ADMIN_TOKEN>" \
  https://your-instance.com/api/v1/admin/federation/peers?status=active
```

### Content Filtering

Configure content filtering for ingested posts:

```bash
# Set actors to follow
curl -X PUT \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"value": ["did:plc:abcd1234", "alice.bsky.social"], "is_public": false}' \
  https://your-instance.com/api/v1/admin/instance/config/atproto_ingest_actors

# Block content with specific labels
curl -X PUT \
  -H "Authorization: Bearer <ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"value": ["porn", "sexual", "violence"], "is_public": false}' \
  https://your-instance.com/api/v1/admin/instance/config/atproto_block_labels
```

### Monitoring Federation

Check federation health and statistics:

```bash
# Get federation status
curl https://your-instance.com/api/v1/federation/status

# View federated timeline
curl https://your-instance.com/api/v1/federation/timeline?limit=20
```

### 3. Logging

Configure structured logging with log aggregation:

```yaml
# fluentd or similar
<source>
  @type forward
  port 24224
  bind 0.0.0.0
</source>

<match athena.**>
  @type elasticsearch
  host elasticsearch
  port 9200
  index_name athena
  type_name logs
</match>
```

## 🔄 Deployment Process

### 1. Blue-Green Deployment

```bash
#!/bin/bash
# deploy.sh

# Build new image
docker build -t athena:new .

# Test new image
docker run --rm athena:new /app/athena --test

# Start new container alongside old
docker-compose up -d --scale app=2

# Health check new container
sleep 30
curl -f http://localhost:8080/health || exit 1

# Switch traffic to new container
docker-compose stop app_old
docker-compose rm -f app_old

# Tag as latest
docker tag athena:new athena:latest
```

### 2. Database Migrations

```bash
# Always backup before migrations
pg_dump -h localhost -U athena_app athena > backup_$(date +%Y%m%d).sql

# Run migrations
make migrate-prod

# Verify migrations
psql -h localhost -U athena_app -d athena -c "SELECT * FROM schema_migrations;"
```

### 3. Rollback Plan

```bash
# Quick rollback
docker-compose down
docker-compose up -d --scale app=1 athena:previous

# Database rollback (if needed)
psql -h localhost -U postgres -d athena < backup_20240101.sql
```

## 🎯 Performance Tuning

See [Performance Tuning Guide](../operations/PERFORMANCE.md) for detailed configuration.

## 🚨 Disaster Recovery

### 1. Backup Strategy

```bash
# Daily database backup
0 2 * * * pg_dump -h localhost -U athena_app athena | gzip > /backup/athena_$(date +\%Y\%m\%d).sql.gz

# Weekly full backup
0 3 * * 0 tar -czf /backup/athena_full_$(date +\%Y\%m\%d).tar.gz /app/uploads /var/lib/postgresql/data

# Sync to S3
0 4 * * * aws s3 sync /backup s3://athena-backups/ --delete
```

### 2. Monitoring Alerts

Configure alerts for:

- High error rate (>1%)
- High latency (p95 > 1s)
- Database connection pool exhaustion
- Disk space < 20%
- Memory usage > 80%
- Upload queue depth > 100

## 📝 Maintenance

### Regular Tasks

- **Daily**: Check logs for errors, monitor metrics
- **Weekly**: Review performance metrics, update dependencies
- **Monthly**: Security updates, capacity planning
- **Quarterly**: Disaster recovery drill, performance audit

### Useful Commands

```bash
# View logs
docker-compose logs -f app

# Database console
docker-compose exec postgres psql -U athena_app athena

# Redis console
docker-compose exec redis redis-cli

# Restart services
docker-compose restart app

# Scale horizontally
docker-compose up -d --scale app=3

# Emergency shutdown
docker-compose stop
```

## 🆘 Troubleshooting

### Common Issues

1. **High Memory Usage**
   - Check for memory leaks: `pprof`
   - Tune GC: `GOGC=50`
   - Reduce connection pools

2. **Slow Queries**
   - Enable slow query log
   - Run `EXPLAIN ANALYZE`
   - Add missing indexes

3. **Upload Failures**
   - Check disk space
   - Verify file permissions
   - Review nginx timeout settings

4. **IPFS Issues**
   - Check IPFS node status
   - Verify network connectivity
   - Review pinning queue

## 📞 Support

For production support:

- Create an issue: <https://github.com/yegamble/athena/issues>
- Security issues: <security@yourdomain.com>
- Documentation: <https://docs.yourdomain.com>

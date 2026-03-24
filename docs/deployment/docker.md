# Docker Deployment Guide

## Production Docker Compose

### Complete Stack Configuration

```yaml
version: '3.8'

services:
  app:
    image: vidra:latest
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
      POSTGRES_DB: vidra
      POSTGRES_USER: vidra_app
    secrets:
      - db_password
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./docker/postgres/init/init-db.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vidra_app"]
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

  ipfs:
    image: ipfs/kubo:latest
    restart: unless-stopped
    volumes:
      - ipfs_data:/data/ipfs
    ports:
      - "127.0.0.1:5001:5001"  # API
      - "127.0.0.1:8081:8080"  # Gateway
    environment:
      - IPFS_PROFILE=server
    deploy:
      resources:
        limits:
          cpus: '1'
          memory: 2G

  ffmpeg-worker:
    image: vidra:latest
    restart: unless-stopped
    command: ["./vidra-server", "--worker-mode"]
    env_file:
      - .env.production
    volumes:
      - ./uploads:/app/uploads
      - ./processed:/app/processed
    depends_on:
      - redis
    deploy:
      replicas: 2
      resources:
        limits:
          cpus: '4'
          memory: 8G

volumes:
  postgres_data:
  redis_data:
  ipfs_data:

secrets:
  db_password:
    file: ./secrets/db_password.txt
```

## Building the Docker Image

### Multi-Stage Dockerfile

```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o vidra-server ./cmd/server

# Production stage
FROM alpine:3.18

RUN apk add --no-cache ca-certificates ffmpeg

WORKDIR /app

COPY --from=builder /app/vidra-server .
COPY --from=builder /app/migrations ./migrations

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["./vidra-server", "health"]

CMD ["./vidra-server"]
```

### Build Commands

```bash
# Build image
docker build -t vidra:latest .

# Build with specific version
docker build -t vidra:v1.0.0 .

# Build and push to registry
docker build -t registry.example.com/vidra:latest .
docker push registry.example.com/vidra:latest
```

## Deployment Steps

### 1. Prepare Environment

```bash
# Create directory structure
mkdir -p vidra/{uploads,logs,processed,ssl,secrets}

# Generate secrets
openssl rand -hex 32 > vidra/secrets/db_password.txt
openssl rand -hex 32 > vidra/secrets/jwt_secret.txt

# Set permissions
chmod 600 vidra/secrets/*
```

### 2. Configure Environment

Create `.env.production`:

```bash
# Application
NODE_ENV=production
LOG_LEVEL=info
SERVER_PORT=8080

# Database
DATABASE_URL=postgres://vidra_app:CHANGE_ME@postgres:5432/vidra?sslmode=disable
DATABASE_MAX_CONNECTIONS=25
DATABASE_MAX_IDLE=5

# Redis
REDIS_URL=redis://:CHANGE_ME@redis:6379/0
REDIS_PASSWORD=CHANGE_ME

# JWT
JWT_SECRET_FILE=/run/secrets/jwt_secret

# Storage
UPLOAD_DIR=/app/uploads
PROCESSED_DIR=/app/processed
MAX_UPLOAD_SIZE=5368709120

# IPFS
IPFS_ENABLED=true
IPFS_API=http://ipfs:5001

# Workers
WORKER_POOL_SIZE=4
PROCESSING_TIMEOUT=3600s
```

### 3. Initialize Database

Create `docker/postgres/init/init-db.sql`:

```sql
-- Create extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "unaccent";

-- Create application user
CREATE USER vidra_app WITH PASSWORD 'CHANGE_ME';
GRANT ALL PRIVILEGES ON DATABASE vidra TO vidra_app;

-- Performance settings
ALTER SYSTEM SET shared_buffers = '256MB';
ALTER SYSTEM SET effective_cache_size = '1GB';
ALTER SYSTEM SET maintenance_work_mem = '64MB';
ALTER SYSTEM SET checkpoint_completion_target = 0.9;
ALTER SYSTEM SET wal_buffers = '16MB';
ALTER SYSTEM SET default_statistics_target = 100;
ALTER SYSTEM SET random_page_cost = 1.1;
```

### 4. Deploy Stack

```bash
# Pull images
docker compose pull

# Start services
docker compose up -d

# Check status
docker compose ps

# View logs
docker compose logs -f

# Run migrations
docker compose exec app ./vidra-server migrate
```

## Service Management

### Start/Stop Services

```bash
# Start all services
docker compose up -d

# Stop all services
docker compose down

# Restart specific service
docker compose restart app

# Scale workers
docker compose up -d --scale ffmpeg-worker=4
```

### Update Deployment

```bash
# Pull latest image
docker pull vidra:latest

# Rolling update
docker compose up -d --no-deps app

# Verify new version
docker compose exec app ./vidra-server version
```

### Maintenance Mode

```bash
# Enable maintenance mode
docker compose exec app ./vidra-server maintenance --enable

# Disable maintenance mode
docker compose exec app ./vidra-server maintenance --disable
```

## Monitoring

### Health Checks

```bash
# Check application health
curl http://localhost:8080/health

# Check readiness
curl http://localhost:8080/ready

# View metrics
curl http://localhost:8080/metrics
```

### Container Stats

```bash
# Resource usage
docker stats

# Detailed inspection
docker compose top

# Check logs
docker compose logs --tail=100 -f app
```

## Backup and Restore

### Backup Volumes

```bash
#!/bin/bash
# backup.sh

DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_DIR="/backups/$DATE"

mkdir -p $BACKUP_DIR

# Backup database
docker compose exec -T postgres pg_dump -U vidra_app vidra | gzip > $BACKUP_DIR/postgres.sql.gz

# Backup Redis
docker compose exec -T redis redis-cli SAVE
docker compose cp redis:/data/dump.rdb $BACKUP_DIR/redis.rdb

# Backup uploads
tar czf $BACKUP_DIR/uploads.tar.gz ./uploads

# Backup IPFS (if needed)
docker compose exec ipfs ipfs repo stat
tar czf $BACKUP_DIR/ipfs.tar.gz ./ipfs_data
```

### Restore from Backup

```bash
#!/bin/bash
# restore.sh

BACKUP_DIR="/backups/20240101_120000"

# Restore database
gunzip < $BACKUP_DIR/postgres.sql.gz | docker compose exec -T postgres psql -U vidra_app vidra

# Restore Redis
docker compose cp $BACKUP_DIR/redis.rdb redis:/data/dump.rdb
docker compose restart redis

# Restore uploads
tar xzf $BACKUP_DIR/uploads.tar.gz -C ./

# Restore IPFS
tar xzf $BACKUP_DIR/ipfs.tar.gz -C ./
docker compose restart ipfs
```

## Troubleshooting

### Common Issues

#### Container Won't Start

```bash
# Check logs
docker compose logs app

# Verify environment
docker compose config

# Check disk space
df -h
```

#### Database Connection Issues

```bash
# Test connection
docker compose exec app psql $DATABASE_URL -c "SELECT 1"

# Check postgres logs
docker compose logs postgres
```

#### High Memory Usage

```bash
# Check memory limits
docker compose exec app cat /proc/meminfo

# Adjust GOGC
docker compose exec app sh -c 'export GOGC=50 && ./vidra-server'
```

### Debug Mode

```bash
# Run with debug logging
docker compose run -e LOG_LEVEL=debug app

# Interactive shell
docker compose exec app sh

# Attach to running container
docker attach vidra_app_1
```

## Security Best Practices

1. **Use secrets management** - Never hardcode passwords
2. **Limit port exposure** - Bind to localhost when possible
3. **Regular updates** - Keep base images updated
4. **Resource limits** - Prevent resource exhaustion
5. **Health checks** - Ensure container recovery
6. **Logging** - Centralize and rotate logs
7. **Network isolation** - Use custom networks
8. **Read-only filesystems** - Where possible
9. **Non-root user** - Run processes as non-root
10. **Image scanning** - Scan for vulnerabilities

## Performance Optimization

### Docker Settings

```json
{
  "storage-driver": "overlay2",
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  },
  "default-ulimits": {
    "nofile": {
      "Hard": 131072,
      "Soft": 131072
    }
  }
}
```

### Compose Optimizations

```yaml
# Use tmpfs for temporary files
tmpfs:
  - /tmp:size=1G

# Enable build cache
x-build-cache: &build-cache
  cache_from:
    - vidra:cache
  cache_to:
    - type=inline
```

# Claude Operations Runbook - Athena Backend

## Common Operations

### Service Management

#### Start Services
```bash
# Development
docker compose up -d

# Production
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# Specific services
docker compose up -d postgres redis ipfs
```

#### Stop Services
```bash
# Graceful shutdown
docker compose down

# With volume cleanup
docker compose down -v

# Emergency stop
docker compose kill
```

#### View Logs
```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f server

# Last N lines
docker compose logs --tail=100 server
```

### Database Operations

#### Connect to Database
```bash
# Direct connection
psql postgres://user:password@localhost:5432/athena

# Via Docker
docker compose exec postgres psql -U athena_user -d athena

# Test database
DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" psql
```

#### Run Migrations
```bash
# Apply pending migrations
atlas migrate apply \
  --dir "file://migrations" \
  --url "postgres://user:password@localhost:5432/athena?sslmode=disable"

# Check migration status
atlas migrate status \
  --dir "file://migrations" \
  --url "postgres://user:password@localhost:5432/athena?sslmode=disable"

# Create new migration
atlas migrate diff add_feature \
  --dir "file://migrations" \
  --to "file://schema.hcl" \
  --dev-url "postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable"
```

#### Database Queries
```sql
-- Check video processing status
SELECT id, title, processing_status, created_at
FROM videos
WHERE processing_status != 'completed'
ORDER BY created_at DESC;

-- View recent uploads
SELECT v.id, v.title, c.name as channel, u.username
FROM videos v
JOIN channels c ON v.channel_id = c.id
JOIN users u ON c.user_id = u.id
ORDER BY v.created_at DESC
LIMIT 10;

-- Check federation queue
SELECT * FROM federation_queue
WHERE status = 'pending'
ORDER BY created_at;

-- User activity
SELECT username, last_active_at, created_at
FROM users
WHERE last_active_at > NOW() - INTERVAL '1 day';
```

### Redis Operations

#### Connect to Redis
```bash
# CLI access
redis-cli -h localhost -p 6379

# With Docker
docker compose exec redis redis-cli

# Test instance
redis-cli -h localhost -p 6380
```

#### Common Redis Commands
```bash
# Check health
ping

# View all keys (careful in production!)
keys *

# Check rate limiting
get rate_limit:user:123

# View session
get session:abc123

# Check upload chunks
smembers upload:chunks:video123

# Clear specific cache
del cache:video:metadata:123

# Monitor commands
monitor
```

### IPFS Operations

#### Check IPFS Status
```bash
# API version
curl http://localhost:5001/api/v0/version

# Peer info
curl http://localhost:5001/api/v0/id

# Connected peers
curl http://localhost:5001/api/v0/swarm/peers

# Repository stats
curl http://localhost:5001/api/v0/repo/stat
```

#### IPFS Content Management
```bash
# Pin content
ipfs pin add QmHash

# List pinned content
ipfs pin ls

# Unpin content
ipfs pin rm QmHash

# Get content
ipfs cat QmHash

# Check if pinned
ipfs pin ls --type=recursive | grep QmHash
```

### Testing Operations

#### Run Tests
```bash
# All tests (short mode for CI)
go test -short -race ./...

# Specific package
go test -v ./internal/httpapi

# Integration tests
go test -tags=integration ./...

# With coverage
go test -cover -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Benchmarks
go test -bench=. ./internal/processing
```

#### Test Troubleshooting
```bash
# Run with verbose output
go test -v -run TestName ./package

# Skip failing test temporarily
go test -short -skip TestFlaky ./...

# Test with specific environment
DATABASE_URL="..." REDIS_URL="..." go test ./...

# Clean test cache
go clean -testcache
```

### Building & Deployment

#### Build Application
```bash
# Local build
go build -o bin/athena-server ./cmd/server

# Production build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -o bin/athena-server ./cmd/server

# Docker build
docker build -t athena:latest .

# Multi-stage build
docker build --target production -t athena:prod .
```

#### Health Checks
```bash
# Liveness probe
curl http://localhost:8080/health

# Readiness probe
curl http://localhost:8080/ready

# Metrics endpoint
curl http://localhost:9090/metrics

# API status
curl http://localhost:8080/api/v1/status
```

### Video Processing

#### Check Processing Queue
```bash
# View pending jobs
redis-cli lrange video:processing:queue 0 -1

# Check job status
redis-cli hget video:processing:job:123 status

# View FFmpeg processes
ps aux | grep ffmpeg

# Check transcoding logs
docker compose logs -f server | grep "ffmpeg"
```

#### Manual Video Processing
```bash
# Trigger reprocessing
curl -X POST http://localhost:8080/api/v1/admin/videos/123/reprocess \
  -H "Authorization: Bearer $TOKEN"

# Check video metadata
ffprobe -v quiet -print_format json -show_format -show_streams video.mp4
```

### Federation Operations

#### Check Federation Status
```bash
# Federation health
curl http://localhost:8080/api/v1/federation/status

# View DID document
curl http://localhost:8080/.well-known/atproto-did

# Check Bluesky connection
curl http://localhost:8080/api/v1/admin/federation/bluesky/status

# View federation metrics
curl http://localhost:8080/api/v1/admin/federation/metrics
```

#### Debug Federation Issues
```bash
# Check federation logs
docker compose logs -f server | grep "federation"

# View failed federation tasks
SELECT * FROM federation_queue WHERE status = 'failed';

# Retry failed task
UPDATE federation_queue SET status = 'pending', retry_count = 0 WHERE id = 'task-id';

# Check blocklist
SELECT * FROM federation_blocklist;
```

### Monitoring & Debugging

#### Application Metrics
```bash
# Prometheus metrics
curl http://localhost:9090/metrics | grep -E "(http_|db_|redis_)"

# Request rate
curl http://localhost:9090/metrics | grep http_requests_total

# Database pool stats
curl http://localhost:9090/metrics | grep db_pool

# Memory usage
go tool pprof http://localhost:6060/debug/pprof/heap
```

#### Debug Mode
```bash
# Run with debug logging
LOG_LEVEL=debug go run ./cmd/server

# Enable pprof
go run -tags debug ./cmd/server

# CPU profiling
go tool pprof http://localhost:6060/debug/pprof/profile

# Trace requests
curl -H "X-Debug: true" http://localhost:8080/api/v1/videos
```

### Emergency Procedures

#### High Load Response
```bash
# Increase rate limits temporarily
redis-cli set rate_limit:global 1000

# Disable expensive features
redis-cli set feature:federation:enabled false

# Scale workers
docker compose up -d --scale worker=5

# Clear cache
redis-cli flushdb
```

#### Data Recovery
```bash
# Backup database
pg_dump -h localhost -U user -d athena > backup.sql

# Restore database
psql -h localhost -U user -d athena < backup.sql

# Export Redis data
redis-cli --rdb dump.rdb

# Restore Redis
redis-cli --pipe < dump.txt
```

#### Service Recovery
```bash
# Restart hung service
docker compose restart server

# Clear stuck jobs
redis-cli del video:processing:queue

# Reset federation queue
UPDATE federation_queue SET status = 'pending', retry_count = 0
WHERE status = 'processing' AND updated_at < NOW() - INTERVAL '1 hour';

# Rebuild search index
psql -c "REINDEX INDEX CONCURRENTLY videos_search_idx;"
```

### Performance Tuning

#### Database Optimization
```sql
-- Analyze tables
ANALYZE videos;
VACUUM ANALYZE;

-- Check slow queries
SELECT query, calls, mean_exec_time
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT 10;

-- Index usage
SELECT schemaname, tablename, indexname, idx_scan
FROM pg_stat_user_indexes
ORDER BY idx_scan;
```

#### Cache Optimization
```bash
# Check cache hit rate
redis-cli info stats | grep keyspace_hits

# TTL audit
redis-cli --scan --pattern "*" | xargs -I {} redis-cli ttl {}

# Memory usage by pattern
redis-cli --bigkeys
```

### Useful Aliases

Add to your shell profile:
```bash
alias athena-logs='docker compose logs -f server'
alias athena-db='docker compose exec postgres psql -U athena_user -d athena'
alias athena-redis='docker compose exec redis redis-cli'
alias athena-test='go test -short -race ./...'
alias athena-lint='golangci-lint run ./...'
alias athena-build='go build -o bin/athena-server ./cmd/server'
```
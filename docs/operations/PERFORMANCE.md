# Performance Tuning Guide

This guide covers performance optimization strategies for Athena in production.

## 🎯 Database Tuning

### 1. PostgreSQL Tuning

For a production database with 16GB RAM:

```sql
-- postgresql.conf
shared_buffers = 4GB                # 25% of RAM
effective_cache_size = 12GB         # 75% of RAM
maintenance_work_mem = 1GB
checkpoint_completion_target = 0.9
wal_buffers = 16MB
default_statistics_target = 100
random_page_cost = 1.1
effective_io_concurrency = 200
work_mem = 16MB                     # Careful with high connection counts
min_wal_size = 1GB
max_wal_size = 4GB
max_worker_processes = 8
max_parallel_workers_per_gather = 4
max_parallel_workers = 8
max_parallel_maintenance_workers = 4
```

### 2. Redis Tuning

```conf
# redis.conf
maxmemory 2gb
maxmemory-policy allkeys-lru      # Evict least recently used keys when full
save 900 1                        # RDB snapshotting
save 300 10
save 60 10000
appendonly yes                    # AOF persistence
appendfsync everysec              # fsync every second (good balance)
```

## 🚀 System & Application Tuning

### 1. OS Limits

**File Descriptors (`/etc/security/limits.conf`):**
```bash
* soft nofile 65536
* hard nofile 65536
```

**Sysctl Tuning (`/etc/sysctl.conf`):**
```bash
# Networking
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 8192
net.core.netdev_max_backlog = 16384
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_notsent_lowat = 16384

# Keepalive
net.ipv4.tcp_keepalive_time = 60
net.ipv4.tcp_keepalive_intvl = 10
net.ipv4.tcp_keepalive_probes = 6
```

### 2. Go Runtime

- **GOGC**: Set to `50` (default 100) to trigger GC more frequently and reduce memory spikes, or `200` to trade memory for CPU.
- **GOMAXPROCS**: Automatically set to available CPUs.

### 3. Transcoding Optimization

- **Workers**: Scale transcode workers horizontally on separate nodes.
- **FFmpeg**: Ensure hardware acceleration (NVENC/VAAPI) is enabled if available.
- **Storage**: Use NVMe/SSD for temporary processing directories to speed up disk I/O.

### 4. Content Delivery Network (CDN)

- **Strategy**: Use a CDN (Cloudflare, AWS CloudFront) for:
  - HLS segments (`.ts`, `.m4s`)
  - Thumbnails and avatars
  - Static assets
- **Caching Headers**:
  - Immutable files (segments, images): `Cache-Control: public, max-age=31536000, immutable`
  - Manifests (`.m3u8`): `Cache-Control: public, max-age=2` (short TTL for live)
  - API responses: `Cache-Control: no-store`

## 📊 Benchmarking

See `tests/load/README.md` for load testing instructions.

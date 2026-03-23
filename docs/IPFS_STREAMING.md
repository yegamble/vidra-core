# IPFS Streaming Guide

## Overview

Athena supports optional IPFS-based streaming for HLS (HTTP Live Streaming) content. This feature allows videos to be delivered through IPFS gateways instead of (or in addition to) the local filesystem, providing:

- **Decentralized delivery** - Content distributed across IPFS network
- **CDN-like performance** - Multiple gateway redundancy
- **Automatic fallback** - Seamlessly falls back to local filesystem if IPFS is unavailable
- **Range request support** - Efficient partial content delivery for seeking
- **Health monitoring** - Automatic gateway health checks and rotation

## Architecture

```
┌──────────────┐
│  HLS Client  │
└──────┬───────┘
       │
       ▼
┌──────────────────┐
│  HLS Handlers    │
└──────┬───────────┘
       │
       ▼
┌──────────────────────────┐
│  IPFS Streaming Service  │
└──────┬──────────┬────────┘
       │          │
       ▼          ▼
┌──────────┐  ┌──────────┐
│   IPFS   │  │  Local   │
│ Gateways │  │   File   │
└──────────┘  └──────────┘
```

## Configuration

### Environment Variables

Add to your `.env` file:

```bash
# Enable IPFS streaming (default: false)
ENABLE_IPFS_STREAMING=true

# Comma-separated list of IPFS gateway URLs
# Defaults to public gateways if not specified
IPFS_GATEWAY_URLS=https://ipfs.io,https://dweb.link,https://cloudflare-ipfs.com

# Timeout for IPFS streaming requests in seconds (default: 30)
IPFS_STREAMING_TIMEOUT=30

# Prefer local filesystem over IPFS for faster delivery (default: true)
IPFS_STREAMING_PREFER_LOCAL=true

# Gateway health check interval in seconds (default: 60)
IPFS_GATEWAY_HEALTH_CHECK_INTERVAL=60

# Max retries for failed IPFS streaming requests (default: 3)
IPFS_STREAMING_MAX_RETRIES=3

# Fallback to local filesystem if IPFS fails (default: true)
IPFS_STREAMING_FALLBACK_TO_LOCAL=true

# Buffer size for IPFS streaming in bytes (default: 32768 = 32KB)
IPFS_STREAMING_BUFFER_SIZE=32768
```

### Deployment Modes

#### Mode 1: IPFS-First (Decentralized)

Best for: Maximum decentralization, reduced bandwidth costs

```bash
ENABLE_IPFS_STREAMING=true
IPFS_STREAMING_PREFER_LOCAL=false
IPFS_STREAMING_FALLBACK_TO_LOCAL=true
```

**Behavior**: Always tries IPFS first, falls back to local if IPFS fails

#### Mode 2: Local-First (Performance)

Best for: Low latency, guaranteed performance

```bash
ENABLE_IPFS_STREAMING=true
IPFS_STREAMING_PREFER_LOCAL=true
IPFS_STREAMING_FALLBACK_TO_LOCAL=true
```

**Behavior**: Uses local filesystem when available, uses IPFS as backup

#### Mode 3: IPFS-Only (Pure Decentralized)

Best for: Testing, pure IPFS deployments

```bash
ENABLE_IPFS_STREAMING=true
IPFS_STREAMING_PREFER_LOCAL=false
IPFS_STREAMING_FALLBACK_TO_LOCAL=false
```

**Behavior**: Only uses IPFS, returns 503 if IPFS is unavailable

#### Mode 4: Disabled (Local Only)

Best for: Traditional deployments, maximum performance

```bash
ENABLE_IPFS_STREAMING=false
```

**Behavior**: All streaming served from local filesystem

## Usage

### Storing HLS Content in IPFS

When transcoding completes, store the HLS playlists and segments in IPFS:

```go
// Example: Store master playlist in IPFS
cid, err := ipfsService.PinFile(ctx, masterPlaylistPath)
if err != nil {
    log.Errorf("Failed to pin master playlist: %v", err)
}

// Store CID in database for retrieval
video.MasterPlaylistCID = cid
```

### Streaming from IPFS

The IPFS streaming service automatically handles:

1. **CID Resolution**: Converts file paths to IPFS CIDs via database lookup
2. **Gateway Selection**: Chooses healthy gateways with load balancing
3. **Range Requests**: Supports HTTP range headers for seeking
4. **Retries**: Automatically retries failed requests with exponential backoff
5. **Fallback**: Falls back to local filesystem if IPFS fails

### API Endpoints

#### Get Master Playlist

```bash
GET /api/v1/videos/livestream/{streamID}/master.m3u8
```

If IPFS streaming is enabled and CID exists:

1. Tries IPFS gateway delivery
2. Falls back to local filesystem if IPFS fails

#### Get Variant Playlist

```bash
GET /api/v1/videos/livestream/{streamID}/{variant}/index.m3u8
```

Same IPFS → local fallback behavior.

#### Get Segment

```bash
GET /api/v1/videos/livestream/{streamID}/{variant}/{segment}.ts
```

Supports HTTP range requests for efficient seeking.

#### IPFS Metrics

```bash
GET /api/v1/videos/ipfs/metrics
```

Returns:

```json
{
  "enabled": true,
  "metrics": {
    "ipfs_requests": 12345,
    "ipfs_successes": 12000,
    "ipfs_failures": 345,
    "local_requests": 5678,
    "local_successes": 5678,
    "local_failures": 0,
    "cache_hits": 8901,
    "cache_misses": 3444,
    "ipfs_success_rate": 97.2,
    "local_success_rate": 100.0,
    "cache_hit_rate": 72.1
  }
}
```

#### Gateway Health

```bash
GET /api/v1/videos/ipfs/gateways
```

Returns:

```json
{
  "enabled": true,
  "gateways": [
    {
      "url": "https://ipfs.io",
      "healthy": true,
      "last_checked": "2025-11-04T17:35:00Z",
      "response_time_ms": 234
    },
    {
      "url": "https://dweb.link",
      "healthy": true,
      "last_checked": "2025-11-04T17:35:01Z",
      "response_time_ms": 456
    }
  ]
}
```

## Gateway Selection

The service uses intelligent gateway selection:

1. **Health Filtering**: Only uses gateways marked as healthy
2. **Round-Robin**: Distributes load evenly across healthy gateways
3. **Automatic Failover**: Tries next gateway if request fails
4. **Health Checks**: Periodically tests gateway availability
5. **Response Time Tracking**: Monitors gateway performance

## Performance Considerations

### Benefits

- **CDN-like delivery**: Multiple gateways provide redundancy
- **Bandwidth offloading**: Reduces server bandwidth usage
- **Decentralization**: Content survives even if origin server is down

### Trade-offs

- **Latency**: IPFS gateway requests may have higher latency than local
- **Reliability**: Dependent on IPFS network and gateway availability
- **Complexity**: Additional infrastructure to manage

### Recommendations

For **production deployments**:

- Use `IPFS_STREAMING_PREFER_LOCAL=true` for guaranteed performance
- Configure multiple reliable IPFS gateways
- Monitor metrics via `/api/v1/videos/ipfs/metrics`
- Set up alerts for high failure rates

For **decentralized deployments**:

- Use `IPFS_STREAMING_PREFER_LOCAL=false` to maximize IPFS usage
- Use public gateways or run your own IPFS gateway cluster
- Accept slightly higher latency for decentralization benefits

## Monitoring

### Metrics to Watch

1. **Success Rates**: Should be >95% for production
2. **Gateway Health**: All gateways should be healthy
3. **Response Times**: Should be <500ms for good UX
4. **Fallback Rate**: High rate may indicate IPFS issues

### Example Prometheus Queries

```promql
# IPFS success rate
rate(ipfs_streaming_successes_total[5m]) / rate(ipfs_streaming_requests_total[5m])

# Gateway health
ipfs_gateway_healthy{gateway="https://ipfs.io"}

# Fallback rate
rate(local_streaming_requests_total[5m]) / (rate(ipfs_streaming_requests_total[5m]) + rate(local_streaming_requests_total[5m]))
```

## Troubleshooting

### IPFS streaming is slow

1. Check gateway response times: `GET /api/v1/videos/ipfs/gateways`
2. Try different gateways or run your own
3. Enable local-first mode: `IPFS_STREAMING_PREFER_LOCAL=true`

### High IPFS failure rate

1. Check gateway health: `GET /api/v1/videos/ipfs/gateways`
2. Verify CIDs are pinned to IPFS
3. Check network connectivity to IPFS gateways
4. Increase timeout: `IPFS_STREAMING_TIMEOUT=60`

### Content not found

1. Verify CID is stored in database
2. Check if content is pinned to IPFS
3. Test CID directly: `https://ipfs.io/ipfs/{cid}`
4. Enable fallback: `IPFS_STREAMING_FALLBACK_TO_LOCAL=true`

## Testing

### Unit Tests

```bash
go test ./internal/usecase/ipfs_streaming/...
```

### Integration Tests

```bash
# Start test environment with IPFS
docker compose up -d postgres redis ipfs

# Run integration tests
ENABLE_IPFS_STREAMING=true \
IPFS_GATEWAY_URLS=http://localhost:8080 \
go test ./internal/httpapi/handlers/video/... -v
```

### Manual Testing

1. Upload a video and transcode to HLS
2. Pin HLS content to IPFS
3. Store CIDs in database
4. Enable IPFS streaming
5. Play video and monitor metrics

## Security Considerations

### Gateway Trust

- Use trusted IPFS gateways (official or self-hosted)
- Verify content integrity via CIDs
- Consider rate limiting gateway requests

### Data Privacy

- IPFS content is publicly accessible
- Don't use IPFS for private/restricted content
- Use local-only streaming for sensitive videos

### DDoS Protection

- IPFS gateways provide natural DDoS protection
- Fallback to local prevents complete outages
- Monitor metrics for abuse patterns

## Future Enhancements

Planned features:

- [ ] Content-addressable caching layer
- [ ] IPFS Cluster integration for pinning
- [ ] Automatic CID generation on transcode
- [ ] Gateway performance benchmarking
- [ ] Custom gateway weighting/priorities
- [ ] IPFS pubsub for live streaming
- [ ] Integration with Filecoin for archival

## Resources

- [IPFS Documentation](https://docs.ipfs.tech/)
- [HLS Specification](https://datatracker.ietf.org/doc/html/rfc8216)
- [HTTP Range Requests](https://developer.mozilla.org/en-US/docs/Web/HTTP/Range_requests)
- [Content Addressing](https://proto.school/content-addressing)

# IPFS Video Upload Implementation

## Overview

This document describes the implementation of IPFS upload functionality for video resolution variants. Each resolution variant (240p, 360p, 480p, 720p, 1080p, etc.) gets uploaded to IPFS with its own unique CID (Content Identifier) since each file has different content.

## Architecture

### Components

1. **IPFS Client** (`internal/ipfs/client.go`)
   - Handles file and directory uploads to IPFS
   - Supports local Kubo node pinning
   - Optional IPFS Cluster pinning for redundancy
   - Concurrent upload support for multiple variants

2. **Encoding Service** (`internal/usecase/encoding/service.go`)
   - Integrated IPFS upload after video transcoding
   - Uploads each resolution variant directory to IPFS
   - Stores CIDs in the database for each variant
   - Best-effort uploads (won't fail encoding on IPFS errors)

3. **Video Repository** (`internal/repository/video_repository.go`)
   - New method: `UpdateProcessingInfoWithCIDs`
   - Stores ProcessedCIDs map (resolution → CID)
   - Stores thumbnail and preview CIDs

4. **Database Schema**
   - `videos.processed_cids` - JSONB map of resolution labels to CIDs
   - `videos.thumbnail_cid` - CID for video thumbnail
   - Existing `videos.output_paths` - Local file paths for variants

## How It Works

### 1. Video Upload and Encoding

When a user uploads a video:

```
User uploads video
       ↓
Chunked upload service receives file
       ↓
Encoding job created
       ↓
Encoding service processes job:
  1. Transcode to multiple resolutions (240p, 360p, 480p, etc.)
  2. Generate HLS playlists and segments for each resolution
  3. Generate master playlist
  4. Upload each resolution directory to IPFS
  5. Upload thumbnail and preview to IPFS
  6. Store all CIDs in database
  7. Mark video as completed
```

### 2. IPFS Upload Process

For each resolution variant:

```go
// Example: 720p variant
resolutionDir := "/storage/streaming-playlists/hls/{videoID}/720p/"
// Contains: stream.m3u8, segment_00000.ts, segment_00001.ts, ...

// Upload entire directory as IPFS UnixFS directory
cid, err := ipfsClient.AddDirectoryAndPin(ctx, resolutionDir)
// Returns: "bafybeig..." (CIDv1)

// Store in database
processedCIDs["720p"] = "bafybeig..."
```

### 3. Data Structure

The `Video` model stores CIDs for each variant:

```go
type Video struct {
    ID            string
    ProcessedCIDs map[string]string  // {"720p": "bafybeig...", "1080p": "bafybeih..."}
    ThumbnailCID  string              // "bafybeij..."
    OutputPaths   map[string]string  // {"720p": "/storage/.../720p/stream.m3u8"}
    // ... other fields
}
```

### 4. Concurrent Uploads

All resolution variants are uploaded concurrently using goroutines:

```go
for _, resolution := range ["240p", "360p", "480p", "720p", "1080p"] {
    go uploadVariant(resolution)  // Parallel uploads
}
```

This significantly speeds up the upload process for videos with many variants.

## Configuration

IPFS upload is controlled by existing environment variables:

```bash
# IPFS Kubo API endpoint
IPFS_API=http://localhost:5001

# IPFS Cluster API endpoint (optional, for redundancy)
IPFS_CLUSTER=http://localhost:9094

# IPFS is required by default
REQUIRE_IPFS=true
```

## CID Format

All CIDs use CIDv1 format with the following parameters:
- **Version**: CIDv1 (modern format)
- **Codec**: dag-pb (UnixFS)
- **Hash**: sha256
- **Options**: `raw-leaves=true` for better deduplication

Example CID: `bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi`

## File Structure on IPFS

Each resolution variant is uploaded as a directory:

```
bafybei... (720p root)
├── stream.m3u8           # HLS playlist
├── segment_00000.ts      # Video segment
├── segment_00001.ts
├── segment_00002.ts
└── ...
```

Access via gateway: `https://ipfs.io/ipfs/{CID}/stream.m3u8`

## Benefits

### 1. Unique CIDs Per Resolution
- Each resolution has different content → different CID
- Content-addressable: same resolution variant always has same CID
- Deduplication across the network
- Integrity verification built-in

### 2. Decentralized Delivery
- Content available through IPFS network
- Not dependent on single server
- Geographic distribution via IPFS gateways
- Resilient to server failures

### 3. Bandwidth Optimization
- Users can fetch only the resolution they need
- HLS adaptive bitrate works seamlessly
- Gateway caching reduces origin load

### 4. Hybrid Approach
- Local files remain primary source
- IPFS as backup/distribution layer
- Gradual migration possible
- Fallback to local on IPFS issues

## Fallback Behavior

The system is designed to be resilient:

1. **IPFS Upload Failure**: Video encoding continues, local files remain available
2. **IPFS Unavailable**: Streaming falls back to local filesystem
3. **Partial Upload**: Successfully uploaded variants are saved, failed ones are skipped
4. **Network Issues**: Configurable timeout (120s default) prevents indefinite hangs

## Testing

Run the encoding tests to verify IPFS upload (with IPFS disabled for unit tests):

```bash
go test ./internal/usecase/encoding/... -v
```

For integration testing with real IPFS:

```bash
# Start IPFS daemon
ipfs daemon

# Run integration tests
go test ./internal/ipfs/... -v
```

## Monitoring

### Metrics

IPFS upload metrics are tracked:
- Total uploads attempted
- Successful uploads
- Failed uploads
- Upload duration per variant

### Logs

The encoding service logs IPFS upload progress:
```
INFO: Uploading variant 720p to IPFS...
INFO: Successfully uploaded 720p to IPFS: bafybeig...
WARN: Failed to upload 1080p to IPFS: context deadline exceeded
```

## Performance Considerations

### Upload Time

Typical upload times per variant (depends on network and IPFS node):
- **240p** (~50MB): 5-10 seconds
- **480p** (~150MB): 15-30 seconds
- **720p** (~300MB): 30-60 seconds
- **1080p** (~600MB): 60-120 seconds

### Optimization Strategies

1. **Parallel Uploads**: All variants uploaded concurrently
2. **Local IPFS Node**: Run Kubo locally to avoid network overhead
3. **IPFS Cluster**: Use cluster for faster pinning across nodes
4. **Timeout**: Set appropriate timeout based on expected file sizes
5. **Best-Effort**: Don't block encoding on IPFS issues

## Future Enhancements

Potential improvements:

1. **Retry Logic**: Automatic retry on transient failures
2. **Resume Support**: Resume interrupted uploads
3. **Garbage Collection**: Unpin old/unused variants
4. **Metrics Dashboard**: Real-time upload monitoring
5. **Multi-Gateway**: Upload to multiple gateways simultaneously
6. **CDN Integration**: Use IPFS gateways as CDN origin

## Troubleshooting

### IPFS Not Reachable

```
ERROR: Failed to connect to IPFS API at http://localhost:5001
```

**Solution**:
- Ensure IPFS daemon is running: `ipfs daemon`
- Check IPFS_API configuration
- Set `REQUIRE_IPFS=false` to continue without IPFS

### Upload Timeout

```
WARN: Failed to upload variant to IPFS: context deadline exceeded
```

**Solution**:
- Increase timeout in `internal/app/app.go` (currently 120s)
- Check network connectivity to IPFS node
- Consider running IPFS locally

### Pinning Failures

```
WARN: Failed to pin CID to cluster
```

**Solution**:
- Check IPFS Cluster health
- Verify IPFS_CLUSTER configuration
- Pinning to cluster is optional (local pin still succeeds)

## API Integration

The HLS streaming handlers automatically use ProcessedCIDs when IPFS streaming is enabled:

```go
// Fetch variant from IPFS or fallback to local
GET /api/v1/videos/{id}/stream/{resolution}/stream.m3u8

// Uses ProcessedCIDs["720p"] if IPFS streaming enabled
// Falls back to local file if IPFS unavailable
```

See `docs/IPFS_STREAMING.md` for streaming configuration.

## Database Queries

Query videos by IPFS availability:

```sql
-- Videos with IPFS CIDs
SELECT id, title, processed_cids
FROM videos
WHERE processed_cids != '{}';

-- Videos with specific resolution on IPFS
SELECT id, title
FROM videos
WHERE processed_cids ? '720p';

-- Count variants per video
SELECT id, jsonb_object_keys(processed_cids) as resolution
FROM videos;
```

## Summary

This implementation provides a robust, scalable solution for storing video variants on IPFS:

✅ **Automatic**: Uploads happen during encoding, no manual intervention
✅ **Resilient**: Failures don't break video processing
✅ **Efficient**: Concurrent uploads minimize time overhead
✅ **Flexible**: Works with or without IPFS
✅ **Scalable**: Distributed storage reduces origin load
✅ **Verifiable**: CIDs provide cryptographic integrity

The system seamlessly integrates IPFS into the video processing pipeline while maintaining compatibility with existing local storage workflows.

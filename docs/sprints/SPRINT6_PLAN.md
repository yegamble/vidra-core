# Sprint 6: HLS Transcoding for Live Streams - PLAN

**Status**: 📋 Planning
**Target Date**: 2025-10-20
**Dependencies**: Sprint 5 (RTMP Server) ✅

## Overview

Sprint 6 adds real-time HLS (HTTP Live Streaming) transcoding to the live streaming infrastructure built in Sprint 5. This enables viewers to watch live streams in their browsers with adaptive bitrate streaming, DVR capabilities, and optional replay saving.

## Goals

1. **Real-time Transcoding**: Convert RTMP input to HLS output with minimal latency
2. **Adaptive Bitrate**: Generate multiple quality variants (360p-1080p)
3. **Browser Playback**: Enable HTML5 video player support
4. **DVR Support**: Allow viewers to pause and rewind live streams
5. **Replay Saving**: Convert ended streams to VOD content
6. **IPFS Integration**: Upload replays to IPFS for decentralized storage

## Architecture

```
┌─────────────┐
│ OBS/Client  │ RTMP publish
└──────┬──────┘
       │
       ▼
┌──────────────────────────────────────────┐
│         RTMPServer (Sprint 5)            │
│  - Accepts RTMP stream                   │
│  - Authenticates stream key              │
│  - Manages stream sessions               │
└──────┬───────────────────────────────────┘
       │
       ├─────► StreamManager (Sprint 5)
       │       └─ State tracking, viewer counts
       │
       └─────► NEW: HLSTranscoder
               ├─ FFmpeg process management
               ├─ Quality variant generation
               ├─ Segment creation (2s chunks)
               ├─ Master playlist (.m3u8)
               └─ Variant playlists

┌──────────────────────────────────────────┐
│         HLS Output                       │
│  Storage: ./storage/live/{streamId}/     │
│  - master.m3u8 (master playlist)         │
│  - 1080p/index.m3u8                      │
│  - 1080p/segment_000.ts                  │
│  - 720p/index.m3u8                       │
│  - 720p/segment_000.ts                   │
│  - 480p/index.m3u8                       │
│  - 480p/segment_000.ts                   │
│  - 360p/index.m3u8                       │
│  - 360p/segment_000.ts                   │
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│         HTTP API                         │
│  GET /api/v1/streams/{id}/hls/master.m3u8│
│  GET /api/v1/streams/{id}/hls/{variant}/index.m3u8│
│  GET /api/v1/streams/{id}/hls/{variant}/segment_XXX.ts│
└──────────────────────────────────────────┘

       │ After stream ends (optional)
       ▼
┌──────────────────────────────────────────┐
│         VOD Conversion                   │
│  - Concatenate segments                  │
│  - Create replay video                   │
│  - Upload to IPFS                        │
│  - Link to videos table                  │
└──────────────────────────────────────────┘
```

## Components

### 1. HLS Transcoder Service

**File**: `internal/livestream/hls_transcoder.go`

```go
type HLSTranscoder struct {
    cfg           *config.Config
    streamRepo    repository.LiveStreamRepository
    logger        *logrus.Logger
    activeStreams map[uuid.UUID]*TranscodeSession
    mu            sync.RWMutex
}

type TranscodeSession struct {
    StreamID      uuid.UUID
    FFmpegProcess *exec.Cmd
    OutputDir     string
    Variants      []QualityVariant
    StartedAt     time.Time
    SegmentCount  int
    Ctx           context.Context
    Cancel        context.CancelFunc
}

type QualityVariant struct {
    Name       string // "1080p", "720p", "480p", "360p"
    Width      int
    Height     int
    Bitrate    int    // Video bitrate in kbps
    AudioRate  int    // Audio bitrate in kbps
    Framerate  int    // Target framerate
}
```

**Key Methods**:
- `StartTranscoding(streamID, rtmpURL)` - Spawn FFmpeg process
- `StopTranscoding(streamID)` - Gracefully stop FFmpeg
- `GetStreamHealth(streamID)` - Check transcoding status
- `CleanupSegments(streamID)` - Remove old segments (DVR window)

### 2. FFmpeg Command Builder

**Approach**: Use FFmpeg with multiple outputs for adaptive bitrate streaming

**Command Template**:
```bash
ffmpeg -i rtmp://localhost:1935/{streamKey} \
  -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac -ar 48000 -b:a 128k \
  -f hls -hls_time 2 -hls_list_size 10 -hls_flags delete_segments+append_list \
  -master_pl_name master.m3u8 \
  -var_stream_map "v:0,a:0 v:1,a:1 v:2,a:2 v:3,a:3" \
  -s:v:0 1920x1080 -b:v:0 5000k -maxrate:v:0 5350k -bufsize:v:0 7500k \
  -s:v:1 1280x720  -b:v:1 2800k -maxrate:v:1 2996k -bufsize:v:1 4200k \
  -s:v:2 854x480   -b:v:2 1400k -maxrate:v:2 1498k -bufsize:v:2 2100k \
  -s:v:3 640x360   -b:v:3 800k  -maxrate:v:3 856k  -bufsize:v:3 1200k \
  ./storage/live/{streamId}/%v/index.m3u8
```

**Features**:
- Multiple quality variants in single FFmpeg process
- Low-latency tuning (`zerolatency`)
- Fast encoding preset (`veryfast`)
- 2-second segments for low latency
- 10-segment window (20s DVR buffer)
- Automatic segment deletion
- Master playlist generation

### 3. HLS Serving Endpoints

**Routes** (add to `routes_refactored.go`):

```go
// Live stream HLS endpoints
r.Route("/streams/{id}/hls", func(r chi.Router) {
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/master.m3u8", hlsHandlers.GetMasterPlaylist)
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/index.m3u8", hlsHandlers.GetVariantPlaylist)
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{variant}/{segment}", hlsHandlers.GetSegment)
})
```

**Handlers**:
- `GetMasterPlaylist` - Serve master.m3u8 with variant list
- `GetVariantPlaylist` - Serve quality-specific index.m3u8
- `GetSegment` - Serve .ts segment files with proper headers

**Cache Headers**:
- Master playlist: `Cache-Control: max-age=2`
- Variant playlists: `Cache-Control: max-age=2`
- Segments: `Cache-Control: max-age=86400` (immutable)

### 4. Segment Management

**Cleanup Strategy**:
- Keep last N segments per variant (configurable, default 10)
- Delete segments older than DVR window
- Run cleanup every 10 seconds
- Keep segments on disk during stream
- Archive or delete on stream end

**Storage Structure**:
```
./storage/live/
├── {stream-id-1}/
│   ├── master.m3u8
│   ├── 1080p/
│   │   ├── index.m3u8
│   │   ├── segment_000.ts
│   │   ├── segment_001.ts
│   │   └── segment_002.ts
│   ├── 720p/
│   │   ├── index.m3u8
│   │   └── segment_*.ts
│   ├── 480p/
│   │   └── ...
│   └── 360p/
│       └── ...
└── {stream-id-2}/
    └── ...
```

### 5. VOD Conversion Service

**File**: `internal/livestream/vod_converter.go`

**Workflow**:
1. Stream ends → trigger VOD conversion (if `save_replay=true`)
2. Select best quality variant (1080p or highest available)
3. Concatenate all segments into single video file
4. Run FFmpeg with `+faststart` for web playback
5. Create video record in `videos` table
6. Upload to IPFS
7. Update `live_streams.replay_video_id`
8. Clean up live segments

**FFmpeg Command**:
```bash
# Create file list
for f in ./storage/live/{streamId}/1080p/segment_*.ts; do
  echo "file '$f'" >> filelist.txt
done

# Concatenate and optimize
ffmpeg -f concat -safe 0 -i filelist.txt \
  -c copy -movflags +faststart \
  ./storage/replays/{streamId}_replay.mp4
```

### 6. Configuration

**New Config Fields** (add to `internal/config/config.go`):

```go
// HLS Configuration
HLSOutputDir            string        // Base directory for HLS output
HLSSegmentDuration      int           // Segment duration in seconds (default: 2)
HLSPlaylistSize         int           // Number of segments in playlist (default: 10)
HLSCleanupInterval      time.Duration // Cleanup interval (default: 10s)
HLSVariants             []string      // Enabled variants: "1080p,720p,480p,360p"

// Transcoding
FFmpegPath              string        // Path to ffmpeg binary (default: "ffmpeg")
FFmpegPreset            string        // Encoding preset (default: "veryfast")
FFmpegTune              string        // Tuning (default: "zerolatency")
MaxConcurrentTranscodes int           // Max simultaneous transcodes (default: 10)

// VOD Replay
EnableReplayConversion  bool          // Enable automatic replay conversion
ReplayStorageDir        string        // Replay video storage (default: ./storage/replays)
ReplayUploadToIPFS      bool          // Upload replays to IPFS (default: true)
ReplayRetentionDays     int           // Keep replays for N days (0=forever)
```

**Environment Variables**:
```bash
# HLS
HLS_OUTPUT_DIR=./storage/live
HLS_SEGMENT_DURATION=2
HLS_PLAYLIST_SIZE=10
HLS_CLEANUP_INTERVAL=10
HLS_VARIANTS=1080p,720p,480p,360p

# Transcoding
FFMPEG_PATH=/usr/bin/ffmpeg
FFMPEG_PRESET=veryfast
FFMPEG_TUNE=zerolatency
MAX_CONCURRENT_TRANSCODES=10

# Replay
ENABLE_REPLAY_CONVERSION=true
REPLAY_STORAGE_DIR=./storage/replays
REPLAY_UPLOAD_TO_IPFS=true
REPLAY_RETENTION_DAYS=30
```

## Implementation Plan

### Phase 1: Core Transcoding (3-4 hours)

1. **Create HLS Transcoder Service**
   - [ ] `internal/livestream/hls_transcoder.go`
   - [ ] FFmpeg command builder
   - [ ] Process lifecycle management
   - [ ] Quality variant definitions
   - [ ] Error handling and logging

2. **Integrate with RTMP Server**
   - [ ] Call transcoder on stream start
   - [ ] Pass RTMP URL to FFmpeg
   - [ ] Monitor FFmpeg health
   - [ ] Stop transcoder on stream end

3. **Configuration**
   - [ ] Add HLS config fields
   - [ ] Add transcoding config fields
   - [ ] Update defaults
   - [ ] Environment variable parsing

### Phase 2: HLS Serving (2-3 hours)

4. **HLS Handlers**
   - [ ] `internal/httpapi/hls_handlers.go`
   - [ ] Master playlist endpoint
   - [ ] Variant playlist endpoint
   - [ ] Segment serving endpoint
   - [ ] Proper MIME types and headers

5. **Segment Management**
   - [ ] Segment cleanup worker
   - [ ] DVR window enforcement
   - [ ] Storage monitoring
   - [ ] Error handling for missing segments

6. **Route Registration**
   - [ ] Add HLS routes to `routes_refactored.go`
   - [ ] Add privacy checks
   - [ ] Add authentication where needed
   - [ ] CORS headers for HLS

### Phase 3: VOD Conversion (2-3 hours)

7. **VOD Converter Service**
   - [ ] `internal/livestream/vod_converter.go`
   - [ ] Segment concatenation
   - [ ] Video optimization (`+faststart`)
   - [ ] IPFS upload integration
   - [ ] Video record creation

8. **Integration**
   - [ ] Trigger on stream end
   - [ ] Background job processing
   - [ ] Progress tracking
   - [ ] Error handling and retries

### Phase 4: Testing & Polish (2-3 hours)

9. **Testing**
   - [ ] Unit tests for transcoder
   - [ ] Integration tests for HLS flow
   - [ ] Test VOD conversion
   - [ ] Test segment cleanup
   - [ ] Load testing (multiple streams)

10. **Documentation**
    - [ ] API documentation for HLS endpoints
    - [ ] Configuration guide
    - [ ] Deployment notes (FFmpeg installation)
    - [ ] Troubleshooting guide

**Total Estimated Time**: 9-13 hours

## Quality Variants

| Variant | Resolution | Video Bitrate | Audio Bitrate | Target Use Case |
|---------|------------|---------------|---------------|-----------------|
| 1080p   | 1920x1080  | 5000 kbps     | 128 kbps      | High-quality desktop/TV |
| 720p    | 1280x720   | 2800 kbps     | 128 kbps      | Desktop/laptop |
| 480p    | 854x480    | 1400 kbps     | 128 kbps      | Mobile, slower connections |
| 360p    | 640x360    | 800 kbps      | 128 kbps      | Low bandwidth |

## API Endpoints (New)

### HLS Streaming

1. **GET `/api/v1/streams/{id}/hls/master.m3u8`**
   - Returns master playlist with all variants
   - Privacy-gated (respects stream privacy setting)
   - Cache-Control: max-age=2

2. **GET `/api/v1/streams/{id}/hls/{variant}/index.m3u8`**
   - Returns variant playlist (e.g., "1080p")
   - Lists available segments
   - Cache-Control: max-age=2

3. **GET `/api/v1/streams/{id}/hls/{variant}/{segment}.ts`**
   - Returns MPEG-TS segment
   - MIME type: `video/MP2T`
   - Cache-Control: max-age=86400, immutable

### Replay Management (Optional)

4. **POST `/api/v1/streams/{id}/convert-replay`**
   - Manually trigger VOD conversion
   - Requires channel ownership
   - Returns job ID

5. **GET `/api/v1/streams/{id}/replay-status`**
   - Check VOD conversion status
   - Returns progress percentage

## Success Criteria

- ✅ Live streams automatically transcode to HLS
- ✅ Multiple quality variants available
- ✅ Viewers can watch in browser with HTML5 player
- ✅ DVR functionality works (pause/rewind)
- ✅ Ended streams convert to VOD
- ✅ Replays upload to IPFS
- ✅ All tests passing
- ✅ Documentation complete

## Dependencies

### External
- **FFmpeg 4.4+**: Required for transcoding
  - Installation: `apt install ffmpeg` (Ubuntu) or `brew install ffmpeg` (macOS)
  - Verify: `ffmpeg -version`

### Internal
- Sprint 5 RTMP server (complete) ✅
- Stream manager (complete) ✅
- Video repository (for VOD) ✅
- IPFS service (for replays) ✅

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| High CPU usage from transcoding | High | Limit concurrent streams, use fast preset |
| FFmpeg crashes | High | Monitor process, auto-restart, alert on failure |
| Disk space exhaustion | High | Segment cleanup, storage monitoring, alerts |
| Transcoding lag | Medium | Tune segment duration, reduce variants |
| Missing segments | Medium | Buffer multiple segments, error recovery |

## Performance Considerations

1. **CPU**: Each stream uses ~1 CPU core for transcoding
   - Solution: Limit `MAX_CONCURRENT_TRANSCODES`
   - Scale: Distribute across multiple servers

2. **Disk I/O**: Each stream writes ~1-2 MB/s
   - Solution: Use fast storage (SSD)
   - Cleanup: Remove old segments aggressively

3. **Network**: Each viewer uses ~800-5000 kbps
   - Solution: Use CDN for segment delivery
   - Cache: Segments are immutable, highly cacheable

4. **Memory**: FFmpeg uses ~50-100 MB per stream
   - Solution: Monitor memory usage
   - Limit: Set max streams per server

## Example Usage

### 1. Start Streaming (OBS)
```
Server: rtmp://your-server:1935
Stream Key: {your-stream-key}
```

### 2. Watch in Browser
```html
<video controls>
  <source src="https://your-server/api/v1/streams/{id}/hls/master.m3u8" type="application/x-mpegURL">
</video>

<!-- Or use Video.js for better compatibility -->
<script src="https://vjs.zencdn.net/7.20.3/video.min.js"></script>
<video id="my-video" class="video-js" controls preload="auto">
  <source src="https://your-server/api/v1/streams/{id}/hls/master.m3u8" type="application/x-mpegURL">
</video>
```

### 3. Get Replay After Stream
```bash
GET /api/v1/videos/{replay_video_id}
```

## Future Enhancements (Sprint 7+)

- **Multi-codec support**: HEVC, AV1 for better compression
- **Thumbnail generation**: Extract keyframes for timeline scrubbing
- **DVR recording**: Save last N hours to allow longer rewind
- **Edge transcoding**: Transcode closer to viewers for lower latency
- **Adaptive switching**: Seamless quality switching based on bandwidth
- **DRM support**: Encrypted HLS for premium content
- **Analytics**: Track quality switches, buffering events

---

**Status**: 📋 Ready to implement
**Next**: Begin Phase 1 - Core Transcoding

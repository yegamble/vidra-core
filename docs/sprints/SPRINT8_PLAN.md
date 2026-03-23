# Sprint 8: Torrent Support with IPFS Hybrid - Implementation Plan

**Sprint Duration:** 2 weeks (Days 1-10)
**Start Date:** 2025-10-21
**Dependencies:** Sprint 5-7 (Live Streaming complete)

## Overview

Sprint 8 implements WebTorrent support for P2P video distribution, with optional IPFS integration for decentralized storage. This creates a hybrid distribution system that reduces bandwidth costs and improves resilience.

## Goals

1. Generate valid .torrent files for all videos
2. Implement WebTorrent-compatible tracker
3. Seed torrents from backend automatically
4. Support hybrid HTTP/WebTorrent/IPFS distribution
5. Integrate with existing video pipeline
6. Ensure ActivityPub federation includes torrent/IPFS info

## Architecture

### Distribution Strategy

```
Video Upload
    ↓
Encoding (HLS segments)
    ↓
┌─────────────────────────────┐
│  Parallel Distribution       │
├─────────────────────────────┤
│ HTTP (Always)               │
│ ↓                           │
│ WebTorrent (Always)         │
│ ↓                           │
│ IPFS (If enabled)           │
│ ↓                           │
│ ActivityPub (Always)        │
│ ↓                           │
│ ATProto (If enabled)        │
└─────────────────────────────┘
```

### Components

1. **Torrent Generator** (`internal/torrent/generator.go`)
   - Creates .torrent files with WebTorrent-compatible trackers
   - Includes all HLS segments + playlist
   - Generates magnet URIs

2. **Torrent Seeder** (`internal/torrent/seeder.go`)
   - Seeds videos using anacrolix/torrent client
   - Manages bandwidth and connection limits
   - Prioritizes popular content

3. **Hybrid Storage** (`internal/storage/hybrid.go`)
   - Coordinates HTTP, WebTorrent, and IPFS
   - Implements fallback logic
   - Tracks access patterns

4. **WebTorrent Tracker** (`internal/torrent/tracker.go`)
   - WebSocket-based for browser compatibility
   - Supports announce/scrape
   - Peer discovery

## Day-by-Day Plan

### Day 1-2: Database Schema & Dependencies

**Database Migration (`049_create_torrent_tables.sql`):**

```sql
CREATE TABLE video_torrents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    info_hash TEXT NOT NULL UNIQUE,
    torrent_file_path TEXT NOT NULL,
    magnet_uri TEXT NOT NULL,
    piece_length INTEGER NOT NULL DEFAULT 262144, -- 256KB
    total_size_bytes BIGINT NOT NULL,
    seeders INTEGER DEFAULT 0,
    leechers INTEGER DEFAULT 0,
    completed_downloads INTEGER DEFAULT 0,
    is_seeding BOOLEAN DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(video_id)
);

CREATE TABLE torrent_trackers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    announce_url TEXT NOT NULL UNIQUE,
    is_websocket BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE torrent_peers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    info_hash TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    ip_address INET NOT NULL,
    port INTEGER NOT NULL,
    uploaded_bytes BIGINT DEFAULT 0,
    downloaded_bytes BIGINT DEFAULT 0,
    left_bytes BIGINT DEFAULT 0,
    event TEXT, -- started, stopped, completed
    user_agent TEXT,
    last_announce_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(info_hash, peer_id)
);

CREATE INDEX idx_video_torrents_video_id ON video_torrents(video_id);
CREATE INDEX idx_video_torrents_info_hash ON video_torrents(info_hash);
CREATE INDEX idx_torrent_peers_info_hash ON torrent_peers(info_hash);
CREATE INDEX idx_torrent_peers_last_announce ON torrent_peers(last_announce_at);

-- Insert default trackers
INSERT INTO torrent_trackers (announce_url, is_websocket, priority) VALUES
    ('wss://tracker.openwebtorrent.com', true, 1),
    ('wss://tracker.btorrent.xyz', true, 2),
    ('wss://tracker.fastcast.nz', true, 3);
```

**Dependencies:**

```bash
go get github.com/anacrolix/torrent
go get github.com/anacrolix/torrent/metainfo
go get github.com/anacrolix/torrent/bencode
```

### Day 3-4: Torrent Generator Implementation

**Files to Create:**

- `internal/domain/torrent.go` - Domain models
- `internal/torrent/generator.go` - Torrent file creation
- `internal/repository/torrent_repository.go` - Database operations

**Key Features:**

- Generate torrent from video files
- Support multi-file torrents (all HLS segments)
- WebTorrent-compatible announce URLs
- Proper piece length calculation
- Include web seeds for HTTP fallback

### Day 5-6: Torrent Seeder & Client

**Files to Create:**

- `internal/torrent/seeder.go` - Backend seeding service
- `internal/torrent/client.go` - Torrent client wrapper
- `internal/torrent/manager.go` - Lifecycle management

**Key Features:**

- Auto-seed all videos with torrents
- Configurable upload/download limits
- Connection limits per torrent
- Prioritization based on popularity
- Graceful shutdown

### Day 7: WebSocket Tracker

**Files to Create:**

- `internal/torrent/tracker.go` - Tracker server
- `internal/torrent/websocket_handler.go` - WebSocket handling

**Key Features:**

- WebTorrent protocol support
- Announce/scrape endpoints
- Peer list management
- Stats tracking

### Day 8: API Integration

**Files to Create:**

- `internal/httpapi/torrent_handlers.go` - HTTP handlers

**Endpoints:**

```
GET /api/v1/videos/:id/torrent - Download .torrent file
GET /api/v1/videos/:id/magnet - Get magnet URI
GET /api/v1/torrents/stats - Global torrent statistics
GET /api/v1/torrents/:infoHash/peers - Get peer list
POST /api/v1/torrents/:infoHash/announce - Tracker announce
```

### Day 9-10: Testing & Integration

## Testing Strategy (Comprehensive)

### Unit Tests (Target: >85% coverage)

**1. Generator Tests** (`torrent/generator_test.go`):

- Test torrent file creation with single file
- Test torrent file creation with multiple files
- Test piece length calculation
- Test info hash generation
- Test magnet URI generation
- Test bencode encoding correctness
- Test tracker URL inclusion
- Test web seed URL inclusion
- Edge cases: empty files, large files, special characters

**2. Domain Model Tests** (`domain/torrent_test.go`):

- Test torrent validation
- Test info hash validation
- Test magnet URI parsing
- Test peer validation
- Test tracker validation

**3. Repository Tests** (`repository/torrent_repository_test.go`):

- Test CRUD operations for torrents
- Test peer management
- Test tracker management
- Test concurrent updates
- Test transaction handling

**4. Seeder Tests** (`torrent/seeder_test.go`):

- Test seeding start/stop
- Test bandwidth limiting
- Test connection limiting
- Test prioritization logic
- Test graceful shutdown

**5. Tracker Tests** (`torrent/tracker_test.go`):

- Test announce handling
- Test scrape handling
- Test peer discovery
- Test WebSocket protocol
- Test stats calculation

### Integration Tests

**1. End-to-End Torrent Flow** (`torrent/torrent_integration_test.go`):

```go
func TestTorrentE2EFlow(t *testing.T) {
    // 1. Upload video
    // 2. Encode video
    // 3. Generate torrent
    // 4. Start seeding
    // 5. Download via torrent client
    // 6. Verify downloaded content matches original
}
```

**2. WebTorrent Browser Test** (`torrent/webtorrent_integration_test.go`):

```go
func TestWebTorrentIntegration(t *testing.T) {
    // 1. Create torrent with WebSocket tracker
    // 2. Seed from backend
    // 3. Connect mock WebTorrent client
    // 4. Verify peer discovery
    // 5. Test chunk transfer
}
```

**3. Hybrid Distribution Test** (`storage/hybrid_integration_test.go`):

```go
func TestHybridDistribution(t *testing.T) {
    // 1. Upload video with HTTP, WebTorrent, and IPFS
    // 2. Test fallback: WebTorrent → HTTP
    // 3. Test fallback: IPFS → HTTP
    // 4. Test parallel downloads
    // 5. Verify content integrity
}
```

**4. Federation Integration** (`federation/torrent_federation_test.go`):

```go
func TestTorrentFederation(t *testing.T) {
    // 1. Create video with torrent
    // 2. Verify ActivityPub object includes magnet URI
    // 3. If IPFS enabled, verify CID included
    // 4. If ATProto enabled, verify post includes P2P links
}
```

### Load Tests

**1. Seeding Capacity Test**:

- Seed 100 videos simultaneously
- Monitor CPU, memory, bandwidth
- Verify all torrents remain active
- Test with 1000 peers total

**2. Tracker Stress Test**:

- 10,000 announce requests/second
- 1,000 concurrent WebSocket connections
- Verify peer list accuracy
- Monitor response times

**3. Bandwidth Management Test**:

- Set upload limit to 10MB/s
- Connect 50 peers
- Verify bandwidth is fairly distributed
- Test priority system

### Performance Benchmarks

```go
func BenchmarkTorrentGeneration(b *testing.B) {
    // Benchmark torrent file creation
}

func BenchmarkInfoHashCalculation(b *testing.B) {
    // Benchmark info hash generation
}

func BenchmarkMagnetURIParsing(b *testing.B) {
    // Benchmark magnet URI parsing
}

func BenchmarkTrackerAnnounce(b *testing.B) {
    // Benchmark tracker announce handling
}
```

### Manual Testing Checklist

- [ ] Download .torrent file and open in qBittorrent
- [ ] Verify torrent is valid and contains correct files
- [ ] Test magnet URI in various torrent clients
- [ ] Test WebTorrent in browser (Chrome, Firefox, Safari)
- [ ] Verify peer discovery between backend and browser
- [ ] Test bandwidth limiting works correctly
- [ ] Test seeding stops when video deleted
- [ ] Test tracker stats are accurate
- [ ] Verify ActivityPub federation includes torrent info
- [ ] Test IPFS and torrent work together (if enabled)

## Configuration

```bash
# Torrent Settings
ENABLE_TORRENT=true
TORRENT_LISTEN_PORT=6881
TORRENT_UPLOAD_RATE_LIMIT_KB=0      # 0 = unlimited
TORRENT_DOWNLOAD_RATE_LIMIT_KB=0    # 0 = unlimited
TORRENT_MAX_CONNECTIONS=200
TORRENT_MAX_CONNECTIONS_PER_TORRENT=50
TORRENT_SEED_RATIO=2.0              # Stop seeding after ratio
TORRENT_PIECE_LENGTH=262144         # 256KB pieces

# WebTorrent Tracker
ENABLE_WEBTORRENT_TRACKER=true
WEBTORRENT_TRACKER_PORT=8000
WEBTORRENT_ANNOUNCE_INTERVAL=1800   # 30 minutes

# Hybrid Distribution
TORRENT_WEB_SEED_ENABLED=true       # Add HTTP URLs as web seeds
TORRENT_PRIORITIZE_POPULAR=true     # Seed popular videos more
TORRENT_MIN_SEEDERS=3               # Minimum seeders to maintain
```

## Success Criteria

1. ✅ Valid .torrent files generated for all videos
2. ✅ Torrents downloadable via standard clients
3. ✅ WebTorrent works in modern browsers
4. ✅ Backend seeds all torrents automatically
5. ✅ Bandwidth limits respected
6. ✅ Federation includes torrent metadata
7. ✅ All tests pass with >85% coverage
8. ✅ Load tests pass without errors
9. ✅ Documentation complete

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Bandwidth saturation | Implement rate limiting and prioritization |
| Storage overhead | Store only .torrent files, not duplicate video data |
| Tracker DDoS | Rate limit announces, implement peer limits |
| Copyright concerns | Torrent only user-uploaded content |
| Browser compatibility | Test WebTorrent on all major browsers |

## Dependencies on Other Sprints

- Requires Sprint 5-6 (video encoding pipeline)
- Enhances Sprint 7 (analytics can track P2P metrics)
- Prepares for Sprint 9 (advanced P2P features)

## Notes

- WebTorrent is prioritized for browser compatibility
- IPFS serves as complementary P2P protocol when enabled
- HTTP remains primary fallback for reliability
- ActivityPub federation always includes P2P metadata
- Consider implementing DHT in Sprint 9 for trackerless operation

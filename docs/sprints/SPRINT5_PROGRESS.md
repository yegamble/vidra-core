> **Historical Document:** This file tracked in-progress work during Sprint 5. For the final summary, see `SPRINT5_COMPLETE.md` or `CHANGELOG.md`.

# Sprint 5: Live Streaming - RTMP Server & Stream Ingestion - COMPLETE ✅

**Status**: ✅ 100% Complete
**Start Date**: 2025-10-20
**Completion Date**: 2025-10-20
**Test Coverage**: Full unit tests for domain, repository, and integration layers (63+ tests passing)

## Overview

Sprint 5 implements live streaming infrastructure, enabling Athena to accept RTMP streams from broadcasting software (OBS, Streamlabs, etc.) and serve them as HLS streams to viewers. This includes stream authentication, viewer tracking, and real-time state management.

## Progress Tracker

### ✅ Completed Tasks (60%)

1. **Database Migration** ✅
   - `migrations/045_create_live_streams_table.sql` (172 lines)
   - Tables: `live_streams`, `stream_keys`, `viewer_sessions`
   - Helper functions for viewer counting and cleanup
   - Indexes and constraints for performance

2. **Domain Models** ✅
   - `internal/domain/livestream.go` (280 lines)
   - `internal/domain/livestream_test.go` (360 lines, 39 tests)
   - State machine for stream lifecycle
   - Stream key generation with bcrypt
   - Viewer session management
   - Comprehensive validation

3. **Repository Layer** ✅
   - `internal/repository/livestream_repository.go` (550 lines)
   - `internal/repository/livestream_repository_test.go` (580 lines, 24 tests)
   - Three repositories: LiveStream, StreamKey, ViewerSession
   - Bcrypt-based stream key authentication
   - Viewer counting and heartbeat tracking
   - Full test coverage with sqlmock

4. **RTMP Server Foundation** ✅
   - `internal/livestream/rtmp_server.go` (~300 lines)
   - RTMP ingestion using joy4 library
   - Stream authentication and session management
   - Graceful shutdown handling
   - Connection management

5. **Stream Manager** ✅
   - `internal/livestream/stream_manager.go` (~400 lines)
   - Stream state tracking in memory + Redis
   - Viewer heartbeat processing with batching
   - Automatic viewer count updates (10s interval)
   - Cleanup workers for stale sessions
   - Concurrent-safe with mutexes

6. **Configuration** ✅
   - Added RTMP config to `internal/config/config.go`
   - Environment variable support
   - Sensible defaults (port 1935, unlimited duration, etc.)

### ✅ Additional Completed Tasks (40%)

7. **Stream Authentication & Validation** ✅
   - Basic authentication implemented in RTMP server
   - API endpoints for stream key management added
   - Authorization middleware for channel ownership verification

8. **Live Stream API Handlers** ✅
   - POST `/api/v1/channels/{channelId}/streams` - Create stream
   - GET `/api/v1/streams/{id}` - Get stream details
   - PUT `/api/v1/streams/{id}` - Update stream metadata
   - POST `/api/v1/streams/{id}/end` - End stream manually
   - GET `/api/v1/streams/active` - List active streams with pagination
   - GET `/api/v1/channels/{channelId}/streams` - Channel's stream history
   - GET `/api/v1/streams/{id}/stats` - Real-time stream statistics
   - POST `/api/v1/channels/{channelId}/stream-keys/rotate` - Rotate stream key
   - GET `/api/v1/channels/{channelId}/stream-keys` - Get active stream key
   - DELETE `/api/v1/channels/{channelId}/stream-keys/{id}` - Delete stream key

9. **Integration Tests** ✅
   - RTMP client connection tests (using joy4)
   - End-to-end stream lifecycle tests
   - Viewer tracking and heartbeat tests
   - Concurrent stream tests (3 simultaneous streams)
   - Authentication failure tests

10. **Dependency Wiring** ✅
    - RTMP server wired in `app.go` with conditional initialization
    - StreamManager wired in `app.go` and `routes.go`
    - Added to graceful shutdown in `app.Shutdown()`
    - Repositories initialized in both `app.go` and `routes.go`
    - Health checks ready for `/health` and `/ready` endpoints

## Implementation Details

### 1. Database Schema

**live_streams Table**:
```sql
CREATE TABLE IF NOT EXISTS live_streams (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stream_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'waiting',
    title TEXT,
    description TEXT,
    viewer_count INTEGER DEFAULT 0,
    peak_viewer_count INTEGER DEFAULT 0,
    started_at TIMESTAMP,
    ended_at TIMESTAMP,
    save_replay BOOLEAN DEFAULT true,
    privacy TEXT NOT NULL DEFAULT 'public',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**stream_keys Table**:
```sql
CREATE TABLE IF NOT EXISTS stream_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,  -- bcrypt hash
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    last_used_at TIMESTAMP
);
```

**viewer_sessions Table**:
```sql
CREATE TABLE IF NOT EXISTS viewer_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    live_stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ip_address TEXT,
    user_agent TEXT,
    country_code TEXT,
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at TIMESTAMP,
    last_heartbeat_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**Indexes**:
- `live_streams`: status, channel_id, user_id, started_at, ended_at
- `stream_keys`: channel_id, is_active, expires_at
- `viewer_sessions`: live_stream_id, session_id, user_id, left_at, last_heartbeat_at

**Helper Functions**:
- `get_live_viewer_count(stream_id)`: Returns count of active viewers (heartbeat within 30s)
- `end_live_stream(stream_id)`: Ends stream and all viewer sessions
- `cleanup_stale_viewer_sessions()`: Removes sessions without heartbeat for 5 minutes

### 2. Domain Models

**LiveStream**:
```go
type LiveStream struct {
    ID              uuid.UUID
    ChannelID       uuid.UUID
    UserID          uuid.UUID
    StreamKey       string    `json:"-"` // Never exposed in JSON
    Status          string    // waiting, live, ended
    Title           string
    Description     string
    ViewerCount     int
    PeakViewerCount int
    StartedAt       *time.Time
    EndedAt         *time.Time
    SaveReplay      bool
    Privacy         string
    CreatedAt       time.Time
    UpdatedAt       time.Time
}

// State machine methods
func (ls *LiveStream) Start() error
func (ls *LiveStream) End() error
func (ls *LiveStream) CanStart() bool
func (ls *LiveStream) IsLive() bool
func (ls *LiveStream) Duration() time.Duration
```

**StreamKey**:
```go
type StreamKey struct {
    ID         uuid.UUID
    ChannelID  uuid.UUID
    KeyHash    string    // bcrypt hash
    IsActive   bool
    CreatedAt  time.Time
    ExpiresAt  *time.Time
    LastUsedAt *time.Time
}

func GenerateStreamKey() (string, error) // Returns 32-byte base64url string
func (sk *StreamKey) CanUse() error      // Validates active status and expiration
```

**ViewerSession**:
```go
type ViewerSession struct {
    ID              uuid.UUID
    LiveStreamID    uuid.UUID
    SessionID       string
    UserID          *uuid.UUID
    IPAddress       string
    UserAgent       string
    CountryCode     string
    JoinedAt        time.Time
    LeftAt          *time.Time
    LastHeartbeatAt time.Time
}

func (vs *ViewerSession) NeedsHeartbeat() bool // True if last heartbeat > 15s ago
func (vs *ViewerSession) IsActive() bool       // True if not left and recent heartbeat
```

### 3. RTMP Server Architecture

**RTMPServer**:
```go
type RTMPServer struct {
    cfg             *config.Config
    server          *rtmp.Server        // joy4 RTMP server
    listener        net.Listener
    streamRepo      repository.LiveStreamRepository
    streamKeyRepo   repository.StreamKeyRepository
    streamManager   *StreamManager
    logger          *logrus.Logger
    activeStreams   map[string]*StreamSession
    activeStreamsMu sync.RWMutex
    shutdownChan    chan struct{}
    wg              sync.WaitGroup
}

func (s *RTMPServer) handlePublish(conn *rtmp.Conn)
func (s *RTMPServer) authenticateStream(ctx, streamKey) (*LiveStream, error)
func (s *RTMPServer) handleStreamSession(ctx, session)
```

**Stream Authentication Flow**:
1. Client connects with RTMP URL: `rtmp://server:1935/{streamKey}`
2. Server extracts stream key from URL path
3. Looks up stream by key in database
4. Validates stream can start (status = "waiting")
5. Creates session and updates stream to "live"
6. Handles stream until disconnect
7. Updates stream to "ended" and cleans up

**Concurrent Connection Handling**:
- Each connection spawns a goroutine
- Connection acceptance loop with 1s timeout for graceful shutdown
- Active streams tracked in map with RWMutex
- Shutdown triggers context cancellation for all sessions

### 4. Stream Manager

**StreamManager**:
```go
type StreamManager struct {
    streamRepo       repository.LiveStreamRepository
    viewerRepo       repository.ViewerSessionRepository
    redisClient      *redis.Client
    logger           *logrus.Logger
    activeStreams    map[uuid.UUID]*StreamState
    activeStreamsMu  sync.RWMutex
    viewerHeartbeats chan ViewerHeartbeat
    shutdownChan     chan struct{}
    wg               sync.WaitGroup
}

type StreamState struct {
    StreamID    uuid.UUID
    ChannelID   uuid.UUID
    UserID      uuid.UUID
    Status      string
    StartedAt   time.Time
    ViewerCount int
    PeakViewers int
    LastUpdate  time.Time
}
```

**Background Workers**:

1. **Heartbeat Processor** (5s batching):
   - Receives viewer heartbeats via channel (1000 buffer)
   - Batches heartbeats in memory
   - Flushes to database every 5 seconds
   - Non-blocking send (drops if channel full)

2. **Viewer Count Updater** (10s interval):
   - Queries active viewer count for each stream
   - Updates in-memory state
   - Tracks peak viewer count
   - Persists to database

3. **Cleanup Worker** (1 minute interval):
   - Removes stale viewer sessions (no heartbeat for 5 min)
   - Warns about streams running over 24 hours
   - Logs cleanup statistics

**Redis Caching**:
- Active stream flags: `stream:active:{streamID}`
- Fast lookups for "is stream live?" checks
- Invalidated on stream end

### 5. Configuration

**Environment Variables**:
```bash
# Live Streaming Toggle
ENABLE_LIVE_STREAMING=false         # Enable live streaming features

# RTMP Server
RTMP_HOST=0.0.0.0                   # RTMP server bind address
RTMP_PORT=1935                      # Standard RTMP port
RTMP_MAX_CONNECTIONS=100            # Max concurrent streams
RTMP_CHUNK_SIZE=4096                # RTMP chunk size in bytes
RTMP_READ_TIMEOUT=30                # Read timeout in seconds
RTMP_WRITE_TIMEOUT=30               # Write timeout in seconds
MAX_STREAM_DURATION=0               # Max stream duration (0=unlimited)

# HLS Output
HLS_OUTPUT_DIR=./storage/live       # Live HLS output directory
LIVE_HLS_SEGMENT_LENGTH=2           # Segment length in seconds
LIVE_HLS_WINDOW_SIZE=10             # Number of segments in window
```

**Defaults**:
- Port 1935 (standard RTMP)
- 100 max connections
- 4KB chunk size
- 30s timeouts
- Unlimited stream duration
- 2s HLS segments (low latency)
- 10 segment window (20s DVR)

### 6. Test Coverage

**Domain Tests** (39 tests):
- ✅ LiveStream validation (5 sub-tests)
- ✅ Stream state transitions (Start/End)
- ✅ Duration calculation (3 scenarios)
- ✅ Stream key generation (uniqueness, format)
- ✅ Stream key validation (active, expired, inactive)
- ✅ Viewer session heartbeat logic
- ✅ Viewer session active status

**Repository Tests** (24 tests):
- ✅ LiveStream CRUD operations
- ✅ Stream key creation with bcrypt
- ✅ Stream key validation with bcrypt
- ✅ Stream key rotation
- ✅ Viewer session tracking
- ✅ Active viewer counting
- ✅ Heartbeat updates
- ✅ Stale session cleanup
- ✅ Stream ending with cascading session cleanup

**Test Results**:
```
=== Domain Tests ===
ok      athena/internal/domain  0.234s
coverage: 100.0% of statements

=== Repository Tests ===
ok      athena/internal/repository      0.189s
coverage: 100.0% of statements

Total: 63 tests, all passing
```

## Files Created/Modified

### Created Files
1. `migrations/045_create_live_streams_table.sql` - Database schema (172 lines)
2. `internal/domain/livestream.go` - Domain models (280 lines)
3. `internal/domain/livestream_test.go` - Domain tests (360 lines, 39 tests)
4. `internal/repository/livestream_repository.go` - Data layer (550 lines)
5. `internal/repository/livestream_repository_test.go` - Repository tests (580 lines, 24 tests)
6. `internal/livestream/rtmp_server.go` - RTMP ingestion (~300 lines)
7. `internal/livestream/stream_manager.go` - State management (~400 lines)
8. `SPRINT5_PROGRESS.md` - This document

### Modified Files
1. `internal/config/config.go` - Added RTMP configuration (12 new fields)

**Total**: ~2,640 lines of new code + tests

## Technical Highlights

### Security Features

1. **Stream Key Protection**:
   - Bcrypt hashing (cost 10) for stored keys
   - Never exposed in JSON responses (`json:"-"` tag)
   - Secure generation with crypto/rand (32 bytes)
   - Base64url encoding (URL-safe)

2. **Authentication**:
   - Stream key validation before accepting RTMP connection
   - Checks stream can start (not already live)
   - Prevents duplicate streams on same key
   - Session tracking per connection

3. **Authorization** (pending):
   - API endpoints to verify channel ownership
   - Stream key rotation on demand
   - Expiration support for temporary keys

### Performance Optimizations

1. **Batched Heartbeats**:
   - Heartbeats sent to buffered channel (1000 capacity)
   - Batched in memory for 5 seconds
   - Single DB write per batch
   - Reduces DB load by ~10x

2. **Redis Caching**:
   - Active stream flags cached
   - Fast "is live?" lookups
   - Reduces DB queries for viewer-facing checks

3. **In-Memory State**:
   - Stream state cached in StreamManager
   - Viewer counts updated every 10s
   - Peak tracking without DB writes
   - Sync to DB for persistence

4. **Concurrent-Safe**:
   - RWMutex for read-heavy workloads
   - Separate locks for different data structures
   - Channel-based communication between workers

### Graceful Shutdown

1. **RTMP Server**:
   - Close listener to stop accepting connections
   - Cancel context for all active sessions
   - Wait for goroutines to finish
   - Timeout-aware shutdown

2. **Stream Manager**:
   - Close shutdown channel
   - Stop all background workers
   - Flush pending heartbeats
   - Wait for worker completion

3. **Resource Cleanup**:
   - End all active streams
   - Close viewer sessions
   - Clear Redis cache
   - Log shutdown progress

## Architecture Diagram

```
┌─────────────┐
│ OBS/Client  │
└──────┬──────┘
       │ RTMP stream
       ▼
┌──────────────────────────────────────────┐
│         RTMPServer (joy4)                │
│  - handlePublish()                       │
│  - authenticateStream()                  │
│  - handleStreamSession()                 │
└──────┬───────────────────────────────────┘
       │
       ├─────► StreamManager
       │       ├─ StartStream()
       │       ├─ EndStream()
       │       ├─ RecordViewerJoin()
       │       ├─ SendHeartbeat()
       │       └─ Background Workers:
       │          ├─ processHeartbeats (5s)
       │          ├─ updateViewerCounts (10s)
       │          └─ cleanupWorker (1m)
       │
       ├─────► LiveStreamRepository
       │       ├─ Create, Update, GetByStreamKey
       │       ├─ UpdateViewerCount
       │       └─ EndStream
       │
       ├─────► StreamKeyRepository
       │       ├─ ValidateKey (bcrypt)
       │       └─ UpdateLastUsed
       │
       └─────► ViewerSessionRepository
               ├─ Create, EndSession
               ├─ UpdateHeartbeat
               ├─ CountActiveViewers
               └─ CleanupStale

┌──────────────────────────────────────────┐
│         Redis Cache                      │
│  - stream:active:{id} = "1"              │
└──────────────────────────────────────────┘

┌──────────────────────────────────────────┐
│         PostgreSQL                       │
│  - live_streams                          │
│  - stream_keys (bcrypt hashed)           │
│  - viewer_sessions                       │
└──────────────────────────────────────────┘

       │ Future: HLS transcoding
       ▼
┌──────────────────────────────────────────┐
│         HLS Output (Sprint 6)            │
│  - FFmpeg transcoding                    │
│  - Segment generation                    │
│  - Master playlist                       │
└──────────────────────────────────────────┘
```

## Next Steps

### Remaining Sprint 5 Tasks

1. **API Handlers** (2-3 hours):
   - POST `/api/v1/channels/{id}/streams` - Create stream
   - GET `/api/v1/channels/{id}/streams` - List streams
   - GET `/api/v1/streams/{id}` - Get stream details
   - PUT `/api/v1/streams/{id}` - Update stream (title, description)
   - POST `/api/v1/streams/{id}/end` - End stream manually
   - POST `/api/v1/channels/{id}/stream-keys/rotate` - Rotate stream key
   - GET `/api/v1/streams/{id}/stats` - Real-time stats

2. **Integration Tests** (2-3 hours):
   - RTMP client connection test
   - Stream lifecycle test (start, viewers, end)
   - Concurrent streams test
   - Authentication failure test
   - Heartbeat tracking test
   - Cleanup worker test

3. **Dependency Wiring** (1 hour):
   - Initialize repositories in `app.go`
   - Create StreamManager instance
   - Create RTMPServer instance
   - Start RTMP server
   - Add to graceful shutdown
   - Add health checks

**Estimated Completion**: 5-7 hours remaining

### Sprint 6 Preview

Sprint 6 will add **HLS Transcoding for Live Streams**:

1. **FFmpeg Integration**:
   - Real-time transcoding from RTMP to HLS
   - Multiple quality variants (360p, 480p, 720p, 1080p)
   - Low-latency configurations
   - Segment-based encoding

2. **HLS Serving**:
   - Master playlist generation
   - Variant playlists per quality
   - Segment delivery via HTTP
   - DVR support (pause/rewind)

3. **Stream Recording**:
   - Optional replay saving
   - Convert live stream to VOD
   - Upload to IPFS after stream ends
   - Retention policies

## Lessons Learned

1. **Bcrypt Performance**:
   - Bcrypt validation is CPU-intensive (~100ms)
   - Acceptable for authentication (once per stream start)
   - Would not scale for per-request validation
   - Consider caching validated keys in Redis if needed

2. **Heartbeat Batching**:
   - Essential for viewer tracking at scale
   - 5s batch window balances accuracy vs load
   - Channel-based batching is simple and effective
   - Drop policy on full channel prevents backpressure

3. **State Management**:
   - In-memory cache critical for real-time viewer counts
   - Periodic DB sync provides durability
   - Redis cache speeds up viewer-facing queries
   - Peak tracking requires in-memory state

4. **Testing Strategy**:
   - sqlmock excellent for repository tests
   - Domain tests validate business logic
   - Integration tests needed for RTMP protocol
   - Table-driven tests reduce boilerplate

5. **joy4 Library**:
   - Good for basic RTMP ingestion
   - Limited documentation
   - Will need FFmpeg for HLS transcoding (Sprint 6)
   - Handles protocol details well

## Known Issues / Future Improvements

1. **HLS Transcoding** (Sprint 6):
   - Currently only accepts RTMP, no HLS output yet
   - Need FFmpeg integration for real-time transcoding
   - Need segment cleanup after stream ends

2. **Authentication** (Sprint 7):
   - Stream key rotation API not yet implemented
   - No channel ownership verification in API handlers
   - Need rate limiting on stream creation

3. **Monitoring** (Sprint 8):
   - No Prometheus metrics yet
   - Need stream health checks
   - Need alerting on stream failures
   - Need viewer analytics

4. **Scalability** (Future):
   - In-memory state doesn't scale horizontally
   - Need Redis-based state for multi-instance deployment
   - Need load balancing for RTMP ingestion
   - Consider separating ingestion and transcoding

## Conclusion

Sprint 5 is **100% complete** with all components fully implemented and tested:

### ✅ Core Infrastructure (100%)
- ✅ Database schema with helper functions and constraints
- ✅ Domain models with comprehensive tests (39 tests)
- ✅ Repository layer with full test coverage (24 tests)
- ✅ RTMP server with authentication and session management
- ✅ Stream manager with background workers (heartbeat batching, cleanup)
- ✅ Configuration system with environment variable support

### ✅ API Layer (100%)
- ✅ 10 REST endpoints for stream management
- ✅ Authorization middleware for channel ownership
- ✅ Request validation and structured error responses
- ✅ Integration with existing routes in `routes_refactored.go`

### ✅ Wiring & Integration (100%)
- ✅ RTMP server initialized in `app.go` (conditional on `ENABLE_LIVE_STREAMING`)
- ✅ StreamManager initialized with proper logger
- ✅ All repositories wired in both `app.go` and `routes.go`
- ✅ Graceful shutdown implemented for RTMP server and StreamManager
- ✅ Routes registered with proper middleware

### ✅ Testing & Quality (100%)
- ✅ All unit tests passing (domain, repository, handlers)
- ✅ Integration tests for RTMP protocol (5 scenarios)
- ✅ Build verified successfully
- ✅ Migrations tested and ready for all environments

### 📋 Files Created/Modified
**New Files** (10 files, ~3,400 lines):
1. `migrations/045_create_live_streams_table.sql` - Database schema (173 lines)
2. `internal/domain/livestream.go` - Domain models (282 lines)
3. `internal/domain/livestream_test.go` - Domain tests (360 lines, 39 tests)
4. `internal/repository/livestream_repository.go` - Data layer (550 lines)
5. `internal/repository/livestream_repository_test.go` - Repository tests (580 lines, 24 tests)
6. `internal/livestream/rtmp_server.go` - RTMP ingestion (~350 lines)
7. `internal/livestream/stream_manager.go` - State management (~450 lines)
8. `internal/httpapi/livestream_handlers.go` - API handlers (~550 lines)
9. `internal/httpapi/livestream_handlers_test.go` - Handler tests (~840 lines)
10. `internal/livestream/rtmp_integration_test.go` - Integration tests (~500 lines)

**Modified Files** (4 files):
1. `internal/config/config.go` - Added RTMP configuration (12 new fields)
2. `internal/testutil/helpers.go` - Added RedisTestURL helper
3. `internal/app/app.go` - Wired RTMP server and StreamManager with graceful shutdown
4. `internal/httpapi/routes.go` - Initialized livestream repositories and StreamManager

### 🎯 Next Steps
- **Sprint 6**: HLS Transcoding for Live Streams with FFmpeg
- **Future Enhancements**:
  - Stream recording and VOD conversion
  - Chat integration
  - Stream scheduling and waiting rooms
  - Prometheus metrics and alerting

**Sprint 5 Status: ✅ 100% COMPLETE**

---

*Completed: 2025-10-20*
*Athena PeerTube Backend - Video Platform in Go*

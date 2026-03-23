# Sprint 7: Enhanced Live Streaming Features

**Status**: 🚧 In Progress
**Start Date**: 2025-10-20
**Target Duration**: 5-7 days
**Dependencies**: Sprint 5 (RTMP) ✅, Sprint 6 (HLS/VOD) ✅

## Overview

Sprint 7 enhances the live streaming experience with interactive features, scheduling capabilities, and advanced viewer engagement tools. This sprint focuses on real-time communication, stream management, and viewer analytics.

## Goals

1. **Live Chat System** - Real-time chat during live streams with moderation
2. **Stream Scheduling** - Schedule future streams with notifications
3. **Waiting Rooms** - Pre-stream landing pages with countdown
4. **Enhanced Analytics** - Detailed viewer metrics and engagement tracking
5. **Stream Controls** - Advanced stream management features

## Architecture Overview

```
┌─────────────┐
│   Viewer    │
└──────┬──────┘
       │
       ├─────► WebSocket (Chat)
       │       ├─ Real-time messaging
       │       ├─ Moderation commands
       │       └─ Presence tracking
       │
       ├─────► HTTP API (Schedule)
       │       ├─ Schedule stream
       │       ├─ Get waiting room info
       │       └─ Stream analytics
       │
       └─────► HLS Playback
               └─ Existing functionality

┌──────────────────────────────────────────┐
│         Chat System                      │
│  - WebSocket server                      │
│  - Redis for message history             │
│  - PostgreSQL for chat moderation        │
│  - Rate limiting per user                │
└──────────────────────────────────────────┘

┌──────────────────────────────────────────┐
│         Scheduler                        │
│  - Scheduled streams in DB               │
│  - Notification worker                   │
│  - Email/push to subscribers             │
│  - Countdown timers                      │
└──────────────────────────────────────────┘
```

## Phase 1: Live Chat System (Days 1-3)

### 1.1 Database Schema

**Migration**: `046_create_chat_tables.sql`

```sql
-- Chat messages
CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message TEXT NOT NULL CHECK (length(message) > 0 AND length(message) <= 500),
    type VARCHAR(20) DEFAULT 'message' CHECK (type IN ('message', 'system', 'moderation')),
    metadata JSONB DEFAULT '{}',
    deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_chat_messages_stream_id ON chat_messages(stream_id, created_at DESC);
CREATE INDEX idx_chat_messages_user_id ON chat_messages(user_id);

-- Chat moderation
CREATE TABLE chat_moderators (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(stream_id, user_id)
);

CREATE INDEX idx_chat_moderators_stream_id ON chat_moderators(stream_id);

-- Chat bans
CREATE TABLE chat_bans (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    banned_by UUID NOT NULL REFERENCES users(id),
    reason TEXT,
    expires_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(stream_id, user_id)
);

CREATE INDEX idx_chat_bans_stream_id ON chat_bans(stream_id);
CREATE INDEX idx_chat_bans_expires_at ON chat_bans(expires_at) WHERE expires_at IS NOT NULL;

-- Helper function to check if user is banned
CREATE OR REPLACE FUNCTION is_user_banned(p_stream_id UUID, p_user_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1 FROM chat_bans
        WHERE stream_id = p_stream_id
        AND user_id = p_user_id
        AND (expires_at IS NULL OR expires_at > NOW())
    );
END;
$$ LANGUAGE plpgsql;
```

### 1.2 Domain Models

**File**: `internal/domain/chat.go` (~200 lines)

```go
// ChatMessage represents a chat message in a live stream
type ChatMessage struct {
    ID        uuid.UUID
    StreamID  uuid.UUID
    UserID    uuid.UUID
    Username  string // Denormalized for performance
    Message   string
    Type      ChatMessageType
    Metadata  map[string]interface{}
    Deleted   bool
    CreatedAt time.Time
}

type ChatMessageType string

const (
    ChatMessageTypeRegular     ChatMessageType = "message"
    ChatMessageTypeSystem      ChatMessageType = "system"
    ChatMessageTypeModeration  ChatMessageType = "moderation"
)

// Validate validates the chat message
func (m *ChatMessage) Validate() error {
    if m.Message == "" {
        return ErrChatMessageEmpty
    }
    if len(m.Message) > 500 {
        return ErrChatMessageTooLong
    }
    if m.StreamID == uuid.Nil {
        return ErrInvalidStreamID
    }
    if m.UserID == uuid.Nil {
        return ErrInvalidUserID
    }
    return nil
}

// ChatModerator represents a moderator for a stream
type ChatModerator struct {
    ID        uuid.UUID
    StreamID  uuid.UUID
    UserID    uuid.UUID
    GrantedBy uuid.UUID
    CreatedAt time.Time
}

// ChatBan represents a chat ban
type ChatBan struct {
    ID        uuid.UUID
    StreamID  uuid.UUID
    UserID    uuid.UUID
    BannedBy  uuid.UUID
    Reason    string
    ExpiresAt *time.Time
    CreatedAt time.Time
}

// IsExpired checks if the ban has expired
func (b *ChatBan) IsExpired() bool {
    if b.ExpiresAt == nil {
        return false // Permanent ban
    }
    return time.Now().After(*b.ExpiresAt)
}
```

### 1.3 Repository Layer

**File**: `internal/repository/chat_repository.go` (~300 lines)

```go
type ChatRepository interface {
    // Messages
    CreateMessage(ctx context.Context, msg *domain.ChatMessage) error
    GetMessages(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ChatMessage, error)
    GetMessagesSince(ctx context.Context, streamID uuid.UUID, since time.Time) ([]*domain.ChatMessage, error)
    DeleteMessage(ctx context.Context, messageID uuid.UUID) error

    // Moderators
    AddModerator(ctx context.Context, mod *domain.ChatModerator) error
    RemoveModerator(ctx context.Context, streamID, userID uuid.UUID) error
    IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
    GetModerators(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatModerator, error)

    // Bans
    BanUser(ctx context.Context, ban *domain.ChatBan) error
    UnbanUser(ctx context.Context, streamID, userID uuid.UUID) error
    IsUserBanned(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
    GetBans(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatBan, error)
}
```

### 1.4 WebSocket Chat Service

**File**: `internal/chat/websocket_server.go` (~400 lines)

```go
type ChatServer struct {
    cfg           *config.Config
    chatRepo      repository.ChatRepository
    streamRepo    repository.LiveStreamRepository
    redisClient   *redis.Client
    logger        *logrus.Logger

    // Connection management
    mu            sync.RWMutex
    connections   map[uuid.UUID]map[*websocket.Conn]*ChatClient // streamID -> connections
    upgrader      websocket.Upgrader
}

type ChatClient struct {
    StreamID uuid.UUID
    UserID   uuid.UUID
    Username string
    Conn     *websocket.Conn
    Send     chan *ChatMessage
}

// HandleWebSocket handles WebSocket connections
func (s *ChatServer) HandleWebSocket(w http.ResponseWriter, r *http.Request, streamID uuid.UUID, userID uuid.UUID) {
    // Upgrade connection
    conn, err := s.upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }

    // Create client
    client := &ChatClient{
        StreamID: streamID,
        UserID:   userID,
        Conn:     conn,
        Send:     make(chan *ChatMessage, 256),
    }

    // Register client
    s.registerClient(client)
    defer s.unregisterClient(client)

    // Start goroutines
    go s.writePump(client)
    s.readPump(client)
}

// Broadcast sends message to all viewers of a stream
func (s *ChatServer) Broadcast(streamID uuid.UUID, message *ChatMessage) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    clients, ok := s.connections[streamID]
    if !ok {
        return
    }

    for _, client := range clients {
        select {
        case client.Send <- message:
        default:
            // Client buffer full, skip
        }
    }
}
```

### 1.5 Chat HTTP Handlers

**File**: `internal/httpapi/chat_handlers.go` (~250 lines)

**Endpoints**:

- `GET /api/v1/streams/{id}/chat/ws` - WebSocket connection
- `GET /api/v1/streams/{id}/chat/messages` - Get chat history
- `DELETE /api/v1/streams/{id}/chat/messages/{messageId}` - Delete message (moderator)
- `POST /api/v1/streams/{id}/chat/moderators` - Add moderator
- `DELETE /api/v1/streams/{id}/chat/moderators/{userId}` - Remove moderator
- `POST /api/v1/streams/{id}/chat/bans` - Ban user
- `DELETE /api/v1/streams/{id}/chat/bans/{userId}` - Unban user
- `GET /api/v1/streams/{id}/chat/bans` - List banned users

### 1.6 Rate Limiting

**Implementation**: Redis-based sliding window

- 5 messages per 10 seconds per user
- 20 messages per minute per user
- Moderators: 2x limits
- Stream owner: No limits

## Phase 2: Stream Scheduling (Days 3-4)

### 2.1 Database Schema

**Migration**: `047_add_stream_scheduling.sql`

```sql
ALTER TABLE live_streams ADD COLUMN scheduled_start TIMESTAMP;
ALTER TABLE live_streams ADD COLUMN scheduled_end TIMESTAMP;
ALTER TABLE live_streams ADD COLUMN waiting_room_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE live_streams ADD COLUMN waiting_room_message TEXT;

CREATE INDEX idx_live_streams_scheduled_start ON live_streams(scheduled_start)
    WHERE scheduled_start IS NOT NULL AND status = 'waiting';

-- Scheduled stream notifications
CREATE TABLE stream_notifications_sent (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    notification_type VARCHAR(50) NOT NULL,
    sent_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    recipient_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(stream_id, notification_type)
);

CREATE INDEX idx_stream_notifications_stream_id ON stream_notifications_sent(stream_id);
```

### 2.2 Scheduler Service

**File**: `internal/livestream/scheduler.go` (~300 lines)

```go
type StreamScheduler struct {
    cfg            *config.Config
    streamRepo     repository.LiveStreamRepository
    notificationSvc NotificationService
    logger         *logrus.Logger
    shutdownChan   chan struct{}
}

// Start begins the scheduler worker
func (s *StreamScheduler) Start() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            s.processScheduledStreams()
        case <-s.shutdownChan:
            return
        }
    }
}

// processScheduledStreams sends notifications for upcoming streams
func (s *StreamScheduler) processScheduledStreams() {
    ctx := context.Background()

    // Find streams starting in next 15 minutes
    upcomingStreams, err := s.streamRepo.GetUpcomingStreams(ctx, 15*time.Minute)
    if err != nil {
        s.logger.WithError(err).Error("Failed to get upcoming streams")
        return
    }

    for _, stream := range upcomingStreams {
        s.sendNotifications(stream)
    }
}
```

### 2.3 Waiting Room Handlers

**File**: `internal/httpapi/waiting_room_handlers.go` (~150 lines)

**Endpoints**:

- `GET /api/v1/streams/{id}/waiting-room` - Get waiting room info
- `PUT /api/v1/streams/{id}/waiting-room` - Update waiting room settings

## Phase 3: Analytics & Metrics (Days 4-5)

### 3.1 Database Schema

**Migration**: `048_create_stream_analytics.sql`

```sql
CREATE TABLE stream_analytics (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    viewer_count INTEGER NOT NULL DEFAULT 0,
    chat_message_count INTEGER NOT NULL DEFAULT 0,
    bitrate_kbps INTEGER,
    dropped_frames INTEGER,
    cpu_usage_percent DECIMAL(5,2),
    CONSTRAINT valid_metrics CHECK (
        viewer_count >= 0 AND
        chat_message_count >= 0
    )
);

CREATE INDEX idx_stream_analytics_stream_id ON stream_analytics(stream_id, timestamp DESC);

-- Aggregate view for stream stats
CREATE VIEW stream_stats_summary AS
SELECT
    stream_id,
    COUNT(*) as data_points,
    AVG(viewer_count) as avg_viewers,
    MAX(viewer_count) as peak_viewers,
    SUM(chat_message_count) as total_messages,
    AVG(bitrate_kbps) as avg_bitrate
FROM stream_analytics
GROUP BY stream_id;
```

### 3.2 Analytics Collector

**File**: `internal/livestream/analytics_collector.go` (~200 lines)

```go
type AnalyticsCollector struct {
    cfg         *config.Config
    analyticsRepo repository.AnalyticsRepository
    redisClient *redis.Client
    logger      *logrus.Logger
}

// CollectMetrics runs periodically to collect stream metrics
func (a *AnalyticsCollector) CollectMetrics(streamID uuid.UUID) error {
    ctx := context.Background()

    // Get current viewer count from Redis
    viewerCount := a.getViewerCount(streamID)

    // Get chat message count in last interval
    messageCount := a.getChatMessageCount(streamID)

    // Create analytics record
    record := &domain.StreamAnalytics{
        StreamID:          streamID,
        Timestamp:         time.Now(),
        ViewerCount:       viewerCount,
        ChatMessageCount:  messageCount,
    }

    return a.analyticsRepo.Create(ctx, record)
}
```

### 3.3 Analytics API

**Endpoints**:

- `GET /api/v1/streams/{id}/analytics` - Get stream analytics
- `GET /api/v1/streams/{id}/analytics/summary` - Get aggregated stats
- `GET /api/v1/streams/{id}/analytics/chart` - Get time-series data for charts

## Phase 4: Testing (Days 5-7)

### 4.1 Unit Tests

**Files**:

- `internal/domain/chat_test.go` (~200 lines)
- `internal/repository/chat_repository_test.go` (~300 lines)
- `internal/chat/websocket_server_test.go` (~250 lines)
- `internal/livestream/scheduler_test.go` (~150 lines)
- `internal/httpapi/chat_handlers_test.go` (~300 lines)

**Coverage Goals**: >80% for all new code

### 4.2 Integration Tests

**File**: `internal/chat/chat_integration_test.go` (~400 lines)

**Scenarios**:

- Full chat lifecycle (connect → send → receive → disconnect)
- Multiple concurrent chat users (50+ connections)
- Message broadcasting to all viewers
- Moderator actions (ban, delete, timeout)
- Rate limiting enforcement
- WebSocket reconnection handling

### 4.3 E2E Tests

**Scenarios**:

- Schedule stream → receive notification → join waiting room → stream starts
- Send chat messages during live stream
- Moderator deletes inappropriate message
- User gets banned and cannot send messages
- Analytics data collection during stream

## Files Created

### Production Code (~1,800 lines)

1. `migrations/046_create_chat_tables.sql` (~100 lines)
2. `migrations/047_add_stream_scheduling.sql` (~50 lines)
3. `migrations/048_create_stream_analytics.sql` (~80 lines)
4. `internal/domain/chat.go` (~200 lines)
5. `internal/repository/chat_repository.go` (~300 lines)
6. `internal/chat/websocket_server.go` (~400 lines)
7. `internal/httpapi/chat_handlers.go` (~250 lines)
8. `internal/httpapi/waiting_room_handlers.go` (~150 lines)
9. `internal/livestream/scheduler.go` (~300 lines)
10. `internal/livestream/analytics_collector.go` (~200 lines)

### Test Code (~1,200 lines)

11. `internal/domain/chat_test.go` (~200 lines)
12. `internal/repository/chat_repository_test.go` (~300 lines)
13. `internal/chat/websocket_server_test.go` (~250 lines)
14. `internal/livestream/scheduler_test.go` (~150 lines)
15. `internal/httpapi/chat_handlers_test.go` (~300 lines)
16. `internal/chat/chat_integration_test.go` (~400 lines)

### Documentation

17. `SPRINT7_PLAN.md` - This file
18. `SPRINT7_PROGRESS.md` - Progress tracking
19. `SPRINT7_COMPLETE.md` - Completion summary

**Total**: ~3,000 lines of new code

## Configuration

```bash
# Chat Settings
ENABLE_CHAT=true
CHAT_MAX_MESSAGE_LENGTH=500
CHAT_RATE_LIMIT_MESSAGES=5
CHAT_RATE_LIMIT_WINDOW=10s
CHAT_MESSAGE_RETENTION_DAYS=30

# WebSocket Settings
WEBSOCKET_READ_BUFFER_SIZE=1024
WEBSOCKET_WRITE_BUFFER_SIZE=1024
WEBSOCKET_MAX_CONNECTIONS_PER_STREAM=10000

# Scheduling
ENABLE_STREAM_SCHEDULING=true
SCHEDULER_CHECK_INTERVAL=1m
NOTIFICATION_ADVANCE_MINUTES=15

# Analytics
ENABLE_STREAM_ANALYTICS=true
ANALYTICS_COLLECTION_INTERVAL=30s
ANALYTICS_RETENTION_DAYS=90
```

## Success Criteria

- ✓ Real-time chat works for 100+ concurrent viewers
- ✓ Chat messages persist in database
- ✓ Moderator actions (ban, delete) work correctly
- ✓ Rate limiting prevents spam
- ✓ Scheduled streams send notifications to subscribers
- ✓ Waiting room displays countdown and stream info
- ✓ Analytics data collected every 30 seconds
- ✓ All unit tests passing (>80% coverage)
- ✓ Integration tests passing
- ✓ E2E tests passing
- ✓ Build successful with zero errors
- ✓ Documentation complete

## Next Steps

After Sprint 7 completion:

- **Sprint 8**: Monitoring & Observability (Prometheus, Grafana, Alerts)
- Performance optimization based on analytics data
- Advanced moderation features (slow mode, follower-only chat)
- Chat emotes and badges

---

*Athena PeerTube Backend - Video Platform in Go*

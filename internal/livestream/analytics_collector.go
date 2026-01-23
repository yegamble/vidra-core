package livestream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// AnalyticsCollectorConfig holds configuration for analytics collection
type AnalyticsCollectorConfig struct {
	CollectionInterval time.Duration
	RetentionDays      int
	CleanupInterval    time.Duration
}

// DefaultAnalyticsConfig returns default analytics configuration
func DefaultAnalyticsConfig() AnalyticsCollectorConfig {
	return AnalyticsCollectorConfig{
		CollectionInterval: 30 * time.Second,
		RetentionDays:      90,
		CleanupInterval:    24 * time.Hour,
	}
}

// StreamRepository interface for analytics collector
type StreamRepository interface {
	GetActiveStreams(ctx context.Context) ([]*domain.LiveStream, error)
	GetByID(ctx context.Context, id string) (*domain.LiveStream, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
}

// ChatRepository interface for analytics collector
type ChatRepository interface {
	GetMessageCountSince(ctx context.Context, streamID uuid.UUID, since time.Time) (int, error)
}

// AnalyticsCollector collects and stores stream analytics data
type AnalyticsCollector struct {
	db               *sqlx.DB
	redis            *redis.Client
	analyticsRepo    repository.AnalyticsRepository
	streamRepo       StreamRepository
	chatRepo         ChatRepository
	config           AnalyticsCollectorConfig
	collectionTicker *time.Ticker
	cleanupTicker    *time.Ticker
	done             chan bool
	wg               sync.WaitGroup
	mu               sync.RWMutex
	running          bool
	activeStreams    map[uuid.UUID]*streamMetrics
}

// streamMetrics holds temporary metrics for active streams
type streamMetrics struct {
	StreamID        uuid.UUID
	PeakViewerCount int
	UniqueViewers   map[string]bool // session IDs
	TotalMessages   int
	UniqueCharters  map[string]bool
	LastCollectedAt time.Time
}

// NewAnalyticsCollector creates a new analytics collector
func NewAnalyticsCollector(
	db *sqlx.DB,
	redis *redis.Client,
	analyticsRepo repository.AnalyticsRepository,
	streamRepo StreamRepository,
	chatRepo ChatRepository,
	config AnalyticsCollectorConfig,
) *AnalyticsCollector {
	return &AnalyticsCollector{
		db:            db,
		redis:         redis,
		analyticsRepo: analyticsRepo,
		streamRepo:    streamRepo,
		chatRepo:      chatRepo,
		config:        config,
		done:          make(chan bool),
		activeStreams: make(map[uuid.UUID]*streamMetrics),
	}
}

// Start begins the analytics collection background process
func (c *AnalyticsCollector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("analytics collector already running")
	}

	c.collectionTicker = time.NewTicker(c.config.CollectionInterval)
	c.cleanupTicker = time.NewTicker(c.config.CleanupInterval)
	c.running = true

	// Start collection goroutine
	c.wg.Add(1)
	go c.runCollection(ctx)

	// Start cleanup goroutine
	c.wg.Add(1)
	go c.runCleanup(ctx)

	log.Printf("Analytics collector started with collection interval: %v", c.config.CollectionInterval)
	return nil
}

// Stop gracefully stops the analytics collector
func (c *AnalyticsCollector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}

	log.Println("Stopping analytics collector...")

	if c.collectionTicker != nil {
		c.collectionTicker.Stop()
	}

	if c.cleanupTicker != nil {
		c.cleanupTicker.Stop()
	}

	close(c.done)
	c.wg.Wait()
	c.running = false

	log.Println("Analytics collector stopped")
}

// IsRunning returns whether the collector is running
func (c *AnalyticsCollector) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// runCollection is the main collection loop
func (c *AnalyticsCollector) runCollection(ctx context.Context) {
	defer c.wg.Done()

	// Collect immediately on start
	if err := c.collectAllStreams(ctx); err != nil {
		log.Printf("Error collecting analytics: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("Analytics collection context cancelled")
			return
		case <-c.done:
			log.Println("Analytics collection received stop signal")
			return
		case <-c.collectionTicker.C:
			if err := c.collectAllStreams(ctx); err != nil {
				log.Printf("Error collecting analytics: %v", err)
			}
		}
	}
}

// runCleanup is the cleanup loop for old analytics data
func (c *AnalyticsCollector) runCleanup(ctx context.Context) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-c.cleanupTicker.C:
			if err := c.analyticsRepo.CleanupOldAnalytics(ctx, c.config.RetentionDays); err != nil {
				log.Printf("Error cleaning up old analytics: %v", err)
			}
		}
	}
}

// collectAllStreams collects analytics for all active streams
func (c *AnalyticsCollector) collectAllStreams(ctx context.Context) error {
	// Get active streams (status = 'live')
	streams, err := c.streamRepo.GetActiveStreams(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active streams: %w", err)
	}

	if len(streams) == 0 {
		return nil
	}

	streamIDs := make([]uuid.UUID, len(streams))
	for i, s := range streams {
		streamIDs[i] = s.ID
	}

	// Batch fetch viewer counts
	viewerCounts, err := c.analyticsRepo.GetCurrentViewerCounts(ctx, streamIDs)
	if err != nil {
		log.Printf("Error batch fetching viewer counts: %v", err)
		// Continue with partial data or empty
		viewerCounts = make(map[uuid.UUID]int)
	}

	// Batch fetch active viewers
	activeViewersMap, err := c.analyticsRepo.GetActiveViewersForStreams(ctx, streamIDs)
	if err != nil {
		log.Printf("Error batch fetching active viewers: %v", err)
		activeViewersMap = make(map[uuid.UUID][]*domain.AnalyticsViewerSession)
	}

	// Batch fetch redis metrics using pipeline
	type redisMetrics struct {
		bitrate    *redis.StringCmd
		framerate  *redis.StringCmd
		resolution *redis.StringCmd
	}
	redisCmds := make(map[uuid.UUID]*redisMetrics)

	if c.redis != nil {
		pipe := c.redis.Pipeline()
		for _, id := range streamIDs {
			cmds := &redisMetrics{}
			cmds.bitrate = pipe.Get(ctx, fmt.Sprintf("stream:%s:bitrate", id))
			cmds.framerate = pipe.Get(ctx, fmt.Sprintf("stream:%s:framerate", id))
			cmds.resolution = pipe.Get(ctx, fmt.Sprintf("stream:%s:resolution", id))
			redisCmds[id] = cmds
		}
		_, _ = pipe.Exec(ctx) // Best effort, ignore pipeline errors
	}

	var analyticsList []*domain.StreamAnalytics

	for _, stream := range streams {
		streamID := stream.ID

		// Initialize metrics for new streams
		c.mu.Lock()
		metrics, exists := c.activeStreams[streamID]
		if !exists {
			metrics = &streamMetrics{
				StreamID:       streamID,
				UniqueViewers:  make(map[string]bool),
				UniqueCharters: make(map[string]bool),
			}
			c.activeStreams[streamID] = metrics
		}
		c.mu.Unlock()

		viewerCount := viewerCounts[streamID]

		// Update peak viewer count
		if viewerCount > metrics.PeakViewerCount {
			metrics.PeakViewerCount = viewerCount
		}

		// Update unique viewers
		if sessions, ok := activeViewersMap[streamID]; ok {
			for _, session := range sessions {
				metrics.UniqueViewers[session.SessionID] = true
			}
		}

		// Chat statistics (still per-stream for now)
		var chatMessageCount, chatParticipantCount int
		if c.chatRepo != nil {
			messageCount, err := c.chatRepo.GetMessageCountSince(ctx, streamID, metrics.LastCollectedAt)
			if err == nil {
				chatMessageCount = messageCount
				metrics.TotalMessages += messageCount
			}
			chatParticipantCount = len(metrics.UniqueCharters)
		}

		// Redis metrics processing
		var bitrate *int
		var framerate *float64
		var resolution string

		if cmds, ok := redisCmds[streamID]; ok {
			if val, err := cmds.bitrate.Result(); err == nil {
				var br int
				if _, err := fmt.Sscanf(val, "%d", &br); err == nil {
					bitrate = &br
				}
			}
			if val, err := cmds.framerate.Result(); err == nil {
				var fr float64
				if _, err := fmt.Sscanf(val, "%f", &fr); err == nil {
					framerate = &fr
				}
			}
			if val, err := cmds.resolution.Result(); err == nil {
				resolution = val
			}
		}

		// Create analytics record
		analytics := &domain.StreamAnalytics{
			ID:                uuid.New(),
			StreamID:          streamID,
			CollectedAt:       time.Now(),
			ViewerCount:       viewerCount,
			PeakViewerCount:   metrics.PeakViewerCount,
			UniqueViewers:     len(metrics.UniqueViewers),
			ChatMessagesCount: chatMessageCount,
			ChatParticipants:  chatParticipantCount,
			Bitrate:           bitrate,
			Framerate:         framerate,
			Resolution:        resolution,
			ViewerCountries:   json.RawMessage("{}"),
			ViewerDevices:     json.RawMessage("{}"),
			ViewerBrowsers:    json.RawMessage("{}"),
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}
		analyticsList = append(analyticsList, analytics)

		// Update last collected time
		metrics.LastCollectedAt = time.Now()
	}

	// Batch insert analytics
	if len(analyticsList) > 0 {
		if err := c.analyticsRepo.BatchCreateAnalytics(ctx, analyticsList); err != nil {
			log.Printf("Error batch creating analytics: %v", err)
		}
	}

	// Batch update stream summaries
	if len(streamIDs) > 0 {
		if err := c.analyticsRepo.BatchUpdateStreamSummaries(ctx, streamIDs); err != nil {
			log.Printf("Error batch updating stream summaries: %v", err)
		}
	}

	return nil
}


// TrackViewerJoin tracks when a viewer joins a stream
func (c *AnalyticsCollector) TrackViewerJoin(ctx context.Context, streamID uuid.UUID, userID *uuid.UUID, sessionID, ipAddress, userAgent string) error {
	// Parse user agent to extract device info (simplified)
	deviceType := "desktop"
	browser := "unknown"
	os := "unknown"

	// This would normally use a proper user agent parser
	if userAgent != "" {
		// Simplified detection
		switch {
		case contains(userAgent, "Mobile"):
			deviceType = "mobile"
		case contains(userAgent, "Tablet"):
			deviceType = "tablet"
		}

		switch {
		case contains(userAgent, "Chrome"):
			browser = "chrome"
		case contains(userAgent, "Firefox"):
			browser = "firefox"
		case contains(userAgent, "Safari"):
			browser = "safari"
		}
	}

	// Create viewer session
	session := &domain.AnalyticsViewerSession{
		ID:              uuid.New(),
		StreamID:        streamID,
		UserID:          userID,
		SessionID:       sessionID,
		JoinedAt:        time.Now(),
		IPAddress:       ipAddress,
		DeviceType:      deviceType,
		Browser:         browser,
		OperatingSystem: os,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	return c.analyticsRepo.CreateViewerSession(ctx, session)
}

// TrackViewerLeave tracks when a viewer leaves a stream
func (c *AnalyticsCollector) TrackViewerLeave(ctx context.Context, sessionID string) error {
	return c.analyticsRepo.EndViewerSession(ctx, sessionID)
}

// TrackEngagement tracks viewer engagement activities
func (c *AnalyticsCollector) TrackEngagement(ctx context.Context, sessionID string, messagesSent int, liked, shared bool) error {
	return c.analyticsRepo.UpdateSessionEngagement(ctx, sessionID, messagesSent, liked, shared)
}

// GetStreamAnalytics retrieves analytics data for a stream
func (c *AnalyticsCollector) GetStreamAnalytics(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
	return c.analyticsRepo.GetAnalyticsTimeSeries(ctx, streamID, timeRange)
}

// GetStreamSummary retrieves summary statistics for a stream
func (c *AnalyticsCollector) GetStreamSummary(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error) {
	return c.analyticsRepo.GetStreamSummary(ctx, streamID)
}

// CleanupStreamMetrics removes metrics for ended streams
func (c *AnalyticsCollector) CleanupStreamMetrics(streamID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.activeStreams, streamID)
}

// Helper function for simple string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}

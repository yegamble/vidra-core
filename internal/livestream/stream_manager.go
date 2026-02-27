package livestream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// StreamManager manages live stream state and viewer tracking
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

// StreamState tracks runtime state of a live stream
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

// ViewerHeartbeat represents a viewer heartbeat update
type ViewerHeartbeat struct {
	StreamID  uuid.UUID
	SessionID string
	Timestamp time.Time
}

// NewStreamManager creates a new stream manager
func NewStreamManager(
	streamRepo repository.LiveStreamRepository,
	viewerRepo repository.ViewerSessionRepository,
	redisClient *redis.Client,
	logger *logrus.Logger,
) *StreamManager {
	return &StreamManager{
		streamRepo:       streamRepo,
		viewerRepo:       viewerRepo,
		redisClient:      redisClient,
		logger:           logger,
		activeStreams:    make(map[uuid.UUID]*StreamState),
		viewerHeartbeats: make(chan ViewerHeartbeat, 1000),
		shutdownChan:     make(chan struct{}),
	}
}

// Start starts the stream manager background workers
func (sm *StreamManager) Start(ctx context.Context) error {
	sm.logger.Info("Starting stream manager")

	// Start heartbeat processor
	sm.wg.Add(1)
	go sm.processHeartbeats(ctx)

	// Start viewer count updater
	sm.wg.Add(1)
	go sm.updateViewerCounts(ctx)

	// Start cleanup worker
	sm.wg.Add(1)
	go sm.cleanupWorker(ctx)

	return nil
}

// Shutdown gracefully shuts down the stream manager
func (sm *StreamManager) Shutdown(ctx context.Context) error {
	sm.logger.Info("Shutting down stream manager")
	close(sm.shutdownChan)

	// Wait for workers to finish
	done := make(chan struct{})
	go func() {
		sm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		sm.logger.Info("Stream manager shut down successfully")
		return nil
	case <-ctx.Done():
		sm.logger.Warn("Stream manager shutdown timed out")
		return ctx.Err()
	}
}

// StartStream transitions a stream to live status
func (sm *StreamManager) StartStream(ctx context.Context, streamID uuid.UUID) error {
	// Get stream from database
	stream, err := sm.streamRepo.GetByID(ctx, streamID)
	if err != nil {
		return fmt.Errorf("failed to get stream: %w", err)
	}

	// Start the stream (updates status and timestamps)
	if err := stream.Start(); err != nil {
		return fmt.Errorf("failed to start stream: %w", err)
	}

	// Update in database
	if err := sm.streamRepo.Update(ctx, stream); err != nil {
		return fmt.Errorf("failed to update stream: %w", err)
	}

	// Add to active streams
	sm.activeStreamsMu.Lock()
	sm.activeStreams[streamID] = &StreamState{
		StreamID:    streamID,
		ChannelID:   stream.ChannelID,
		UserID:      stream.UserID,
		Status:      domain.StreamStatusLive,
		StartedAt:   *stream.StartedAt,
		ViewerCount: 0,
		PeakViewers: 0,
		LastUpdate:  time.Now(),
	}
	sm.activeStreamsMu.Unlock()

	// Cache in Redis for fast lookups
	if sm.redisClient != nil {
		key := fmt.Sprintf("stream:active:%s", streamID)
		if err := sm.redisClient.Set(ctx, key, "1", 0).Err(); err != nil {
			sm.logger.WithError(err).Warn("Failed to cache stream in Redis")
		}
	}

	sm.logger.WithFields(logrus.Fields{
		"stream_id":  streamID,
		"channel_id": stream.ChannelID,
	}).Info("Stream started")

	return nil
}

// EndStream transitions a stream to ended status
func (sm *StreamManager) EndStream(ctx context.Context, streamID uuid.UUID) error {
	// Remove from active streams
	sm.activeStreamsMu.Lock()
	delete(sm.activeStreams, streamID)
	sm.activeStreamsMu.Unlock()

	// End the stream in database (also ends all viewer sessions)
	if err := sm.streamRepo.EndStream(ctx, streamID); err != nil {
		return fmt.Errorf("failed to end stream: %w", err)
	}

	// Remove from Redis cache
	if sm.redisClient != nil {
		key := fmt.Sprintf("stream:active:%s", streamID)
		if err := sm.redisClient.Del(ctx, key).Err(); err != nil {
			sm.logger.WithError(err).Warn("Failed to remove stream from Redis")
		}
	}

	sm.logger.WithField("stream_id", streamID).Info("Stream ended")

	return nil
}

// IsStreamActive checks if a stream is currently active
func (sm *StreamManager) IsStreamActive(streamID uuid.UUID) bool {
	sm.activeStreamsMu.RLock()
	defer sm.activeStreamsMu.RUnlock()
	_, exists := sm.activeStreams[streamID]
	return exists
}

// GetStreamState returns the current state of a stream
func (sm *StreamManager) GetStreamState(streamID uuid.UUID) (*StreamState, bool) {
	sm.activeStreamsMu.RLock()
	defer sm.activeStreamsMu.RUnlock()
	state, exists := sm.activeStreams[streamID]
	return state, exists
}

// RecordViewerJoin records a new viewer joining a stream
func (sm *StreamManager) RecordViewerJoin(ctx context.Context, streamID uuid.UUID, sessionID string, userID *uuid.UUID, ipAddress, userAgent, countryCode string) error {
	session := &domain.ViewerSession{
		ID:           uuid.New(),
		LiveStreamID: streamID,
		SessionID:    sessionID,
		UserID:       userID,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		CountryCode:  countryCode,
	}

	if err := sm.viewerRepo.Create(ctx, session); err != nil {
		return fmt.Errorf("failed to create viewer session: %w", err)
	}

	sm.logger.WithFields(logrus.Fields{
		"stream_id":  streamID,
		"session_id": sessionID,
	}).Debug("Viewer joined stream")

	return nil
}

// RecordViewerLeave records a viewer leaving a stream
func (sm *StreamManager) RecordViewerLeave(ctx context.Context, sessionID string) error {
	if err := sm.viewerRepo.EndSession(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to end viewer session: %w", err)
	}

	sm.logger.WithField("session_id", sessionID).Debug("Viewer left stream")

	return nil
}

// SendHeartbeat sends a viewer heartbeat (non-blocking)
func (sm *StreamManager) SendHeartbeat(streamID uuid.UUID, sessionID string) {
	select {
	case sm.viewerHeartbeats <- ViewerHeartbeat{
		StreamID:  streamID,
		SessionID: sessionID,
		Timestamp: time.Now(),
	}:
	default:
		// Channel full, drop heartbeat (not critical)
		sm.logger.Debug("Viewer heartbeat channel full, dropping heartbeat")
	}
}

func (sm *StreamManager) processHeartbeats(ctx context.Context) {
	defer sm.wg.Done()

	batch := make(map[string]time.Time) // sessionID -> last heartbeat time
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sm.flushRemainingHeartbeats(batch)
			return
		case <-sm.shutdownChan:
			sm.flushRemainingHeartbeats(batch)
			return
		case heartbeat := <-sm.viewerHeartbeats:
			// Batch heartbeats for efficiency
			batch[heartbeat.SessionID] = heartbeat.Timestamp
		case <-ticker.C:
			// Flush batch to database
			if len(batch) > 0 {
				sm.flushHeartbeatBatch(ctx, batch)
				batch = make(map[string]time.Time)
			}
		}
	}
}

// flushRemainingHeartbeats drains any accumulated heartbeats on shutdown.
// Uses a fresh context since the parent context may already be cancelled.
func (sm *StreamManager) flushRemainingHeartbeats(batch map[string]time.Time) {
	if len(batch) == 0 {
		return
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sm.flushHeartbeatBatch(flushCtx, batch)
}

func (sm *StreamManager) flushHeartbeatBatch(ctx context.Context, batch map[string]time.Time) {
	for sessionID := range batch {
		if err := sm.viewerRepo.UpdateHeartbeat(ctx, sessionID); err != nil {
			sm.logger.WithError(err).WithField("session_id", sessionID).
				Debug("Failed to update heartbeat")
		}
	}
}

func (sm *StreamManager) updateViewerCounts(ctx context.Context) {
	defer sm.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.shutdownChan:
			return
		case <-ticker.C:
			sm.refreshViewerCounts(ctx)
		}
	}
}

func (sm *StreamManager) refreshViewerCounts(ctx context.Context) {
	sm.activeStreamsMu.RLock()
	streamIDs := make([]uuid.UUID, 0, len(sm.activeStreams))
	for streamID := range sm.activeStreams {
		streamIDs = append(streamIDs, streamID)
	}
	sm.activeStreamsMu.RUnlock()

	for _, streamID := range streamIDs {
		count, err := sm.viewerRepo.CountActiveViewers(ctx, streamID)
		if err != nil {
			sm.logger.WithError(err).WithField("stream_id", streamID).
				Error("Failed to count active viewers")
			continue
		}

		// Update stream state
		sm.activeStreamsMu.Lock()
		if state, exists := sm.activeStreams[streamID]; exists {
			state.ViewerCount = count
			if count > state.PeakViewers {
				state.PeakViewers = count
			}
			state.LastUpdate = time.Now()
		}
		sm.activeStreamsMu.Unlock()

		// Update in database
		if err := sm.streamRepo.UpdateViewerCount(ctx, streamID, count); err != nil {
			sm.logger.WithError(err).WithField("stream_id", streamID).
				Error("Failed to update viewer count in database")
		}
	}
}

func (sm *StreamManager) cleanupWorker(ctx context.Context) {
	defer sm.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.shutdownChan:
			return
		case <-ticker.C:
			sm.performCleanup(ctx)
		}
	}
}

func (sm *StreamManager) performCleanup(ctx context.Context) {
	// Cleanup stale viewer sessions
	count, err := sm.viewerRepo.CleanupStale(ctx)
	if err != nil {
		sm.logger.WithError(err).Error("Failed to cleanup stale viewer sessions")
	} else if count > 0 {
		sm.logger.WithField("count", count).Info("Cleaned up stale viewer sessions")
	}

	// Check for streams that should have ended but are still marked as active
	sm.activeStreamsMu.RLock()
	for streamID, state := range sm.activeStreams {
		// If stream has been live for more than 24 hours, check if it's really still active
		if time.Since(state.StartedAt) > 24*time.Hour {
			sm.logger.WithFields(logrus.Fields{
				"stream_id": streamID,
				"duration":  time.Since(state.StartedAt),
			}).Warn("Stream has been active for over 24 hours")
		}
	}
	sm.activeStreamsMu.RUnlock()
}

// GetActiveStreamCount returns the number of currently active streams
func (sm *StreamManager) GetActiveStreamCount() int {
	sm.activeStreamsMu.RLock()
	defer sm.activeStreamsMu.RUnlock()
	return len(sm.activeStreams)
}

// GetAllActiveStreams returns all currently active stream states
func (sm *StreamManager) GetAllActiveStreams() []*StreamState {
	sm.activeStreamsMu.RLock()
	defer sm.activeStreamsMu.RUnlock()

	states := make([]*StreamState, 0, len(sm.activeStreams))
	for _, state := range sm.activeStreams {
		states = append(states, state)
	}
	return states
}

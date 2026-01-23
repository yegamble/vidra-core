package livestream

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
)

// Mock implementations

type mockStreamRepo struct {
	streams []*domain.LiveStream
}

func (m *mockStreamRepo) GetActiveStreams(ctx context.Context) ([]*domain.LiveStream, error) {
	return m.streams, nil
}

func (m *mockStreamRepo) GetByID(ctx context.Context, id string) (*domain.LiveStream, error) {
	return nil, nil
}

func (m *mockStreamRepo) GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}

type mockAnalyticsRepo struct {
	viewerCounts  map[uuid.UUID]int
	activeViewers map[uuid.UUID][]*domain.AnalyticsViewerSession
}

func (m *mockAnalyticsRepo) GetCurrentViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	time.Sleep(time.Millisecond) // Simulate DB latency
	return m.viewerCounts[streamID], nil
}

func (m *mockAnalyticsRepo) GetActiveViewers(ctx context.Context, streamID uuid.UUID) ([]*domain.AnalyticsViewerSession, error) {
	time.Sleep(time.Millisecond) // Simulate DB latency
	return m.activeViewers[streamID], nil
}

func (m *mockAnalyticsRepo) CreateAnalytics(ctx context.Context, analytics *domain.StreamAnalytics) error {
	time.Sleep(time.Millisecond) // Simulate DB latency
	return nil
}

func (m *mockAnalyticsRepo) UpdateStreamSummary(ctx context.Context, streamID uuid.UUID) error {
	time.Sleep(time.Millisecond) // Simulate DB latency
	return nil
}

// Satisfy AnalyticsRepository interface
func (m *mockAnalyticsRepo) CleanupOldAnalytics(ctx context.Context, retentionDays int) error { return nil }
func (m *mockAnalyticsRepo) GetAnalyticsByStream(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) GetAnalyticsTimeSeries(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) GetLatestAnalytics(ctx context.Context, streamID uuid.UUID) (*domain.StreamAnalytics, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) GetStreamSummary(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) CreateOrUpdateSummary(ctx context.Context, summary *domain.StreamStatsSummary) error {
	return nil
}
func (m *mockAnalyticsRepo) CreateViewerSession(ctx context.Context, session *domain.AnalyticsViewerSession) error {
	return nil
}
func (m *mockAnalyticsRepo) EndViewerSession(ctx context.Context, sessionID string) error {
	return nil
}
func (m *mockAnalyticsRepo) GetViewerSession(ctx context.Context, sessionID string) (*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) UpdateSessionEngagement(ctx context.Context, sessionID string, messagesSent int, liked, shared bool) error {
	return nil
}

// New methods (will be added to interface later, but we can implement them here already to make compiler happy when interface changes)
func (m *mockAnalyticsRepo) GetCurrentViewerCounts(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	time.Sleep(time.Millisecond) // Simulate batch DB latency
	return m.viewerCounts, nil
}

func (m *mockAnalyticsRepo) GetActiveViewersForStreams(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]*domain.AnalyticsViewerSession, error) {
	time.Sleep(time.Millisecond) // Simulate batch DB latency
	return m.activeViewers, nil
}

func (m *mockAnalyticsRepo) BatchCreateAnalytics(ctx context.Context, analytics []*domain.StreamAnalytics) error {
	time.Sleep(time.Millisecond) // Simulate batch DB latency
	return nil
}

func (m *mockAnalyticsRepo) BatchUpdateStreamSummaries(ctx context.Context, streamIDs []uuid.UUID) error {
	time.Sleep(time.Millisecond) // Simulate batch DB latency
	return nil
}

type mockChatRepo struct{}

func (m *mockChatRepo) GetMessageCountSince(ctx context.Context, streamID uuid.UUID, since time.Time) (int, error) {
	time.Sleep(time.Millisecond) // Simulate DB latency
	return 10, nil
}

func BenchmarkCollectAllStreams(b *testing.B) {
	// Setup mocks
	streamRepo := &mockStreamRepo{}
	analyticsRepo := &mockAnalyticsRepo{
		viewerCounts:  make(map[uuid.UUID]int),
		activeViewers: make(map[uuid.UUID][]*domain.AnalyticsViewerSession),
	}
	chatRepo := &mockChatRepo{}

	// Create N active streams
	nStreams := 50
	for i := 0; i < nStreams; i++ {
		streamID := uuid.New()
		streamRepo.streams = append(streamRepo.streams, &domain.LiveStream{
			ID:     streamID,
			Status: "live",
		})
		analyticsRepo.viewerCounts[streamID] = 100
		analyticsRepo.activeViewers[streamID] = []*domain.AnalyticsViewerSession{
			{SessionID: "s1"}, {SessionID: "s2"},
		}
	}

	config := DefaultAnalyticsConfig()
	collector := NewAnalyticsCollector(nil, nil, analyticsRepo, streamRepo, chatRepo, config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.collectAllStreams(context.Background())
	}
}

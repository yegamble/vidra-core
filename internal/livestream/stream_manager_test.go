package livestream

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLiveStreamRepo struct {
	mu      sync.Mutex
	streams map[uuid.UUID]*domain.LiveStream
	err     error
}

func newMockLiveStreamRepo() *mockLiveStreamRepo {
	return &mockLiveStreamRepo{streams: make(map[uuid.UUID]*domain.LiveStream)}
}

func (m *mockLiveStreamRepo) Create(ctx context.Context, s *domain.LiveStream) error { return m.err }
func (m *mockLiveStreamRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	s, ok := m.streams[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (m *mockLiveStreamRepo) GetByStreamKey(ctx context.Context, key string) (*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockLiveStreamRepo) Update(ctx context.Context, s *domain.LiveStream) error { return m.err }
func (m *mockLiveStreamRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	return nil
}
func (m *mockLiveStreamRepo) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	return nil
}
func (m *mockLiveStreamRepo) Delete(ctx context.Context, id uuid.UUID) error    { return nil }
func (m *mockLiveStreamRepo) EndStream(ctx context.Context, id uuid.UUID) error { return m.err }
func (m *mockLiveStreamRepo) GetChannelByStreamID(ctx context.Context, streamID uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error {
	return nil
}
func (m *mockLiveStreamRepo) ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart *time.Time, scheduledEnd *time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error {
	return nil
}
func (m *mockLiveStreamRepo) CancelSchedule(ctx context.Context, streamID uuid.UUID) error {
	return nil
}
func (m *mockLiveStreamRepo) GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *mockLiveStreamRepo) GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func (m *mockLiveStreamRepo) addStream(s *domain.LiveStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[s.ID] = s
}

type mockViewerSessionRepo struct {
	err error
}

func (m *mockViewerSessionRepo) Create(ctx context.Context, s *domain.ViewerSession) error {
	return m.err
}
func (m *mockViewerSessionRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.ViewerSession, error) {
	return nil, nil
}
func (m *mockViewerSessionRepo) GetBySessionID(ctx context.Context, sessionID string) (*domain.ViewerSession, error) {
	return nil, nil
}
func (m *mockViewerSessionRepo) GetActiveByStream(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ViewerSession, error) {
	return nil, nil
}
func (m *mockViewerSessionRepo) CountActiveViewers(ctx context.Context, streamID uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockViewerSessionRepo) UpdateHeartbeat(ctx context.Context, sessionID string) error {
	return nil
}
func (m *mockViewerSessionRepo) EndSession(ctx context.Context, sessionID string) error {
	return m.err
}
func (m *mockViewerSessionRepo) CleanupStale(ctx context.Context) (int, error) { return 0, nil }

func newTestStreamManager() *StreamManager {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return NewStreamManager(
		newMockLiveStreamRepo(),
		&mockViewerSessionRepo{},
		nil,
		logger,
	)
}

func newWaitingStream(id, channelID, userID uuid.UUID) *domain.LiveStream {
	return &domain.LiveStream{
		ID:        id,
		ChannelID: channelID,
		UserID:    userID,
		Status:    domain.StreamStatusWaiting,
	}
}

func TestStreamManager_InitialActiveCountIsZero(t *testing.T) {
	sm := newTestStreamManager()
	assert.Equal(t, 0, sm.GetActiveStreamCount())
}

func TestStreamManager_IsStreamActive_ReturnsFalseForUnknown(t *testing.T) {
	sm := newTestStreamManager()
	assert.False(t, sm.IsStreamActive(uuid.New()))
}

func TestStreamManager_GetStreamState_ReturnsNilForUnknown(t *testing.T) {
	sm := newTestStreamManager()
	state, exists := sm.GetStreamState(uuid.New())
	assert.Nil(t, state)
	assert.False(t, exists)
}

func TestStreamManager_GetAllActiveStreams_EmptyInitially(t *testing.T) {
	sm := newTestStreamManager()
	streams := sm.GetAllActiveStreams()
	assert.Empty(t, streams)
}

func TestStreamManager_StartStream_ActivatesStream(t *testing.T) {
	streamRepo := newMockLiveStreamRepo()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	sm := NewStreamManager(streamRepo, &mockViewerSessionRepo{}, nil, logger)

	streamID := uuid.New()
	stream := newWaitingStream(streamID, uuid.New(), uuid.New())
	streamRepo.addStream(stream)

	ctx := context.Background()
	err := sm.StartStream(ctx, streamID)
	require.NoError(t, err)

	assert.True(t, sm.IsStreamActive(streamID))
	assert.Equal(t, 1, sm.GetActiveStreamCount())

	state, exists := sm.GetStreamState(streamID)
	require.True(t, exists)
	assert.Equal(t, streamID, state.StreamID)
	assert.Equal(t, domain.StreamStatusLive, state.Status)
}

func TestStreamManager_StartStream_StreamNotFound_ReturnsError(t *testing.T) {
	sm := newTestStreamManager()
	err := sm.StartStream(context.Background(), uuid.New())
	require.Error(t, err)
}

func TestStreamManager_EndStream_RemovesFromActiveStreams(t *testing.T) {
	streamRepo := newMockLiveStreamRepo()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	sm := NewStreamManager(streamRepo, &mockViewerSessionRepo{}, nil, logger)

	streamID := uuid.New()
	stream := newWaitingStream(streamID, uuid.New(), uuid.New())
	streamRepo.addStream(stream)

	ctx := context.Background()
	require.NoError(t, sm.StartStream(ctx, streamID))
	assert.True(t, sm.IsStreamActive(streamID))

	require.NoError(t, sm.EndStream(ctx, streamID))
	assert.False(t, sm.IsStreamActive(streamID))
	assert.Equal(t, 0, sm.GetActiveStreamCount())
}

func TestStreamManager_RecordViewerJoin_Success(t *testing.T) {
	sm := newTestStreamManager()
	streamID := uuid.New()
	err := sm.RecordViewerJoin(context.Background(), streamID, "sess-1", nil, "1.2.3.4", "Go-test/1.0", "US")
	require.NoError(t, err)
}

func TestStreamManager_RecordViewerLeave_Success(t *testing.T) {
	sm := newTestStreamManager()
	err := sm.RecordViewerLeave(context.Background(), "sess-1")
	require.NoError(t, err)
}

func TestStreamManager_SendHeartbeat_NonBlocking(t *testing.T) {
	sm := newTestStreamManager()
	streamID := uuid.New()
	done := make(chan struct{})
	go func() {
		sm.SendHeartbeat(streamID, "sess-1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SendHeartbeat blocked unexpectedly")
	}
}

func TestStreamManager_StartAndShutdown(t *testing.T) {
	sm := newTestStreamManager()
	ctx := context.Background()
	require.NoError(t, sm.Start(ctx))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, sm.Shutdown(shutdownCtx))
}

func TestStreamManager_ConcurrentStreamAccess(t *testing.T) {
	streamRepo := newMockLiveStreamRepo()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	sm := NewStreamManager(streamRepo, &mockViewerSessionRepo{}, nil, logger)

	streamIDs := make([]uuid.UUID, 5)
	for i := range streamIDs {
		id := uuid.New()
		streamIDs[i] = id
		streamRepo.addStream(newWaitingStream(id, uuid.New(), uuid.New()))
	}

	var wg sync.WaitGroup
	ctx := context.Background()
	for _, id := range streamIDs {
		wg.Add(1)
		go func(sid uuid.UUID) {
			defer wg.Done()
			_ = sm.StartStream(ctx, sid)
			sm.IsStreamActive(sid)
			sm.GetStreamState(sid)
			sm.GetActiveStreamCount()
			_ = sm.EndStream(ctx, sid)
		}(id)
	}
	wg.Wait()
}

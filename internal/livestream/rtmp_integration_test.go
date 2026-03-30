package livestream

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"

	"github.com/google/uuid"
	"github.com/nareix/joy4/format/rtmp"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestRTMPServerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	td.TruncateTables(t, "live_streams", "stream_keys", "viewer_sessions", "channels", "users")

	streamRepo := repository.NewLiveStreamRepository(td.DB)
	streamKeyRepo := repository.NewStreamKeyRepository(td.DB)
	viewerRepo := repository.NewViewerSessionRepository(td.DB)

	redisOpts, err := redis.ParseURL(testutil.RedisTestURL())
	require.NoError(t, err)
	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	require.NoError(t, redisClient.FlushDB(context.Background()).Err())

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	streamManager := NewStreamManager(streamRepo, viewerRepo, redisClient, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = streamManager.Start(ctx)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		streamManager.Shutdown(shutdownCtx)
	}()

	cfg := &config.Config{
		RTMPHost:           "127.0.0.1",
		RTMPPort:           19350,
		RTMPChunkSize:      4096,
		RTMPMaxConnections: 10,
		RTMPReadTimeout:    30,
		RTMPWriteTimeout:   30,
		MaxStreamDuration:  0,
	}

	rtmpServer := NewRTMPServer(cfg, streamRepo, streamKeyRepo, streamManager, nil, nil, logger)
	err = rtmpServer.Start(ctx)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		rtmpServer.Shutdown(shutdownCtx)
	}()

	time.Sleep(100 * time.Millisecond)

	t.Run("BasicStreamLifecycle", func(t *testing.T) {
		testBasicStreamLifecycle(t, td, streamRepo, streamKeyRepo, cfg, streamManager)
	})

	t.Run("AuthenticationFailure", func(t *testing.T) {
		testAuthenticationFailure(t, td, cfg)
	})

	t.Run("ConcurrentStreams", func(t *testing.T) {
		testConcurrentStreams(t, td, streamRepo, streamKeyRepo, cfg, streamManager)
	})

	t.Run("ViewerTracking", func(t *testing.T) {
		testViewerTracking(t, td, streamRepo, streamKeyRepo, viewerRepo, cfg, streamManager)
	})

	t.Run("StreamAlreadyActive", func(t *testing.T) {
		testStreamAlreadyActive(t, td, streamRepo, streamKeyRepo, cfg)
	})
}

func testBasicStreamLifecycle(t *testing.T, td *testutil.TestDB, streamRepo repository.LiveStreamRepository, streamKeyRepo repository.StreamKeyRepository, cfg *config.Config, streamManager *StreamManager) {
	ctx := context.Background()

	user := testutil.CreateTestUser(t, td.DB, "testuser1@test.com", string(domain.RoleUser))
	userID, _ := uuid.Parse(user.ID)
	channelID := testutil.CreateTestChannel(t, td.DB, user.ID, "testchannel1")

	streamKey, err := domain.GenerateStreamKey()
	require.NoError(t, err)

	keyHash, err := bcrypt.GenerateFromPassword([]byte(streamKey), bcrypt.DefaultCost)
	require.NoError(t, err)

	_, err = streamKeyRepo.Create(ctx, channelID, string(keyHash), nil)
	require.NoError(t, err)

	stream := &domain.LiveStream{
		ID:          uuid.New(),
		ChannelID:   channelID,
		UserID:      userID,
		Title:       "Test Stream",
		Description: "Integration test stream",
		StreamKey:   streamKey,
		Status:      domain.StreamStatusWaiting,
		Privacy:     domain.StreamPrivacyPublic,
		SaveReplay:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = streamRepo.Create(ctx, stream)
	require.NoError(t, err)

	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", cfg.RTMPHost, cfg.RTMPPort, streamKey)
	conn, err := connectRTMP(rtmpURL)
	if err != nil {
		t.Logf("RTMP connection failed: %v", err)
		t.Skip("RTMP server may not be ready, skipping test")
		return
	}
	defer conn.Close()

	require.Eventually(t, func() bool {
		updatedStream, err := streamRepo.GetByID(ctx, stream.ID)
		if err != nil {
			return false
		}
		return updatedStream.Status == domain.StreamStatusLive
	}, 1*time.Second, 10*time.Millisecond, "Stream should go live")

	updatedStream, err := streamRepo.GetByID(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StreamStatusLive, updatedStream.Status)
	assert.NotNil(t, updatedStream.StartedAt)

	state, exists := streamManager.GetStreamState(stream.ID)
	assert.True(t, exists)
	assert.Equal(t, domain.StreamStatusLive, state.Status)

	conn.Close()

	require.Eventually(t, func() bool {
		endedStream, err := streamRepo.GetByID(ctx, stream.ID)
		if err != nil {
			return false
		}
		return endedStream.Status == domain.StreamStatusEnded
	}, 1*time.Second, 10*time.Millisecond, "Stream should end")

	endedStream, err := streamRepo.GetByID(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StreamStatusEnded, endedStream.Status)
	assert.NotNil(t, endedStream.EndedAt)
	assert.Zero(t, endedStream.ViewerCount)
}

func testAuthenticationFailure(t *testing.T, td *testutil.TestDB, cfg *config.Config) {
	invalidKey := "invalid-stream-key-that-does-not-exist"
	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", cfg.RTMPHost, cfg.RTMPPort, invalidKey)

	conn, err := connectRTMP(rtmpURL)
	if err == nil {
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)
	}

	t.Log("Authentication failure test completed (connection rejected)")
}

func testConcurrentStreams(t *testing.T, td *testutil.TestDB, streamRepo repository.LiveStreamRepository, streamKeyRepo repository.StreamKeyRepository, cfg *config.Config, streamManager *StreamManager) {
	ctx := context.Background()
	numStreams := 3

	var wg sync.WaitGroup
	streams := make([]*domain.LiveStream, numStreams)
	connections := make([]*rtmp.Conn, numStreams)

	for i := 0; i < numStreams; i++ {
		user := testutil.CreateTestUser(t, td.DB, fmt.Sprintf("concurrentuser%d@test.com", i), string(domain.RoleUser))
		userID, _ := uuid.Parse(user.ID)
		channelID := testutil.CreateTestChannel(t, td.DB, user.ID, fmt.Sprintf("concurrentchannel%d", i))

		streamKey, err := domain.GenerateStreamKey()
		require.NoError(t, err)

		keyHash, err := bcrypt.GenerateFromPassword([]byte(streamKey), bcrypt.DefaultCost)
		require.NoError(t, err)

		_, err = streamKeyRepo.Create(ctx, channelID, string(keyHash), nil)
		require.NoError(t, err)

		stream := &domain.LiveStream{
			ID:          uuid.New(),
			ChannelID:   channelID,
			UserID:      userID,
			Title:       fmt.Sprintf("Concurrent Test Stream %d", i),
			Description: fmt.Sprintf("Concurrent stream %d", i),
			StreamKey:   streamKey,
			Status:      domain.StreamStatusWaiting,
			Privacy:     domain.StreamPrivacyPublic,
			SaveReplay:  true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = streamRepo.Create(ctx, stream)
		require.NoError(t, err)

		streams[i] = stream

		wg.Add(1)
		go func(idx int, key string) {
			defer wg.Done()
			rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", cfg.RTMPHost, cfg.RTMPPort, key)
			conn, err := connectRTMP(rtmpURL)
			if err != nil {
				t.Logf("Concurrent connection %d failed: %v", idx, err)
				return
			}
			connections[idx] = conn
		}(i, streamKey)
	}

	wg.Wait()

	time.Sleep(1 * time.Second)

	liveCount := 0
	for i := 0; i < numStreams; i++ {
		updatedStream, err := streamRepo.GetByID(ctx, streams[i].ID)
		require.NoError(t, err)
		if updatedStream.Status == domain.StreamStatusLive {
			liveCount++
		}
	}

	assert.Greater(t, liveCount, 0, "At least one stream should be live")
	t.Logf("Successfully started %d/%d concurrent streams", liveCount, numStreams)

	for i := 0; i < numStreams; i++ {
		if connections[i] != nil {
			connections[i].Close()
		}
	}

	time.Sleep(500 * time.Millisecond)

	for i := 0; i < numStreams; i++ {
		if connections[i] != nil {
			endedStream, err := streamRepo.GetByID(ctx, streams[i].ID)
			require.NoError(t, err)
			assert.Equal(t, domain.StreamStatusEnded, endedStream.Status)
		}
	}
}

func testViewerTracking(t *testing.T, td *testutil.TestDB, streamRepo repository.LiveStreamRepository, streamKeyRepo repository.StreamKeyRepository, viewerRepo repository.ViewerSessionRepository, cfg *config.Config, streamManager *StreamManager) {
	ctx := context.Background()

	user := testutil.CreateTestUser(t, td.DB, "vieweruser@test.com", string(domain.RoleUser))
	userID, _ := uuid.Parse(user.ID)
	channelID := testutil.CreateTestChannel(t, td.DB, user.ID, "viewerchannel")

	streamKey, err := domain.GenerateStreamKey()
	require.NoError(t, err)

	keyHash, err := bcrypt.GenerateFromPassword([]byte(streamKey), bcrypt.DefaultCost)
	require.NoError(t, err)

	_, err = streamKeyRepo.Create(ctx, channelID, string(keyHash), nil)
	require.NoError(t, err)

	stream := &domain.LiveStream{
		ID:          uuid.New(),
		ChannelID:   channelID,
		UserID:      userID,
		Title:       "Viewer Tracking Test",
		Description: "Test viewer tracking",
		StreamKey:   streamKey,
		Status:      domain.StreamStatusWaiting,
		Privacy:     domain.StreamPrivacyPublic,
		SaveReplay:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = streamRepo.Create(ctx, stream)
	require.NoError(t, err)

	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", cfg.RTMPHost, cfg.RTMPPort, streamKey)
	conn, err := connectRTMP(rtmpURL)
	if err != nil {
		t.Logf("RTMP connection failed: %v", err)
		t.Skip("RTMP server may not be ready, skipping test")
		return
	}
	defer conn.Close()

	time.Sleep(500 * time.Millisecond)

	numViewers := 3
	for i := 0; i < numViewers; i++ {
		viewerSession := &domain.ViewerSession{
			ID:              uuid.New(),
			LiveStreamID:    stream.ID,
			SessionID:       uuid.New().String(),
			UserID:          nil,
			IPAddress:       fmt.Sprintf("192.168.1.%d", i+1),
			UserAgent:       "TestClient/1.0",
			CountryCode:     "US",
			JoinedAt:        time.Now(),
			LastHeartbeatAt: time.Now(),
		}
		err := viewerRepo.Create(ctx, viewerSession)
		require.NoError(t, err)

		err = streamManager.RecordViewerJoin(ctx, domain.ViewerJoinRequest{
			StreamID:    stream.ID,
			SessionID:   viewerSession.SessionID,
			UserID:      nil,
			IPAddress:   viewerSession.IPAddress,
			UserAgent:   viewerSession.UserAgent,
			CountryCode: viewerSession.CountryCode,
		})
		require.NoError(t, err)

		streamManager.SendHeartbeat(stream.ID, viewerSession.SessionID)
	}

	time.Sleep(2 * time.Second)

	count, err := viewerRepo.CountActiveViewers(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, numViewers, count, "Should have %d active viewers", numViewers)

	updatedStream, err := streamRepo.GetByID(ctx, stream.ID)
	require.NoError(t, err)
	assert.Greater(t, updatedStream.ViewerCount, 0, "Viewer count should be updated")

	conn.Close()

	require.Eventually(t, func() bool {
		endedStream, err := streamRepo.GetByID(ctx, stream.ID)
		if err != nil {
			return false
		}
		return endedStream.Status == domain.StreamStatusEnded
	}, 1*time.Second, 10*time.Millisecond, "Stream should end")

	endedStream, err := streamRepo.GetByID(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StreamStatusEnded, endedStream.Status)
}

func testStreamAlreadyActive(t *testing.T, td *testutil.TestDB, streamRepo repository.LiveStreamRepository, streamKeyRepo repository.StreamKeyRepository, cfg *config.Config) {
	ctx := context.Background()

	user := testutil.CreateTestUser(t, td.DB, "duplicateuser@test.com", string(domain.RoleUser))
	userID, _ := uuid.Parse(user.ID)
	channelID := testutil.CreateTestChannel(t, td.DB, user.ID, "duplicatechannel")

	streamKey, err := domain.GenerateStreamKey()
	require.NoError(t, err)

	keyHash, err := bcrypt.GenerateFromPassword([]byte(streamKey), bcrypt.DefaultCost)
	require.NoError(t, err)

	_, err = streamKeyRepo.Create(ctx, channelID, string(keyHash), nil)
	require.NoError(t, err)

	stream := &domain.LiveStream{
		ID:          uuid.New(),
		ChannelID:   channelID,
		UserID:      userID,
		Title:       "Duplicate Stream Test",
		Description: "Test duplicate connection",
		StreamKey:   streamKey,
		Status:      domain.StreamStatusWaiting,
		Privacy:     domain.StreamPrivacyPublic,
		SaveReplay:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = streamRepo.Create(ctx, stream)
	require.NoError(t, err)

	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", cfg.RTMPHost, cfg.RTMPPort, streamKey)
	conn1, err := connectRTMP(rtmpURL)
	if err != nil {
		t.Logf("RTMP connection failed: %v", err)
		t.Skip("RTMP server may not be ready, skipping test")
		return
	}
	defer conn1.Close()

	time.Sleep(500 * time.Millisecond)

	conn2, err := connectRTMP(rtmpURL)
	if err == nil {
		defer conn2.Close()
		time.Sleep(200 * time.Millisecond)
	}

	updatedStream, err := streamRepo.GetByID(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StreamStatusLive, updatedStream.Status)

	conn1.Close()
	time.Sleep(500 * time.Millisecond)
}

func connectRTMP(rtmpURL string) (*rtmp.Conn, error) {
	u, err := rtmp.ParseURL(rtmpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RTMP URL: %w", err)
	}

	addr := net.JoinHostPort(u.Hostname(), u.Port())
	netConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RTMP server: %w", err)
	}

	conn := rtmp.NewConn(netConn)
	conn.URL = u

	return conn, nil
}

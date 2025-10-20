package livestream

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/google/uuid"
	"github.com/nareix/joy4/format/rtmp"
	"github.com/sirupsen/logrus"
)

// RTMPServer handles RTMP stream ingestion
type RTMPServer struct {
	cfg             *config.Config
	server          *rtmp.Server
	listener        net.Listener
	streamRepo      repository.LiveStreamRepository
	streamKeyRepo   repository.StreamKeyRepository
	streamManager   *StreamManager
	logger          *logrus.Logger
	activeStreams   map[string]*StreamSession // streamKey -> session
	activeStreamsMu sync.RWMutex
	shutdownChan    chan struct{}
	wg              sync.WaitGroup
}

// StreamSession represents an active RTMP stream
type StreamSession struct {
	StreamID   uuid.UUID
	StreamKey  string
	ChannelID  uuid.UUID
	UserID     uuid.UUID
	StartedAt  time.Time
	Conn       *rtmp.Conn
	cancelFunc context.CancelFunc
}

// RTMPServerConfig holds configuration for the RTMP server
type RTMPServerConfig struct {
	Host              string
	Port              int
	ChunkSize         int
	MaxConnections    int
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	MaxStreamDuration time.Duration
}

// NewRTMPServer creates a new RTMP server
func NewRTMPServer(
	cfg *config.Config,
	streamRepo repository.LiveStreamRepository,
	streamKeyRepo repository.StreamKeyRepository,
	streamManager *StreamManager,
	logger *logrus.Logger,
) *RTMPServer {
	s := &RTMPServer{
		cfg:           cfg,
		streamRepo:    streamRepo,
		streamKeyRepo: streamKeyRepo,
		streamManager: streamManager,
		logger:        logger,
		activeStreams: make(map[string]*StreamSession),
		shutdownChan:  make(chan struct{}),
	}

	// Create RTMP server with authentication callback
	s.server = &rtmp.Server{
		HandlePublish: s.handlePublish,
		HandlePlay:    s.handlePlay,
	}

	return s
}

// Start starts the RTMP server
func (s *RTMPServer) Start(ctx context.Context) error {
	rtmpAddr := fmt.Sprintf("%s:%d", s.cfg.RTMPHost, s.cfg.RTMPPort)

	listener, err := net.Listen("tcp", rtmpAddr)
	if err != nil {
		return fmt.Errorf("failed to start RTMP server: %w", err)
	}

	s.listener = listener
	s.logger.WithField("address", rtmpAddr).Info("RTMP server started")

	// Start cleanup goroutine
	s.wg.Add(1)
	go s.cleanupRoutine(ctx)

	// Start accepting connections
	s.wg.Add(1)
	go s.acceptConnections(ctx)

	return nil
}

// Shutdown gracefully shuts down the RTMP server
func (s *RTMPServer) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down RTMP server...")
	close(s.shutdownChan)

	// Close listener to stop accepting new connections
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			s.logger.WithError(err).Error("Error closing RTMP listener")
		}
	}

	// End all active streams
	s.activeStreamsMu.Lock()
	for streamKey, session := range s.activeStreams {
		s.logger.WithField("stream_key", streamKey).Info("Ending active stream")
		if session.cancelFunc != nil {
			session.cancelFunc()
		}
	}
	s.activeStreamsMu.Unlock()

	// Wait for goroutines to finish
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("RTMP server shut down successfully")
		return nil
	case <-ctx.Done():
		s.logger.Warn("RTMP server shutdown timed out")
		return ctx.Err()
	}
}

func (s *RTMPServer) acceptConnections(ctx context.Context) {
	defer s.wg.Done()

	for {
		select {
		case <-s.shutdownChan:
			return
		case <-ctx.Done():
			return
		default:
		}

		// Set deadline to allow periodic checks for shutdown
		if err := s.listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second)); err != nil {
			s.logger.WithError(err).Error("Failed to set listener deadline")
			continue
		}

		conn, err := s.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Timeout is expected, continue loop
			}
			select {
			case <-s.shutdownChan:
				return // Server is shutting down
			default:
				s.logger.WithError(err).Error("Failed to accept RTMP connection")
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(ctx, conn)
	}
}

func (s *RTMPServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		if err := conn.Close(); err != nil {
			s.logger.WithError(err).Error("Failed to close RTMP connection")
		}
	}()

	// Wrap connection with RTMP protocol
	rtmpConn := rtmp.NewConn(conn)

	// Handle the connection using joy4's server
	s.server.HandleConn(rtmpConn)
}

func (s *RTMPServer) handlePublish(conn *rtmp.Conn) {
	streamKey := conn.URL.Path[1:] // Remove leading slash

	s.logger.WithFields(logrus.Fields{
		"stream_key": streamKey,
		"remote_ip":  conn.NetConn().RemoteAddr().String(),
	}).Info("RTMP publish request received")

	// Authenticate stream key
	ctx := context.Background()
	stream, err := s.authenticateStream(ctx, streamKey)
	if err != nil {
		s.logger.WithError(err).WithField("stream_key", streamKey).Warn("Stream authentication failed")
		return
	}

	// Check if stream is already active
	s.activeStreamsMu.RLock()
	if _, exists := s.activeStreams[streamKey]; exists {
		s.activeStreamsMu.RUnlock()
		s.logger.WithField("stream_key", streamKey).Warn("Stream is already active")
		return
	}
	s.activeStreamsMu.RUnlock()

	// Start the stream
	sessionCtx, cancel := context.WithCancel(ctx)
	session := &StreamSession{
		StreamID:   stream.ID,
		StreamKey:  streamKey,
		ChannelID:  stream.ChannelID,
		UserID:     stream.UserID,
		StartedAt:  time.Now(),
		Conn:       conn,
		cancelFunc: cancel,
	}

	s.activeStreamsMu.Lock()
	s.activeStreams[streamKey] = session
	s.activeStreamsMu.Unlock()

	// Update stream status to live
	if err := s.streamManager.StartStream(ctx, stream.ID); err != nil {
		s.logger.WithError(err).Error("Failed to start stream")
		cancel()
		return
	}

	s.logger.WithField("stream_id", stream.ID).Info("Stream started successfully")

	// Handle stream until it ends
	s.handleStreamSession(sessionCtx, session)

	// Cleanup
	s.activeStreamsMu.Lock()
	delete(s.activeStreams, streamKey)
	s.activeStreamsMu.Unlock()

	// End the stream
	if err := s.streamManager.EndStream(ctx, stream.ID); err != nil {
		s.logger.WithError(err).Error("Failed to end stream")
	}

	s.logger.WithField("stream_id", stream.ID).Info("Stream ended")
}

func (s *RTMPServer) handlePlay(conn *rtmp.Conn) {
	// For now, we don't support RTMP playback (only HLS)
	s.logger.WithField("remote_ip", conn.NetConn().RemoteAddr().String()).
		Warn("RTMP playback not supported, use HLS instead")
}

func (s *RTMPServer) authenticateStream(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	// Get stream by stream key
	stream, err := s.streamRepo.GetByStreamKey(ctx, streamKey)
	if err != nil {
		return nil, fmt.Errorf("stream not found: %w", err)
	}

	// Check if stream can start
	if !stream.CanStart() {
		return nil, fmt.Errorf("stream cannot start (status: %s)", stream.Status)
	}

	return stream, nil
}

func (s *RTMPServer) handleStreamSession(ctx context.Context, session *StreamSession) {
	// Read packets from RTMP connection and forward to HLS transcoder
	// This is where we'd integrate with FFmpeg for HLS transcoding

	// For now, just keep the connection alive until context is cancelled
	<-ctx.Done()
}

func (s *RTMPServer) cleanupRoutine(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdownChan:
			return
		case <-ticker.C:
			s.performCleanup(ctx)
		}
	}
}

func (s *RTMPServer) performCleanup(ctx context.Context) {
	// Check for stale streams (connected but not sending data)
	s.activeStreamsMu.RLock()
	defer s.activeStreamsMu.RUnlock()

	for streamKey, session := range s.activeStreams {
		// Check if stream has been running too long (if max duration is set)
		if s.cfg.MaxStreamDuration > 0 && time.Since(session.StartedAt) > s.cfg.MaxStreamDuration {
			s.logger.WithFields(logrus.Fields{
				"stream_key": streamKey,
				"stream_id":  session.StreamID,
				"duration":   time.Since(session.StartedAt),
			}).Warn("Stream exceeded maximum duration, ending")

			if session.cancelFunc != nil {
				session.cancelFunc()
			}
		}
	}
}

// GetActiveStreamCount returns the number of currently active streams
func (s *RTMPServer) GetActiveStreamCount() int {
	s.activeStreamsMu.RLock()
	defer s.activeStreamsMu.RUnlock()
	return len(s.activeStreams)
}

// GetActiveStreams returns a list of currently active stream sessions
func (s *RTMPServer) GetActiveStreams() []*StreamSession {
	s.activeStreamsMu.RLock()
	defer s.activeStreamsMu.RUnlock()

	sessions := make([]*StreamSession, 0, len(s.activeStreams))
	for _, session := range s.activeStreams {
		sessions = append(sessions, session)
	}
	return sessions
}

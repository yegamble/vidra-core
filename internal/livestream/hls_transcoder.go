package livestream

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
)

// QualityVariant defines a video quality preset for HLS transcoding
type QualityVariant struct {
	Name         string // "1080p", "720p", "480p", "360p"
	Width        int
	Height       int
	VideoBitrate int // Video bitrate in kbps
	AudioBitrate int // Audio bitrate in kbps
	MaxBitrate   int // Max bitrate for buffer calculation
	BufferSize   int // Buffer size in kbps
	Framerate    int // Target framerate
}

// GetQualityVariants returns the standard quality variants for HLS transcoding
func GetQualityVariants() []QualityVariant {
	return []QualityVariant{
		{
			Name:         "1080p",
			Width:        1920,
			Height:       1080,
			VideoBitrate: 5000,
			AudioBitrate: 128,
			MaxBitrate:   5350,
			BufferSize:   7500,
			Framerate:    30,
		},
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: 2800,
			AudioBitrate: 128,
			MaxBitrate:   2996,
			BufferSize:   4200,
			Framerate:    30,
		},
		{
			Name:         "480p",
			Width:        854,
			Height:       480,
			VideoBitrate: 1400,
			AudioBitrate: 128,
			MaxBitrate:   1498,
			BufferSize:   2100,
			Framerate:    30,
		},
		{
			Name:         "360p",
			Width:        640,
			Height:       360,
			VideoBitrate: 800,
			AudioBitrate: 128,
			MaxBitrate:   856,
			BufferSize:   1200,
			Framerate:    30,
		},
	}
}

// FilterVariantsByConfig filters quality variants based on configuration
func FilterVariantsByConfig(cfg *config.Config) []QualityVariant {
	allVariants := GetQualityVariants()
	if cfg.HLSVariants == "" || cfg.HLSVariants == "all" {
		return allVariants
	}

	// Parse comma-separated list
	enabled := make(map[string]bool)
	for _, v := range strings.Split(cfg.HLSVariants, ",") {
		enabled[strings.TrimSpace(v)] = true
	}

	// Filter variants
	filtered := []QualityVariant{}
	for _, v := range allVariants {
		if enabled[v.Name] {
			filtered = append(filtered, v)
		}
	}

	return filtered
}

// TranscodeSession represents an active HLS transcoding session
type TranscodeSession struct {
	StreamID      uuid.UUID
	FFmpegProcess *exec.Cmd
	OutputDir     string
	Variants      []QualityVariant
	StartedAt     time.Time
	SegmentCount  int
	Ctx           context.Context
	Cancel        context.CancelFunc
	RTMPUrl       string
}

// HLSTranscoder manages HLS transcoding for live streams
type HLSTranscoder struct {
	cfg           *config.Config
	streamRepo    repository.LiveStreamRepository
	logger        *logrus.Logger
	activeStreams map[uuid.UUID]*TranscodeSession
	mu            sync.RWMutex
	shutdownChan  chan struct{}
	wg            sync.WaitGroup
}

// NewHLSTranscoder creates a new HLS transcoder
func NewHLSTranscoder(
	cfg *config.Config,
	streamRepo repository.LiveStreamRepository,
	logger *logrus.Logger,
) *HLSTranscoder {
	return &HLSTranscoder{
		cfg:           cfg,
		streamRepo:    streamRepo,
		logger:        logger,
		activeStreams: make(map[uuid.UUID]*TranscodeSession),
		shutdownChan:  make(chan struct{}),
	}
}

// StartTranscoding starts HLS transcoding for a stream
func (t *HLSTranscoder) StartTranscoding(ctx context.Context, stream *domain.LiveStream, rtmpURL string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if already transcoding
	if _, exists := t.activeStreams[stream.ID]; exists {
		return fmt.Errorf("stream %s is already being transcoded", stream.ID)
	}

	// Create output directory
	outputDir := filepath.Join(t.cfg.HLSOutputDir, stream.ID.String())
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Get enabled variants
	variants := FilterVariantsByConfig(t.cfg)
	if len(variants) == 0 {
		return fmt.Errorf("no quality variants enabled")
	}

	// Create variant directories
	for _, v := range variants {
		variantDir := filepath.Join(outputDir, v.Name)
		if err := os.MkdirAll(variantDir, 0755); err != nil {
			return fmt.Errorf("failed to create variant directory %s: %w", v.Name, err)
		}
	}

	// Build FFmpeg command
	cmd := t.buildFFmpegCommand(rtmpURL, outputDir, variants)

	// Create session context
	sessionCtx, cancel := context.WithCancel(ctx)

	// Create session
	session := &TranscodeSession{
		StreamID:      stream.ID,
		FFmpegProcess: cmd,
		OutputDir:     outputDir,
		Variants:      variants,
		StartedAt:     time.Now(),
		Ctx:           sessionCtx,
		Cancel:        cancel,
		RTMPUrl:       rtmpURL,
	}

	// Store session
	t.activeStreams[stream.ID] = session

	// Start FFmpeg process
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.runFFmpegProcess(session)
	}()

	t.logger.WithFields(logrus.Fields{
		"stream_id": stream.ID,
		"variants":  len(variants),
		"output":    outputDir,
	}).Info("Started HLS transcoding")

	return nil
}

// buildFFmpegCommand builds the FFmpeg command for HLS transcoding
func (t *HLSTranscoder) buildFFmpegCommand(rtmpURL, outputDir string, variants []QualityVariant) *exec.Cmd {
	args := []string{
		"-i", rtmpURL,
		"-c:v", "libx264",
		"-preset", t.cfg.FFmpegPreset,
		"-tune", t.cfg.FFmpegTune,
		"-c:a", "aac",
		"-ar", "48000",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", t.cfg.LiveHLSSegmentLength),
		"-hls_list_size", fmt.Sprintf("%d", t.cfg.LiveHLSWindowSize),
		"-hls_flags", "delete_segments+append_list+program_date_time",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(outputDir, "%v", "segment_%03d.ts"),
		"-master_pl_name", "master.m3u8",
	}

	// Build variant stream map
	variantMap := []string{}
	for i := range variants {
		variantMap = append(variantMap, fmt.Sprintf("v:%d,a:%d", i, i))
	}
	args = append(args, "-var_stream_map", strings.Join(variantMap, " "))

	// Add per-variant encoding parameters
	for i, v := range variants {
		// Video encoding
		args = append(args,
			fmt.Sprintf("-s:v:%d", i), fmt.Sprintf("%dx%d", v.Width, v.Height),
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", v.VideoBitrate),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", v.MaxBitrate),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", v.BufferSize),
			fmt.Sprintf("-r:v:%d", i), fmt.Sprintf("%d", v.Framerate),
		)

		// Audio encoding
		args = append(args,
			fmt.Sprintf("-b:a:%d", i), fmt.Sprintf("%dk", v.AudioBitrate),
		)
	}

	// Output pattern
	args = append(args, filepath.Join(outputDir, "%v", "index.m3u8"))

	cmd := exec.Command(t.cfg.FFmpegPath, args...)
	cmd.Env = os.Environ()

	return cmd
}

// runFFmpegProcess runs the FFmpeg process and handles its lifecycle
func (t *HLSTranscoder) runFFmpegProcess(session *TranscodeSession) {
	// Capture output for logging
	session.FFmpegProcess.Stdout = os.Stdout
	session.FFmpegProcess.Stderr = os.Stderr

	t.logger.WithFields(logrus.Fields{
		"stream_id": session.StreamID,
		"command":   session.FFmpegProcess.String(),
	}).Debug("Starting FFmpeg process")

	// Start process
	if err := session.FFmpegProcess.Start(); err != nil {
		t.logger.WithError(err).WithField("stream_id", session.StreamID).Error("Failed to start FFmpeg")
		return
	}

	// Wait for process to complete or be cancelled
	done := make(chan error, 1)
	go func() {
		done <- session.FFmpegProcess.Wait()
	}()

	select {
	case <-session.Ctx.Done():
		// Context cancelled - kill process
		t.logger.WithField("stream_id", session.StreamID).Info("Stopping FFmpeg process")
		if err := session.FFmpegProcess.Process.Kill(); err != nil {
			t.logger.WithError(err).WithField("stream_id", session.StreamID).Warn("Failed to kill FFmpeg process")
		}
		<-done // Wait for process to exit

	case err := <-done:
		// Process exited
		if err != nil {
			t.logger.WithError(err).WithField("stream_id", session.StreamID).Error("FFmpeg process failed")
		} else {
			t.logger.WithField("stream_id", session.StreamID).Info("FFmpeg process completed")
		}
	}

	// Clean up session
	t.mu.Lock()
	delete(t.activeStreams, session.StreamID)
	t.mu.Unlock()
}

// StopTranscoding stops HLS transcoding for a stream
func (t *HLSTranscoder) StopTranscoding(streamID uuid.UUID) error {
	t.mu.Lock()
	session, exists := t.activeStreams[streamID]
	if !exists {
		t.mu.Unlock()
		return fmt.Errorf("stream %s is not being transcoded", streamID)
	}
	t.mu.Unlock()

	// Cancel session context to stop FFmpeg
	session.Cancel()

	t.logger.WithField("stream_id", streamID).Info("Stopped HLS transcoding")
	return nil
}

// GetSession returns the transcoding session for a stream
func (t *HLSTranscoder) GetSession(streamID uuid.UUID) (*TranscodeSession, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	session, exists := t.activeStreams[streamID]
	return session, exists
}

// IsTranscoding checks if a stream is currently being transcoded
func (t *HLSTranscoder) IsTranscoding(streamID uuid.UUID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, exists := t.activeStreams[streamID]
	return exists
}

// GetActiveStreamCount returns the number of streams being transcoded
func (t *HLSTranscoder) GetActiveStreamCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.activeStreams)
}

// Shutdown gracefully stops all transcoding sessions
func (t *HLSTranscoder) Shutdown(ctx context.Context) error {
	close(t.shutdownChan)

	// Stop all active sessions
	t.mu.Lock()
	sessions := make([]*TranscodeSession, 0, len(t.activeStreams))
	for _, session := range t.activeStreams {
		sessions = append(sessions, session)
	}
	t.mu.Unlock()

	// Cancel all sessions
	for _, session := range sessions {
		session.Cancel()
	}

	// Wait for all processes to stop (with timeout)
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.logger.Info("All HLS transcoding sessions stopped")
		return nil
	case <-ctx.Done():
		t.logger.Warn("Timeout waiting for HLS transcoding sessions to stop")
		return ctx.Err()
	}
}

// CleanupOldSegments removes old HLS segments based on the DVR window
func (t *HLSTranscoder) CleanupOldSegments(streamID uuid.UUID) error {
	_, exists := t.GetSession(streamID)
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	// FFmpeg handles segment deletion with the delete_segments flag
	// This is a no-op since we're using FFmpeg's automatic deletion
	t.logger.WithField("stream_id", streamID).Debug("Segment cleanup handled by FFmpeg")
	return nil
}

// GetHLSPlaylistURL returns the master playlist URL for a stream
func (t *HLSTranscoder) GetHLSPlaylistURL(streamID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/streams/%s/hls/master.m3u8", streamID)
}

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

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
)

type QualityVariant struct {
	Name         string
	Width        int
	Height       int
	VideoBitrate int
	AudioBitrate int
	MaxBitrate   int
	BufferSize   int
	Framerate    int
}

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

func FilterVariantsByConfig(cfg *config.Config) []QualityVariant {
	allVariants := GetQualityVariants()
	if cfg.HLSVariants == "" || cfg.HLSVariants == "all" {
		return allVariants
	}

	enabled := make(map[string]bool)
	for _, v := range strings.Split(cfg.HLSVariants, ",") {
		enabled[strings.TrimSpace(v)] = true
	}

	filtered := []QualityVariant{}
	for _, v := range allVariants {
		if enabled[v.Name] {
			filtered = append(filtered, v)
		}
	}

	return filtered
}

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

type HLSTranscoder struct {
	cfg           *config.Config
	streamRepo    repository.LiveStreamRepository
	logger        *logrus.Logger
	activeStreams map[uuid.UUID]*TranscodeSession
	mu            sync.RWMutex
	shutdownChan  chan struct{}
	wg            sync.WaitGroup
}

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

func (t *HLSTranscoder) StartTranscoding(ctx context.Context, stream *domain.LiveStream, rtmpURL string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.activeStreams[stream.ID]; exists {
		return fmt.Errorf("stream %s is already being transcoded", stream.ID)
	}

	outputDir := filepath.Join(t.cfg.HLSOutputDir, stream.ID.String())
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	variants := FilterVariantsByConfig(t.cfg)
	if len(variants) == 0 {
		return fmt.Errorf("no quality variants enabled")
	}

	for _, v := range variants {
		variantDir := filepath.Join(outputDir, v.Name)
		if err := os.MkdirAll(variantDir, 0750); err != nil {
			return fmt.Errorf("failed to create variant directory %s: %w", v.Name, err)
		}
	}

	cmd := t.buildFFmpegCommand(rtmpURL, outputDir, variants)

	sessionCtx, cancel := context.WithCancel(ctx)

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

	t.activeStreams[stream.ID] = session

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

	variantMap := []string{}
	for i := range variants {
		variantMap = append(variantMap, fmt.Sprintf("v:%d,a:%d", i, i))
	}
	args = append(args, "-var_stream_map", strings.Join(variantMap, " "))

	for i, v := range variants {
		args = append(args,
			fmt.Sprintf("-s:v:%d", i), fmt.Sprintf("%dx%d", v.Width, v.Height),
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", v.VideoBitrate),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", v.MaxBitrate),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", v.BufferSize),
			fmt.Sprintf("-r:v:%d", i), fmt.Sprintf("%d", v.Framerate),
		)

		args = append(args,
			fmt.Sprintf("-b:a:%d", i), fmt.Sprintf("%dk", v.AudioBitrate),
		)
	}

	args = append(args, filepath.Join(outputDir, "%v", "index.m3u8"))

	cmd := exec.Command(t.cfg.FFmpegPath, args...)
	cmd.Env = os.Environ()

	return cmd
}

func (t *HLSTranscoder) runFFmpegProcess(session *TranscodeSession) {
	session.FFmpegProcess.Stdout = os.Stdout
	session.FFmpegProcess.Stderr = os.Stderr

	t.logger.WithFields(logrus.Fields{
		"stream_id": session.StreamID,
		"command":   session.FFmpegProcess.String(),
	}).Debug("Starting FFmpeg process")

	if err := session.FFmpegProcess.Start(); err != nil {
		t.logger.WithError(err).WithField("stream_id", session.StreamID).Error("Failed to start FFmpeg")
		return
	}

	done := make(chan error, 1)
	go func() {
		done <- session.FFmpegProcess.Wait()
	}()

	select {
	case <-session.Ctx.Done():
		t.logger.WithField("stream_id", session.StreamID).Info("Stopping FFmpeg process")
		if err := session.FFmpegProcess.Process.Kill(); err != nil {
			t.logger.WithError(err).WithField("stream_id", session.StreamID).Warn("Failed to kill FFmpeg process")
		}
		<-done

	case err := <-done:
		if err != nil {
			t.logger.WithError(err).WithField("stream_id", session.StreamID).Error("FFmpeg process failed")
		} else {
			t.logger.WithField("stream_id", session.StreamID).Info("FFmpeg process completed")
		}
	}

	t.mu.Lock()
	delete(t.activeStreams, session.StreamID)
	t.mu.Unlock()
}

func (t *HLSTranscoder) StopTranscoding(streamID uuid.UUID) error {
	t.mu.Lock()
	session, exists := t.activeStreams[streamID]
	if !exists {
		t.mu.Unlock()
		return fmt.Errorf("stream %s is not being transcoded", streamID)
	}
	t.mu.Unlock()

	session.Cancel()

	t.logger.WithField("stream_id", streamID).Info("Stopped HLS transcoding")
	return nil
}

func (t *HLSTranscoder) GetSession(streamID uuid.UUID) (*TranscodeSession, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	session, exists := t.activeStreams[streamID]
	return session, exists
}

func (t *HLSTranscoder) IsTranscoding(streamID uuid.UUID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, exists := t.activeStreams[streamID]
	return exists
}

func (t *HLSTranscoder) GetActiveStreamCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.activeStreams)
}

func (t *HLSTranscoder) Shutdown(ctx context.Context) error {
	close(t.shutdownChan)

	t.mu.Lock()
	sessions := make([]*TranscodeSession, 0, len(t.activeStreams))
	for _, session := range t.activeStreams {
		sessions = append(sessions, session)
	}
	t.mu.Unlock()

	for _, session := range sessions {
		session.Cancel()
	}

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

func (t *HLSTranscoder) GetHLSPlaylistURL(streamID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/streams/%s/hls/master.m3u8", streamID)
}

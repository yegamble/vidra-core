package encoding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"athena/internal/domain"
)

// CodecEncoder defines the interface for codec-specific encoding
type CodecEncoder interface {
	// Name returns the codec name (h264, vp9, av1)
	Name() string
	// SupportsResolution checks if this codec supports the given resolution
	SupportsResolution(resolution string) bool
	// Encode encodes the input file to HLS format
	Encode(ctx context.Context, input string, height int, outPlaylist string, segPattern string) error
	// GetCodecsString returns the CODECS parameter for HLS manifests
	GetCodecsString(height int) string
}

// H264Encoder implements CodecEncoder for H.264
type H264Encoder struct {
	service *service
}

func NewH264Encoder(s *service) *H264Encoder {
	return &H264Encoder{service: s}
}

func (e *H264Encoder) Name() string {
	return "h264"
}

func (e *H264Encoder) SupportsResolution(resolution string) bool {
	// H.264 supports all resolutions
	return true
}

func (e *H264Encoder) Encode(ctx context.Context, input string, height int, outPlaylist string, segPattern string) error {
	args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-profile:v", "high",
		"-level", "4.0",
		"-movflags", "+faststart",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", e.service.cfg.HLSSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		outPlaylist,
	}
	return e.service.execFFmpeg(ctx, args)
}

func (e *H264Encoder) GetCodecsString(height int) string {
	// H.264 High Profile Level 4.0
	return "avc1.640028,mp4a.40.2"
}

// VP9Encoder implements CodecEncoder for VP9
type VP9Encoder struct {
	service *service
}

func NewVP9Encoder(s *service) *VP9Encoder {
	return &VP9Encoder{service: s}
}

func (e *VP9Encoder) Name() string {
	return "vp9"
}

func (e *VP9Encoder) SupportsResolution(resolution string) bool {
	// VP9 supports all resolutions
	return true
}

func (e *VP9Encoder) Encode(ctx context.Context, input string, height int, outPlaylist string, segPattern string) error {
	// VP9 encoding requires two-pass for optimal quality
	tmpDir := filepath.Dir(outPlaylist)
	passLogFile := filepath.Join(tmpDir, "ffmpeg2pass")

	// First pass - analyze
	pass1Args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libvpx-vp9",
		"-b:v", "0", // Use CRF mode
		"-crf", fmt.Sprintf("%d", e.service.cfg.VP9Quality),
		"-cpu-used", fmt.Sprintf("%d", e.service.cfg.VP9Speed),
		"-row-mt", "1",
		"-threads", "4",
		"-tile-columns", "2",
		"-tile-rows", "1",
		"-pass", "1",
		"-passlogfile", passLogFile,
		"-an", // No audio in first pass
		"-f", "null",
		"-", // Output to /dev/null
	}

	if err := e.service.execFFmpeg(ctx, pass1Args); err != nil {
		return fmt.Errorf("vp9 pass 1 failed: %w", err)
	}

	// Second pass - encode
	pass2Args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libvpx-vp9",
		"-b:v", "0",
		"-crf", fmt.Sprintf("%d", e.service.cfg.VP9Quality),
		"-cpu-used", fmt.Sprintf("%d", e.service.cfg.VP9Speed),
		"-row-mt", "1",
		"-threads", "4",
		"-tile-columns", "2",
		"-tile-rows", "1",
		"-pass", "2",
		"-passlogfile", passLogFile,
		"-c:a", "libopus",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", e.service.cfg.HLSSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		outPlaylist,
	}

	if err := e.service.execFFmpeg(ctx, pass2Args); err != nil {
		return fmt.Errorf("vp9 pass 2 failed: %w", err)
	}

	// Cleanup pass log files
	_ = os.Remove(passLogFile + "-0.log")
	_ = os.Remove(passLogFile + "-0.log.mbtree")

	return nil
}

func (e *VP9Encoder) GetCodecsString(height int) string {
	// VP9 Profile 0, Level determined by resolution
	// Format: vp09.PP.LL.DD.CC
	// PP = Profile (00 for Profile 0)
	// LL = Level
	// DD = Bit depth (08 for 8-bit)
	// CC = Chroma subsampling (01 for 4:2:0)

	level := "31" // Default level 3.1
	if height >= 2160 {
		level = "51" // 4K
	} else if height >= 1440 {
		level = "50" // 2K
	} else if height >= 1080 {
		level = "40" // 1080p
	} else if height >= 720 {
		level = "31" // 720p
	}

	return fmt.Sprintf("vp09.00.%s.08.01,opus", level)
}

// AV1Encoder implements CodecEncoder for AV1
type AV1Encoder struct {
	service *service
}

func NewAV1Encoder(s *service) *AV1Encoder {
	return &AV1Encoder{service: s}
}

func (e *AV1Encoder) Name() string {
	return "av1"
}

func (e *AV1Encoder) SupportsResolution(resolution string) bool {
	// AV1 supports all resolutions
	return true
}

func (e *AV1Encoder) Encode(ctx context.Context, input string, height int, outPlaylist string, segPattern string) error {
	// AV1 encoding with SVT-AV1 (faster) or libaom (better quality)
	// Using libaom for now as it's more widely available

	tmpDir := filepath.Dir(outPlaylist)
	passLogFile := filepath.Join(tmpDir, "ffmpeg2pass-av1")

	// First pass
	pass1Args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libaom-av1",
		"-cpu-used", fmt.Sprintf("%d", e.service.cfg.AV1Preset),
		"-crf", fmt.Sprintf("%d", e.service.cfg.AV1CRF),
		"-b:v", "0",
		"-strict", "experimental",
		"-pass", "1",
		"-passlogfile", passLogFile,
		"-an",
		"-f", "null",
		"-",
	}

	if err := e.service.execFFmpeg(ctx, pass1Args); err != nil {
		return fmt.Errorf("av1 pass 1 failed: %w", err)
	}

	// Second pass
	pass2Args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libaom-av1",
		"-cpu-used", fmt.Sprintf("%d", e.service.cfg.AV1Preset),
		"-crf", fmt.Sprintf("%d", e.service.cfg.AV1CRF),
		"-b:v", "0",
		"-strict", "experimental",
		"-pass", "2",
		"-passlogfile", passLogFile,
		"-c:a", "libopus",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", e.service.cfg.HLSSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		outPlaylist,
	}

	if err := e.service.execFFmpeg(ctx, pass2Args); err != nil {
		return fmt.Errorf("av1 pass 2 failed: %w", err)
	}

	// Cleanup
	_ = os.Remove(passLogFile + "-0.log")
	_ = os.Remove(passLogFile + "-0.log.mbtree")

	return nil
}

func (e *AV1Encoder) GetCodecsString(height int) string {
	// AV1 codec string format: av01.P.LLT.DD.C.CC.cp.tc.mc.F
	// Simplified version for HLS
	return "av01.0.05M.08,opus"
}

// GetCodecEncoder returns the appropriate encoder for the given codec name
func (s *service) GetCodecEncoder(codecName string) (CodecEncoder, error) {
	switch strings.ToLower(codecName) {
	case "h264", "avc", "":
		return NewH264Encoder(s), nil
	case "vp9", "vp09":
		if !s.cfg.EnableVP9 {
			return nil, fmt.Errorf("VP9 encoding is not enabled")
		}
		return NewVP9Encoder(s), nil
	case "av1", "av01":
		if !s.cfg.EnableAV1 {
			return nil, fmt.Errorf("AV1 encoding is not enabled")
		}
		return NewAV1Encoder(s), nil
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codecName)
	}
}

// GetEnabledCodecs returns a list of enabled codecs based on configuration
func (s *service) GetEnabledCodecs() []string {
	codecs := []string{"h264"} // H.264 is always enabled

	if s.cfg.EnableVP9 {
		codecs = append(codecs, "vp9")
	}

	if s.cfg.EnableAV1 {
		codecs = append(codecs, "av1")
	}

	return codecs
}

// transcodeHLSWithCodec encodes a video using the specified codec
// nolint:unused // Will be used in Sprint 3 for live streaming integration
func (s *service) transcodeHLSWithCodec(ctx context.Context, codec string, input string, height int, outPlaylist string, segPattern string) error {
	encoder, err := s.GetCodecEncoder(codec)
	if err != nil {
		return err
	}

	return encoder.Encode(ctx, input, height, outPlaylist, segPattern)
}

// encodeResolutionsMultiCodec encodes all target resolutions for multiple codecs
// nolint:unused // Will be used in Sprint 3 for live streaming integration
func (s *service) encodeResolutionsMultiCodec(ctx context.Context, job *domain.EncodingJob, baseOutDir string, codecs []string, update func()) error {
	for _, codec := range codecs {
		codecDir := filepath.Join(baseOutDir, codec)
		if err := os.MkdirAll(codecDir, 0o750); err != nil {
			return fmt.Errorf("failed to create codec dir %s: %w", codec, err)
		}

		// Encode all resolutions for this codec
		if err := s.encodeResolutionsForCodec(ctx, job, codecDir, codec, update); err != nil {
			return fmt.Errorf("encoding %s failed: %w", codec, err)
		}
	}

	return nil
}

// encodeResolutionsForCodec encodes all resolutions for a single codec
// nolint:unused // Will be used in Sprint 3 for live streaming integration
func (s *service) encodeResolutionsForCodec(ctx context.Context, job *domain.EncodingJob, codecDir string, codec string, update func()) error {
	encoder, err := s.GetCodecEncoder(codec)
	if err != nil {
		return err
	}

	for _, res := range job.TargetResolutions {
		height, ok := domain.HeightForResolution(res)
		if !ok || !encoder.SupportsResolution(res) {
			continue
		}

		resDir := filepath.Join(codecDir, fmt.Sprintf("%dp", height))
		if err := os.MkdirAll(resDir, 0o750); err != nil {
			return err
		}

		outPlaylist := filepath.Join(resDir, "stream.m3u8")
		segPattern := filepath.Join(resDir, "segment_%05d.ts")

		if err := encoder.Encode(ctx, job.SourceFilePath, height, outPlaylist, segPattern); err != nil {
			return fmt.Errorf("encode %s %s: %w", codec, res, err)
		}

		update()
	}

	return nil
}

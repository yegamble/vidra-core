package encoding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vidra-core/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiCodecPlaylistGenerator(t *testing.T) {
	cfg := &config.Config{
		EnableVP9:  true,
		EnableAV1:  true,
		VP9Quality: 31,
		VP9Speed:   2,
		AV1Preset:  6,
		AV1CRF:     30,
	}

	svc := &service{cfg: cfg}
	gen := NewMultiCodecPlaylistGenerator(svc)

	t.Run("Adjust bandwidth for codec", func(t *testing.T) {
		baseBandwidth := 5000000 // 5 Mbps for 1080p H.264

		h264Bandwidth := gen.adjustBandwidthForCodec("h264", baseBandwidth)
		assert.Equal(t, baseBandwidth, h264Bandwidth)

		vp9Bandwidth := gen.adjustBandwidthForCodec("vp9", baseBandwidth)
		assert.Equal(t, 3500000, vp9Bandwidth) // 70% of baseline

		av1Bandwidth := gen.adjustBandwidthForCodec("av1", baseBandwidth)
		assert.Equal(t, 2500000, av1Bandwidth) // 50% of baseline
	})

	t.Run("Calculate width from height", func(t *testing.T) {
		tests := []struct {
			height int
			want   int
		}{
			{720, 1280},
			{1080, 1920},
			{2160, 3840},
		}

		for _, tt := range tests {
			width := gen.calculateWidth(tt.height)
			assert.Equal(t, tt.want, width, "Width for height %d", tt.height)
		}
	})
}

func TestGenerateMultiCodecMasterPlaylist(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()

	cfg := &config.Config{
		EnableVP9:          true,
		EnableAV1:          false,
		HLSSegmentDuration: 4,
		VP9Quality:         31,
		VP9Speed:           2,
	}

	svc := &service{cfg: cfg}
	gen := NewMultiCodecPlaylistGenerator(svc)

	// Create mock codec directories and playlists
	resolutions := []string{"720p", "1080p"}
	codecs := []string{"h264", "vp9"}

	for _, codec := range codecs {
		for _, res := range resolutions {
			dir := filepath.Join(tmpDir, codec, res)
			require.NoError(t, os.MkdirAll(dir, 0o750))

			// Create mock playlist file
			playlistPath := filepath.Join(dir, "stream.m3u8")
			require.NoError(t, os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0o600))
		}
	}

	// Generate master playlist
	err := gen.GenerateMultiCodecMasterPlaylist(tmpDir, resolutions, codecs)
	require.NoError(t, err)

	// Read and verify master playlist
	masterPath := filepath.Join(tmpDir, "master.m3u8")
	content, err := os.ReadFile(masterPath)
	require.NoError(t, err)

	playlist := string(content)

	// Verify playlist structure
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:7")

	// Verify H.264 variants
	assert.Contains(t, playlist, "h264/720p/stream.m3u8")
	assert.Contains(t, playlist, "h264/1080p/stream.m3u8")

	// Verify VP9 variants
	assert.Contains(t, playlist, "vp9/720p/stream.m3u8")
	assert.Contains(t, playlist, "vp9/1080p/stream.m3u8")

	// Verify codec strings
	assert.Contains(t, playlist, "avc1") // H.264
	assert.Contains(t, playlist, "vp09") // VP9

	// Verify bandwidth adjustment (VP9 should be ~70% of H.264)
	lines := strings.Split(playlist, "\n")
	var h264_1080_bandwidth, vp9_1080_bandwidth int
	for i, line := range lines {
		if strings.Contains(line, "1080p H264") && i > 0 {
			prevLine := lines[i-1]
			if strings.Contains(prevLine, "BANDWIDTH=") {
				_, _ = fmt.Sscanf(prevLine, "#EXT-X-STREAM-INF:BANDWIDTH=%d", &h264_1080_bandwidth)
			}
		}
		if strings.Contains(line, "1080p VP9") && i > 0 {
			prevLine := lines[i-1]
			if strings.Contains(prevLine, "BANDWIDTH=") {
				_, _ = fmt.Sscanf(prevLine, "#EXT-X-STREAM-INF:BANDWIDTH=%d", &vp9_1080_bandwidth)
			}
		}
	}

	if h264_1080_bandwidth > 0 && vp9_1080_bandwidth > 0 {
		ratio := float64(vp9_1080_bandwidth) / float64(h264_1080_bandwidth)
		assert.InDelta(t, 0.7, ratio, 0.01, "VP9 bandwidth should be ~70% of H.264")
	}
}

func TestGenerateLegacyMasterPlaylist(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		EnableVP9: false,
		EnableAV1: false,
	}

	svc := &service{cfg: cfg}
	gen := NewMultiCodecPlaylistGenerator(svc)

	// Create mock H.264 playlists only
	resolutions := []string{"720p", "1080p"}
	for _, res := range resolutions {
		dir := filepath.Join(tmpDir, res)
		require.NoError(t, os.MkdirAll(dir, 0o750))

		playlistPath := filepath.Join(dir, "stream.m3u8")
		require.NoError(t, os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0o600))
	}

	// Generate legacy playlist
	err := gen.GenerateLegacyMasterPlaylist(tmpDir, resolutions)
	require.NoError(t, err)

	// Read and verify
	masterPath := filepath.Join(tmpDir, "master.m3u8")
	content, err := os.ReadFile(masterPath)
	require.NoError(t, err)

	playlist := string(content)

	// Verify simpler structure (version 3, not 7)
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:3")
	assert.NotContains(t, playlist, "#EXT-X-VERSION:7")

	// Verify variants
	assert.Contains(t, playlist, "720p/stream.m3u8")
	assert.Contains(t, playlist, "1080p/stream.m3u8")

	// Should not contain codec strings in legacy mode
	assert.NotContains(t, playlist, "CODECS=")
}

func TestGenerateCodecSpecificMasterPlaylist(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		EnableVP9:  true,
		VP9Quality: 31,
		VP9Speed:   2,
	}

	svc := &service{cfg: cfg}
	gen := NewMultiCodecPlaylistGenerator(svc)

	// Create VP9 codec directory structure
	codecDir := filepath.Join(tmpDir, "vp9")
	resolutions := []string{"720p", "1080p"}

	for _, res := range resolutions {
		dir := filepath.Join(codecDir, res)
		require.NoError(t, os.MkdirAll(dir, 0o750))

		playlistPath := filepath.Join(dir, "stream.m3u8")
		require.NoError(t, os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0o600))
	}

	// Generate codec-specific master playlist
	err := gen.GenerateCodecSpecificMasterPlaylist(codecDir, "vp9", resolutions)
	require.NoError(t, err)

	// Read and verify
	masterPath := filepath.Join(codecDir, "master.m3u8")
	content, err := os.ReadFile(masterPath)
	require.NoError(t, err)

	playlist := string(content)

	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:7")
	assert.Contains(t, playlist, "720p/stream.m3u8")
	assert.Contains(t, playlist, "1080p/stream.m3u8")
	assert.Contains(t, playlist, "vp09") // VP9 codec string
}

func TestDetectAvailableCodecs(t *testing.T) {
	t.Run("All codecs available", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create all codec directories
		for _, codec := range []string{"h264", "vp9", "av1"} {
			codecDir := filepath.Join(tmpDir, codec)
			require.NoError(t, os.MkdirAll(codecDir, 0o750))
		}

		codecs := DetectAvailableCodecs(tmpDir)
		assert.ElementsMatch(t, []string{"h264", "vp9", "av1"}, codecs)
	})

	t.Run("Only H264 available", func(t *testing.T) {
		tmpDir := t.TempDir()

		codecDir := filepath.Join(tmpDir, "h264")
		require.NoError(t, os.MkdirAll(codecDir, 0o750))

		codecs := DetectAvailableCodecs(tmpDir)
		assert.Equal(t, []string{"h264"}, codecs)
	})

	t.Run("Legacy structure (no codec directories)", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create resolution directories directly (legacy structure)
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "720p"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "1080p"), 0o750))

		codecs := DetectAvailableCodecs(tmpDir)
		assert.Equal(t, []string{"h264"}, codecs)
	})

	t.Run("Mixed H264 and VP9", func(t *testing.T) {
		tmpDir := t.TempDir()

		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "h264"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "vp9"), 0o750))

		codecs := DetectAvailableCodecs(tmpDir)
		assert.ElementsMatch(t, []string{"h264", "vp9"}, codecs)
	})
}

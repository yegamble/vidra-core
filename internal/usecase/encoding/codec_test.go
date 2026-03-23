package encoding

import (
	"context"
	"testing"

	"vidra-core/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodecEncoders(t *testing.T) {
	// Create a test service
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
		EnableVP9:          true,
		VP9Quality:         31,
		VP9Speed:           2,
		EnableAV1:          true,
		AV1Preset:          6,
		AV1CRF:             30,
	}

	svc := &service{
		cfg: cfg,
	}

	t.Run("H264 Encoder", func(t *testing.T) {
		encoder := NewH264Encoder(svc)

		assert.Equal(t, "h264", encoder.Name())
		assert.True(t, encoder.SupportsResolution("720p"))
		assert.True(t, encoder.SupportsResolution("1080p"))
		assert.True(t, encoder.SupportsResolution("4320p"))

		codecString := encoder.GetCodecsString(1080)
		assert.Contains(t, codecString, "avc1")
		assert.Contains(t, codecString, "mp4a")
	})

	t.Run("VP9 Encoder", func(t *testing.T) {
		encoder := NewVP9Encoder(svc)

		assert.Equal(t, "vp9", encoder.Name())
		assert.True(t, encoder.SupportsResolution("720p"))
		assert.True(t, encoder.SupportsResolution("1080p"))

		// Test codec strings for different resolutions
		codec720p := encoder.GetCodecsString(720)
		assert.Contains(t, codec720p, "vp09")
		assert.Contains(t, codec720p, "opus")

		codec1080p := encoder.GetCodecsString(1080)
		assert.Contains(t, codec1080p, "vp09")
		assert.Contains(t, codec1080p, "opus")

		codec2160p := encoder.GetCodecsString(2160)
		assert.Contains(t, codec2160p, "vp09")
		assert.Contains(t, codec2160p, "51") // Level for 4K
	})

	t.Run("AV1 Encoder", func(t *testing.T) {
		encoder := NewAV1Encoder(svc)

		assert.Equal(t, "av1", encoder.Name())
		assert.True(t, encoder.SupportsResolution("720p"))
		assert.True(t, encoder.SupportsResolution("1080p"))

		codecString := encoder.GetCodecsString(1080)
		assert.Contains(t, codecString, "av01")
		assert.Contains(t, codecString, "opus")
	})
}

func TestGetCodecEncoder(t *testing.T) {
	cfg := &config.Config{
		EnableVP9:  true,
		EnableAV1:  true,
		VP9Quality: 31,
		VP9Speed:   2,
		AV1Preset:  6,
		AV1CRF:     30,
	}

	svc := &service{cfg: cfg}

	tests := []struct {
		name      string
		codecName string
		wantName  string
		wantErr   bool
	}{
		{
			name:      "H264",
			codecName: "h264",
			wantName:  "h264",
			wantErr:   false,
		},
		{
			name:      "H264 empty",
			codecName: "",
			wantName:  "h264",
			wantErr:   false,
		},
		{
			name:      "VP9",
			codecName: "vp9",
			wantName:  "vp9",
			wantErr:   false,
		},
		{
			name:      "AV1",
			codecName: "av1",
			wantName:  "av1",
			wantErr:   false,
		},
		{
			name:      "Unsupported codec",
			codecName: "hevc",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoder, err := svc.GetCodecEncoder(tt.codecName)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, encoder)
			} else {
				require.NoError(t, err)
				require.NotNil(t, encoder)
				assert.Equal(t, tt.wantName, encoder.Name())
			}
		})
	}
}

func TestGetCodecEncoder_DisabledCodecs(t *testing.T) {
	cfg := &config.Config{
		EnableVP9: false,
		EnableAV1: false,
	}

	svc := &service{cfg: cfg}

	t.Run("VP9 disabled", func(t *testing.T) {
		encoder, err := svc.GetCodecEncoder("vp9")
		assert.Error(t, err)
		assert.Nil(t, encoder)
		assert.Contains(t, err.Error(), "not enabled")
	})

	t.Run("AV1 disabled", func(t *testing.T) {
		encoder, err := svc.GetCodecEncoder("av1")
		assert.Error(t, err)
		assert.Nil(t, encoder)
		assert.Contains(t, err.Error(), "not enabled")
	})

	t.Run("H264 always enabled", func(t *testing.T) {
		encoder, err := svc.GetCodecEncoder("h264")
		assert.NoError(t, err)
		assert.NotNil(t, encoder)
	})
}

func TestGetEnabledCodecs(t *testing.T) {
	tests := []struct {
		name       string
		enableVP9  bool
		enableAV1  bool
		wantCodecs []string
	}{
		{
			name:       "H264 only",
			enableVP9:  false,
			enableAV1:  false,
			wantCodecs: []string{"h264"},
		},
		{
			name:       "H264 and VP9",
			enableVP9:  true,
			enableAV1:  false,
			wantCodecs: []string{"h264", "vp9"},
		},
		{
			name:       "All codecs",
			enableVP9:  true,
			enableAV1:  true,
			wantCodecs: []string{"h264", "vp9", "av1"},
		},
		{
			name:       "H264 and AV1",
			enableVP9:  false,
			enableAV1:  true,
			wantCodecs: []string{"h264", "av1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				EnableVP9: tt.enableVP9,
				EnableAV1: tt.enableAV1,
			}

			svc := &service{cfg: cfg}
			codecs := svc.GetEnabledCodecs()

			assert.Equal(t, tt.wantCodecs, codecs)
		})
	}
}

func TestTranscodeHLSWithCodec(t *testing.T) {
	// This test requires ffmpeg to be installed, so we skip if not available
	ctx := context.Background()

	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
		EnableVP9:          true,
		VP9Quality:         31,
		VP9Speed:           2,
	}

	svc := &service{cfg: cfg}

	t.Run("Valid codec name", func(t *testing.T) {
		// We don't actually encode (would require test video file)
		// Just test that the function accepts valid codec names
		encoder, err := svc.GetCodecEncoder("h264")
		require.NoError(t, err)
		assert.NotNil(t, encoder)
	})

	t.Run("Case insensitive codec names", func(t *testing.T) {
		encoder, err := svc.GetCodecEncoder("VP9")
		require.NoError(t, err)
		assert.Equal(t, "vp9", encoder.Name())
	})

	// Note: Actual encoding tests would require:
	// 1. A test video file
	// 2. ffmpeg installed
	// 3. Temporary output directory
	// These are better suited for integration tests
	_ = ctx // silence unused warning
}

func TestCodecStrings(t *testing.T) {
	cfg := &config.Config{
		EnableVP9:  true,
		EnableAV1:  true,
		VP9Quality: 31,
		VP9Speed:   2,
		AV1Preset:  6,
		AV1CRF:     30,
	}

	svc := &service{cfg: cfg}

	t.Run("H264 codec string", func(t *testing.T) {
		encoder := NewH264Encoder(svc)
		codecStr := encoder.GetCodecsString(1080)

		// Should contain H.264 baseline profile
		assert.Contains(t, codecStr, "avc1")
		// Should contain AAC audio
		assert.Contains(t, codecStr, "mp4a")
	})

	t.Run("VP9 codec string varies by resolution", func(t *testing.T) {
		encoder := NewVP9Encoder(svc)

		// 720p should have level 31
		codec720p := encoder.GetCodecsString(720)
		assert.Contains(t, codec720p, "vp09.00.31")

		// 1080p should have level 40
		codec1080p := encoder.GetCodecsString(1080)
		assert.Contains(t, codec1080p, "vp09.00.40")

		// 4K should have level 51
		codec4k := encoder.GetCodecsString(2160)
		assert.Contains(t, codec4k, "vp09.00.51")
	})

	t.Run("AV1 codec string", func(t *testing.T) {
		encoder := NewAV1Encoder(svc)
		codecStr := encoder.GetCodecsString(1080)

		assert.Contains(t, codecStr, "av01")
		assert.Contains(t, codecStr, "opus")
	})
}

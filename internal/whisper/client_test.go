package whisper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Create mock binaries
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	whisperPath := filepath.Join(tempDir, "whisper")
	modelsDir := filepath.Join(tempDir, "models")

	err := os.WriteFile(ffmpegPath, []byte("fake ffmpeg"), 0755)
	require.NoError(t, err)
	err = os.WriteFile(whisperPath, []byte("fake whisper"), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(modelsDir, 0755)
	require.NoError(t, err)

	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
			errMsg:  "config is nil",
		},
		{
			name: "invalid provider",
			config: &Config{
				Provider:  "invalid",
				ModelSize: domain.WhisperModelSizeBase,
			},
			wantErr: true,
			errMsg:  "invalid provider",
		},
		{
			name: "invalid model size",
			config: &Config{
				Provider:  domain.WhisperProviderLocal,
				ModelSize: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid model size",
		},
		{
			name: "local provider without whisper.cpp path",
			config: &Config{
				Provider:   domain.WhisperProviderLocal,
				ModelSize:  domain.WhisperModelSizeBase,
				ModelsDir:  modelsDir,
				FFmpegPath: ffmpegPath,
			},
			wantErr: true,
			errMsg:  "whisper.cpp path is required",
		},
		{
			name: "local provider without models dir",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				FFmpegPath:     ffmpegPath,
			},
			wantErr: true,
			errMsg:  "models directory is required",
		},
		{
			name: "local provider with non-existent whisper.cpp",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: "/nonexistent/whisper",
				ModelsDir:      modelsDir,
				FFmpegPath:     ffmpegPath,
			},
			wantErr: true,
			errMsg:  "whisper.cpp binary not found",
		},
		{
			name: "local provider with non-existent models dir",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      "/nonexistent/models",
				FFmpegPath:     ffmpegPath,
			},
			wantErr: true,
			errMsg:  "models directory not found",
		},
		{
			name: "local provider with HTTP API URL",
			config: &Config{
				Provider:      domain.WhisperProviderLocal,
				ModelSize:     domain.WhisperModelSizeBase,
				WhisperAPIURL: "http://localhost:8000",
				FFmpegPath:    ffmpegPath,
			},
			wantErr: false,
		},
		{
			name: "openai provider without API key",
			config: &Config{
				Provider:   domain.WhisperProviderOpenAI,
				ModelSize:  domain.WhisperModelSizeBase,
				FFmpegPath: ffmpegPath,
			},
			wantErr: true,
			errMsg:  "OpenAI API key is required",
		},
		{
			name: "openai provider with valid config",
			config: &Config{
				Provider:     domain.WhisperProviderOpenAI,
				ModelSize:    domain.WhisperModelSizeBase,
				OpenAIAPIKey: "sk-test-key",
				FFmpegPath:   ffmpegPath,
			},
			wantErr: false,
		},
		{
			name: "missing ffmpeg path",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      modelsDir,
			},
			wantErr: true,
			errMsg:  "ffmpeg path is required",
		},
		{
			name: "non-existent ffmpeg",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      modelsDir,
				FFmpegPath:     "/nonexistent/ffmpeg",
			},
			wantErr: true,
			errMsg:  "ffmpeg binary not found",
		},
		{
			name: "valid local provider config",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      modelsDir,
				FFmpegPath:     ffmpegPath,
			},
			wantErr: false,
		},
		{
			name: "temp dir created if not provided",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      modelsDir,
				FFmpegPath:     ffmpegPath,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.config.TempDir == "" {
					assert.NotEmpty(t, tt.config.TempDir, "TempDir should be set to default")
				}
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	tempDir := t.TempDir()

	// Create mock binaries
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	whisperPath := filepath.Join(tempDir, "whisper")
	modelsDir := filepath.Join(tempDir, "models")

	err := os.WriteFile(ffmpegPath, []byte("fake ffmpeg"), 0755)
	require.NoError(t, err)
	err = os.WriteFile(whisperPath, []byte("fake whisper"), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(modelsDir, 0755)
	require.NoError(t, err)

	tests := []struct {
		name         string
		config       *Config
		wantProvider domain.WhisperProvider
		wantErr      bool
	}{
		{
			name: "create openai client",
			config: &Config{
				Provider:     domain.WhisperProviderOpenAI,
				ModelSize:    domain.WhisperModelSizeBase,
				OpenAIAPIKey: "sk-test-key",
				FFmpegPath:   ffmpegPath,
			},
			wantProvider: domain.WhisperProviderOpenAI,
			wantErr:      false,
		},
		{
			name: "create local client with whisper.cpp",
			config: &Config{
				Provider:       domain.WhisperProviderLocal,
				ModelSize:      domain.WhisperModelSizeBase,
				WhisperCppPath: whisperPath,
				ModelsDir:      modelsDir,
				FFmpegPath:     ffmpegPath,
			},
			wantProvider: domain.WhisperProviderLocal,
			wantErr:      false,
		},
		{
			name: "create local client with HTTP API",
			config: &Config{
				Provider:      domain.WhisperProviderLocal,
				ModelSize:     domain.WhisperModelSizeBase,
				WhisperAPIURL: "http://localhost:8000",
				FFmpegPath:    ffmpegPath,
			},
			wantProvider: domain.WhisperProviderLocal,
			wantErr:      false,
		},
		{
			name: "invalid config",
			config: &Config{
				Provider:  "invalid",
				ModelSize: domain.WhisperModelSizeBase,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)
				assert.Equal(t, tt.wantProvider, client.GetProvider())
			}
		})
	}
}

func TestGetModelPath(t *testing.T) {
	tests := []struct {
		name      string
		modelsDir string
		modelSize domain.WhisperModelSize
		want      string
	}{
		{
			name:      "tiny model",
			modelsDir: "/models",
			modelSize: domain.WhisperModelSizeTiny,
			want:      "/models/ggml-tiny.bin",
		},
		{
			name:      "base model",
			modelsDir: "/models",
			modelSize: domain.WhisperModelSizeBase,
			want:      "/models/ggml-base.bin",
		},
		{
			name:      "small model",
			modelsDir: "/models",
			modelSize: domain.WhisperModelSizeSmall,
			want:      "/models/ggml-small.bin",
		},
		{
			name:      "medium model",
			modelsDir: "/models",
			modelSize: domain.WhisperModelSizeMedium,
			want:      "/models/ggml-medium.bin",
		},
		{
			name:      "large model",
			modelsDir: "/models",
			modelSize: domain.WhisperModelSizeLarge,
			want:      "/models/ggml-large.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetModelPath(tt.modelsDir, tt.modelSize)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetTempAudioPath(t *testing.T) {
	tests := []struct {
		name    string
		tempDir string
		videoID string
		want    string
	}{
		{
			name:    "standard path",
			tempDir: "/tmp/whisper",
			videoID: "video123",
			want:    "/tmp/whisper/video123.wav",
		},
		{
			name:    "with UUID",
			tempDir: "/tmp/whisper",
			videoID: "550e8400-e29b-41d4-a716-446655440000",
			want:    "/tmp/whisper/550e8400-e29b-41d4-a716-446655440000.wav",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetTempAudioPath(tt.tempDir, tt.videoID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTranscriptionResult_Segments(t *testing.T) {
	result := &TranscriptionResult{
		Text:             "Hello world. This is a test.",
		DetectedLanguage: "en",
		Confidence:       0.95,
		Duration:         5.5,
		Segments: []TranscriptionSegment{
			{
				Index:      0,
				Start:      0.0,
				End:        1.5,
				Text:       "Hello world.",
				Confidence: 0.96,
			},
			{
				Index:      1,
				Start:      1.5,
				End:        3.2,
				Text:       "This is a test.",
				Confidence: 0.94,
			},
		},
	}

	assert.Equal(t, 2, len(result.Segments))
	assert.Equal(t, "Hello world.", result.Segments[0].Text)
	assert.Equal(t, 0.0, result.Segments[0].Start)
	assert.Equal(t, 1.5, result.Segments[0].End)
	assert.Equal(t, "This is a test.", result.Segments[1].Text)
}

func TestOpenAIClient_GetProvider(t *testing.T) {
	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	err := os.WriteFile(ffmpegPath, []byte("fake ffmpeg"), 0755)
	require.NoError(t, err)

	cfg := &Config{
		Provider:     domain.WhisperProviderOpenAI,
		ModelSize:    domain.WhisperModelSizeBase,
		OpenAIAPIKey: "sk-test-key",
		FFmpegPath:   ffmpegPath,
	}

	client, err := NewClient(cfg)
	require.NoError(t, err)
	assert.Equal(t, domain.WhisperProviderOpenAI, client.GetProvider())
}

func TestOpenAIClient_Transcribe_FileValidation(t *testing.T) {
	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	err := os.WriteFile(ffmpegPath, []byte("fake ffmpeg"), 0755)
	require.NoError(t, err)

	cfg := &Config{
		Provider:     domain.WhisperProviderOpenAI,
		ModelSize:    domain.WhisperModelSizeBase,
		OpenAIAPIKey: "sk-test-key",
		FFmpegPath:   ffmpegPath,
		TempDir:      tempDir,
	}

	client, err := newOpenAIClient(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("non-existent file", func(t *testing.T) {
		_, err := client.Transcribe(ctx, "/nonexistent/audio.wav", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to stat audio file")
	})

	t.Run("file too large", func(t *testing.T) {
		// Create a file larger than 25MB
		largeFile := filepath.Join(tempDir, "large.wav")
		f, err := os.Create(largeFile)
		require.NoError(t, err)

		// Write more than 25MB of data
		data := make([]byte, 26*1024*1024)
		_, err = f.Write(data)
		require.NoError(t, err)
		f.Close()

		_, err = client.Transcribe(ctx, largeFile, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "audio file too large")
	})
}

func TestTranscriptionSegment_Validation(t *testing.T) {
	seg := TranscriptionSegment{
		Index:      0,
		Start:      0.0,
		End:        2.5,
		Text:       "Test segment",
		Confidence: 0.95,
	}

	assert.Equal(t, 0, seg.Index)
	assert.Equal(t, 0.0, seg.Start)
	assert.Equal(t, 2.5, seg.End)
	assert.Equal(t, "Test segment", seg.Text)
	assert.InDelta(t, 0.95, seg.Confidence, 0.01)

	// Verify time ordering
	assert.Less(t, seg.Start, seg.End, "Start time should be less than end time")
}

func TestConfig_Defaults(t *testing.T) {
	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	whisperPath := filepath.Join(tempDir, "whisper")
	modelsDir := filepath.Join(tempDir, "models")

	err := os.WriteFile(ffmpegPath, []byte("fake ffmpeg"), 0755)
	require.NoError(t, err)
	err = os.WriteFile(whisperPath, []byte("fake whisper"), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(modelsDir, 0755)
	require.NoError(t, err)

	cfg := &Config{
		Provider:       domain.WhisperProviderLocal,
		ModelSize:      domain.WhisperModelSizeBase,
		WhisperCppPath: whisperPath,
		ModelsDir:      modelsDir,
		FFmpegPath:     ffmpegPath,
		// TempDir not set - should use default
	}

	err = validateConfig(cfg)
	require.NoError(t, err)

	// Check that TempDir was set to a default value
	assert.NotEmpty(t, cfg.TempDir)
	assert.Contains(t, cfg.TempDir, "whisper")

	// Verify temp directory was created
	_, err = os.Stat(cfg.TempDir)
	assert.NoError(t, err, "Temp directory should be created")
}

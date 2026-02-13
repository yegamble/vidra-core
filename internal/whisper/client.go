package whisper

import (
	"athena/internal/domain"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// TranscriptionResult represents the result of a Whisper transcription
type TranscriptionResult struct {
	Text             string  // Full transcription text
	DetectedLanguage string  // ISO 639-1 language code (e.g., 'en', 'es', 'fr')
	Confidence       float64 // Confidence score (0-1)
	Duration         float64 // Audio duration in seconds
	Segments         []TranscriptionSegment
}

// TranscriptionSegment represents a time-aligned segment of transcription
type TranscriptionSegment struct {
	Index      int     // Segment index
	Start      float64 // Start time in seconds
	End        float64 // End time in seconds
	Text       string  // Segment text
	Confidence float64 // Segment confidence score
}

// Client defines the interface for Whisper transcription providers
type Client interface {
	// Transcribe transcribes an audio file to text with optional language hint
	Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*TranscriptionResult, error)

	// ExtractAudioFromVideo extracts audio track from video file
	ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error

	// FormatToVTT converts transcription result to WebVTT format
	FormatToVTT(result *TranscriptionResult) (string, error)

	// FormatToSRT converts transcription result to SRT format
	FormatToSRT(result *TranscriptionResult) (string, error)

	// GetProvider returns the provider type
	GetProvider() domain.WhisperProvider
}

// Config holds Whisper client configuration
type Config struct {
	Provider       domain.WhisperProvider
	ModelSize      domain.WhisperModelSize
	OpenAIAPIKey   string
	WhisperCppPath string // Path to whisper.cpp binary
	WhisperAPIURL  string // URL for HTTP Whisper service
	ModelsDir      string // Directory containing Whisper models
	FFmpegPath     string // Path to FFmpeg binary
	TempDir        string // Directory for temporary files
}

// NewClient creates a new Whisper client based on the provider
func NewClient(cfg *Config) (Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid whisper config: %w", err)
	}

	switch cfg.Provider {
	case domain.WhisperProviderLocal:
		// Check if HTTP API URL is provided
		if cfg.WhisperAPIURL != "" {
			return newHTTPClient(cfg)
		}
		return newLocalClient(cfg)
	case domain.WhisperProviderOpenAI:
		return newOpenAIClient(cfg)
	default:
		return nil, fmt.Errorf("unsupported whisper provider: %s", cfg.Provider)
	}
}

// validateConfig validates the Whisper configuration
func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if !cfg.Provider.IsValid() {
		return fmt.Errorf("invalid provider: %s", cfg.Provider)
	}

	if !cfg.ModelSize.IsValid() {
		return fmt.Errorf("invalid model size: %s", cfg.ModelSize)
	}

	switch cfg.Provider {
	case domain.WhisperProviderLocal:
		// If HTTP API URL is provided, use HTTP client
		if cfg.WhisperAPIURL != "" {
			// HTTP client validation
			if cfg.WhisperAPIURL == "" {
				return fmt.Errorf("whisper API URL is required for HTTP-based local provider")
			}
		} else {
			// whisper.cpp validation
			if cfg.WhisperCppPath == "" {
				return fmt.Errorf("whisper.cpp path is required for local provider")
			}
			if cfg.ModelsDir == "" {
				return fmt.Errorf("models directory is required for local provider")
			}
			if _, err := os.Stat(cfg.WhisperCppPath); os.IsNotExist(err) {
				return fmt.Errorf("whisper.cpp binary not found at: %s", cfg.WhisperCppPath)
			}
			if _, err := os.Stat(cfg.ModelsDir); os.IsNotExist(err) {
				return fmt.Errorf("models directory not found at: %s", cfg.ModelsDir)
			}
		}

	case domain.WhisperProviderOpenAI:
		if cfg.OpenAIAPIKey == "" {
			return fmt.Errorf("OpenAI API key is required for openai-api provider")
		}
	}

	if cfg.FFmpegPath == "" {
		return fmt.Errorf("ffmpeg path is required")
	}

	if _, err := os.Stat(cfg.FFmpegPath); os.IsNotExist(err) {
		return fmt.Errorf("ffmpeg binary not found at: %s", cfg.FFmpegPath)
	}

	if cfg.TempDir == "" {
		cfg.TempDir = filepath.Join(os.TempDir(), "whisper")
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(cfg.TempDir, 0750); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	return nil
}

// GetModelPath returns the path to the Whisper model file for a given size
func GetModelPath(modelsDir string, modelSize domain.WhisperModelSize) string {
	return filepath.Join(modelsDir, fmt.Sprintf("ggml-%s.bin", modelSize))
}

// GetTempAudioPath generates a temporary path for extracted audio
func GetTempAudioPath(tempDir string, videoID string) string {
	return filepath.Join(tempDir, fmt.Sprintf("%s.wav", videoID))
}

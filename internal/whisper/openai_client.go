package whisper

import (
	"vidra-core/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	openAIWhisperURL = "https://api.openai.com/v1/audio/transcriptions"
	maxFileSize      = 25 * 1024 * 1024 // 25MB limit for OpenAI API
)

// openAIClient implements the Client interface using OpenAI Whisper API
type openAIClient struct {
	config     *Config
	httpClient *http.Client
}

func newOpenAIClient(cfg *Config) (Client, error) {
	return &openAIClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // Generous timeout for large files
		},
	}, nil
}

func (c *openAIClient) GetProvider() domain.WhisperProvider {
	return domain.WhisperProviderOpenAI
}

// Transcribe transcribes an audio file using OpenAI Whisper API
func (c *openAIClient) Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*TranscriptionResult, error) {
	// Check file size
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat audio file: %w", err)
	}

	if fileInfo.Size() > maxFileSize {
		return nil, fmt.Errorf("audio file too large: %d bytes (max %d bytes)", fileInfo.Size(), maxFileSize)
	}

	// Open audio file
	audioFile, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer func() { _ = audioFile.Close() }()

	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add file field
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, audioFile); err != nil {
		return nil, fmt.Errorf("failed to copy audio file: %w", err)
	}

	// Add model field (whisper-1 is the only model available)
	if err := writer.WriteField("model", "whisper-1"); err != nil {
		return nil, fmt.Errorf("failed to write model field: %w", err)
	}

	// Add response format (verbose_json gives us timestamps)
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("failed to write response_format field: %w", err)
	}

	// Add language hint if provided
	if targetLanguage != nil && *targetLanguage != "" {
		if err := writer.WriteField("language", *targetLanguage); err != nil {
			return nil, fmt.Errorf("failed to write language field: %w", err)
		}
	}

	// Add timestamp granularities for word-level timestamps
	if err := writer.WriteField("timestamp_granularities[]", "segment"); err != nil {
		return nil, fmt.Errorf("failed to write timestamp_granularities field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", openAIWhisperURL, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.OpenAIAPIKey))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai api error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp OpenAIWhisperResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to our format
	result := &TranscriptionResult{
		Text:             apiResp.Text,
		DetectedLanguage: apiResp.Language,
		Duration:         apiResp.Duration,
		Segments:         make([]TranscriptionSegment, 0, len(apiResp.Segments)),
	}

	// Calculate average confidence
	totalConfidence := 0.0
	for i, segment := range apiResp.Segments {
		result.Segments = append(result.Segments, TranscriptionSegment{
			Index:      i,
			Start:      segment.Start,
			End:        segment.End,
			Text:       strings.TrimSpace(segment.Text),
			Confidence: segment.AvgLogprob, // OpenAI uses log probability
		})
		totalConfidence += segment.AvgLogprob
	}

	if len(apiResp.Segments) > 0 {
		result.Confidence = totalConfidence / float64(len(apiResp.Segments))
	}

	return result, nil
}

// ExtractAudioFromVideo extracts audio track from video using FFmpeg
func (c *openAIClient) ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// FFmpeg command to extract audio as MP3 for OpenAI API
	args := []string{
		"-i", videoPath,
		"-vn",            // No video
		"-acodec", "mp3", // MP3 codec (supported by OpenAI)
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-b:a", "64k", // 64kbps bitrate (reduces file size)
		"-y", // Overwrite output
		outputPath,
	}

	cmd := exec.CommandContext(ctx, c.config.FFmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg audio extraction failed: %w, output: %s", err, string(output))
	}

	// Check if output file is within size limit
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("failed to stat output file: %w", err)
	}

	if fileInfo.Size() > maxFileSize {
		return fmt.Errorf("extracted audio too large: %d bytes (max %d bytes)", fileInfo.Size(), maxFileSize)
	}

	return nil
}

// FormatToVTT converts transcription result to WebVTT format
func (c *openAIClient) FormatToVTT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	// WebVTT header
	sb.WriteString("WEBVTT\n\n")

	// Add cues
	for i, segment := range result.Segments {
		// Format timestamps (HH:MM:SS.mmm)
		start := formatVTTTimestamp(segment.Start)
		end := formatVTTTimestamp(segment.End)

		// Write cue
		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", start, end)
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// FormatToSRT converts transcription result to SRT format
func (c *openAIClient) FormatToSRT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	for i, segment := range result.Segments {
		// Format timestamps (HH:MM:SS,mmm)
		start := formatSRTTimestamp(segment.Start)
		end := formatSRTTimestamp(segment.End)

		// Write subtitle entry
		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", start, end)
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// OpenAIWhisperResponse represents the verbose JSON response from OpenAI Whisper API
type OpenAIWhisperResponse struct {
	Task     string  `json:"task"`
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Text     string  `json:"text"`
	Segments []struct {
		ID               int     `json:"id"`
		Seek             int     `json:"seek"`
		Start            float64 `json:"start"`
		End              float64 `json:"end"`
		Text             string  `json:"text"`
		Tokens           []int   `json:"tokens"`
		Temperature      float64 `json:"temperature"`
		AvgLogprob       float64 `json:"avg_logprob"`
		CompressionRatio float64 `json:"compression_ratio"`
		NoSpeechProb     float64 `json:"no_speech_prob"`
	} `json:"segments"`
}

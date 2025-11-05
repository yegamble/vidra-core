package whisper

import (
	"athena/internal/domain"
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

// httpClient implements the Client interface using an HTTP Whisper service
type httpClient struct {
	config     *Config
	httpClient *http.Client
	baseURL    string
}

func newHTTPClient(cfg *Config) (Client, error) {
	if cfg.WhisperAPIURL == "" {
		return nil, fmt.Errorf("whisper API URL is required for HTTP provider")
	}

	return &httpClient{
		config:  cfg,
		baseURL: cfg.WhisperAPIURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // Generous timeout for large files
		},
	}, nil
}

func (c *httpClient) GetProvider() domain.WhisperProvider {
	return domain.WhisperProviderLocal
}

// Transcribe transcribes an audio file using HTTP Whisper service
func (c *httpClient) Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*TranscriptionResult, error) {
	// Open audio file
	audioFile, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer audioFile.Close()

	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add file field
	part, err := writer.CreateFormFile("audio_file", filepath.Base(audioPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, audioFile); err != nil {
		return nil, fmt.Errorf("failed to copy audio file: %w", err)
	}

	// Add task field
	if err := writer.WriteField("task", "transcribe"); err != nil {
		return nil, fmt.Errorf("failed to write task field: %w", err)
	}

	// Add language hint if provided
	if targetLanguage != nil && *targetLanguage != "" {
		if err := writer.WriteField("language", *targetLanguage); err != nil {
			return nil, fmt.Errorf("failed to write language field: %w", err)
		}
	}

	// Add output format
	if err := writer.WriteField("output", "json"); err != nil {
		return nil, fmt.Errorf("failed to write output field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/asr", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("whisper API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp WhisperHTTPResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to our format
	result := &TranscriptionResult{
		Text:             apiResp.Text,
		DetectedLanguage: apiResp.Language,
		Segments:         make([]TranscriptionSegment, 0, len(apiResp.Segments)),
	}

	// Calculate average confidence and build segments
	totalConfidence := 0.0
	for i, segment := range apiResp.Segments {
		confidence := 1.0 // Default confidence if not provided
		if segment.AvgLogprob != 0 {
			// Convert log probability to a 0-1 confidence score
			confidence = 1.0 + segment.AvgLogprob/10.0
			if confidence < 0 {
				confidence = 0
			}
			if confidence > 1 {
				confidence = 1
			}
		}

		result.Segments = append(result.Segments, TranscriptionSegment{
			Index:      i,
			Start:      segment.Start,
			End:        segment.End,
			Text:       strings.TrimSpace(segment.Text),
			Confidence: confidence,
		})
		totalConfidence += confidence
	}

	if len(apiResp.Segments) > 0 {
		result.Confidence = totalConfidence / float64(len(apiResp.Segments))
		result.Duration = result.Segments[len(result.Segments)-1].End
	}

	return result, nil
}

// ExtractAudioFromVideo extracts audio track from video using FFmpeg
func (c *httpClient) ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// FFmpeg command to extract audio as WAV for Whisper
	args := []string{
		"-i", videoPath,
		"-vn",                  // No video
		"-acodec", "pcm_s16le", // PCM 16-bit
		"-ar", "16000",         // 16kHz sample rate
		"-ac", "1",             // Mono
		"-y",                   // Overwrite output
		outputPath,
	}

	cmd := exec.CommandContext(ctx, c.config.FFmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg audio extraction failed: %w, output: %s", err, string(output))
	}

	return nil
}

// FormatToVTT converts transcription result to WebVTT format
func (c *httpClient) FormatToVTT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	// WebVTT header
	sb.WriteString("WEBVTT\n\n")

	// Add cues
	for i, segment := range result.Segments {
		// Format timestamps (HH:MM:SS.mmm)
		start := formatVTTTimestamp(segment.Start)
		end := formatVTTTimestamp(segment.End)

		// Write cue
		sb.WriteString(fmt.Sprintf("%d\n", i+1))
		sb.WriteString(fmt.Sprintf("%s --> %s\n", start, end))
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// FormatToSRT converts transcription result to SRT format
func (c *httpClient) FormatToSRT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	for i, segment := range result.Segments {
		// Format timestamps (HH:MM:SS,mmm)
		start := formatSRTTimestamp(segment.Start)
		end := formatSRTTimestamp(segment.End)

		// Write subtitle entry
		sb.WriteString(fmt.Sprintf("%d\n", i+1))
		sb.WriteString(fmt.Sprintf("%s --> %s\n", start, end))
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// WhisperHTTPResponse represents the JSON response from HTTP Whisper service
type WhisperHTTPResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language"`
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

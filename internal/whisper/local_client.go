package whisper

import (
	"vidra-core/internal/domain"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type localClient struct {
	config *Config
}

func newLocalClient(cfg *Config) (Client, error) {
	return &localClient{config: cfg}, nil
}

func (c *localClient) GetProvider() domain.WhisperProvider {
	return domain.WhisperProviderLocal
}

func (c *localClient) Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*TranscriptionResult, error) {
	modelPath := GetModelPath(c.config.ModelsDir, c.config.ModelSize)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("whisper model not found: %s (download it first)", modelPath)
	}

	outputFile := filepath.Join(c.config.TempDir, fmt.Sprintf("whisper_%d.json", time.Now().Unix()))
	defer func() { _ = os.Remove(outputFile) }()

	args := []string{
		"-m", modelPath,
		"-f", audioPath,
		"--output-json",
		"--output-file", strings.TrimSuffix(outputFile, ".json"),
		"--print-progress",
	}

	if targetLanguage != nil && *targetLanguage != "" {
		args = append(args, "-l", *targetLanguage)
	}

	cmd := exec.CommandContext(ctx, c.config.WhisperCppPath, args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper.cpp execution failed: %w", err)
	}

	jsonData, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper output: %w", err)
	}

	var whisperOutput WhisperCppOutput
	if err := json.Unmarshal(jsonData, &whisperOutput); err != nil {
		return nil, fmt.Errorf("failed to parse whisper output: %w", err)
	}

	result := &TranscriptionResult{
		Text:             whisperOutput.GetFullText(),
		DetectedLanguage: whisperOutput.GetDetectedLanguage(),
		Confidence:       whisperOutput.GetAverageConfidence(),
		Segments:         make([]TranscriptionSegment, 0, len(whisperOutput.Transcription)),
	}

	for i, segment := range whisperOutput.Transcription {
		result.Segments = append(result.Segments, TranscriptionSegment{
			Index:      i,
			Start:      float64(segment.Timestamps.From) / 100.0,
			End:        float64(segment.Timestamps.To) / 100.0,
			Text:       strings.TrimSpace(segment.Text),
			Confidence: segment.GetConfidence(),
		})
	}

	if len(result.Segments) > 0 {
		result.Duration = result.Segments[len(result.Segments)-1].End
	}

	return result, nil
}

func (c *localClient) ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	args := []string{
		"-i", videoPath,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, c.config.FFmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg audio extraction failed: %w, output: %s", err, string(output))
	}

	return nil
}

func (c *localClient) FormatToVTT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	sb.WriteString("WEBVTT\n\n")

	for i, segment := range result.Segments {
		start := formatVTTTimestamp(segment.Start)
		end := formatVTTTimestamp(segment.End)

		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", start, end)
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

func (c *localClient) FormatToSRT(result *TranscriptionResult) (string, error) {
	var sb strings.Builder

	for i, segment := range result.Segments {
		start := formatSRTTimestamp(segment.Start)
		end := formatSRTTimestamp(segment.End)

		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", start, end)
		sb.WriteString(segment.Text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

func formatTimestamp(seconds float64, separator string) string {
	hours := int(seconds) / 3600
	minutes := (int(seconds) % 3600) / 60
	secs := int(seconds) % 60
	millis := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", hours, minutes, secs, separator, millis)
}

func formatVTTTimestamp(seconds float64) string {
	return formatTimestamp(seconds, ".")
}

func formatSRTTimestamp(seconds float64) string {
	return formatTimestamp(seconds, ",")
}

type WhisperCppOutput struct {
	SystemInfo struct {
		Model       string `json:"model"`
		Language    string `json:"language"`
		TranslateEn bool   `json:"translate"`
	} `json:"systeminfo"`
	Transcription []WhisperSegment `json:"transcription"`
}

type WhisperSegment struct {
	Timestamps struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"timestamps"`
	Offsets struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"offsets"`
	Text   string                 `json:"text"`
	Tokens []WhisperToken         `json:"tokens,omitempty"`
	Meta   map[string]interface{} `json:"meta,omitempty"`
}

type WhisperToken struct {
	Text        string  `json:"text"`
	ID          int     `json:"id"`
	Probability float64 `json:"p"`
	Timestamp   struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"t"`
}

func (o *WhisperCppOutput) GetFullText() string {
	var sb strings.Builder
	for _, segment := range o.Transcription {
		sb.WriteString(strings.TrimSpace(segment.Text))
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String())
}

func (o *WhisperCppOutput) GetDetectedLanguage() string {
	if o.SystemInfo.Language != "" {
		return o.SystemInfo.Language
	}
	return "en"
}

func (o *WhisperCppOutput) GetAverageConfidence() float64 {
	if len(o.Transcription) == 0 {
		return 0.0
	}

	totalConfidence := 0.0
	for _, segment := range o.Transcription {
		totalConfidence += segment.GetConfidence()
	}

	return totalConfidence / float64(len(o.Transcription))
}

func (s *WhisperSegment) GetConfidence() float64 {
	if len(s.Tokens) == 0 {
		return 1.0
	}

	totalProb := 0.0
	for _, token := range s.Tokens {
		totalProb += token.Probability
	}

	return totalProb / float64(len(s.Tokens))
}

func ParseWhisperCppLog(reader *bufio.Reader) (int, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}

		if strings.Contains(line, "[") && strings.Contains(line, "%]") {
			start := strings.Index(line, "[") + 1
			end := strings.Index(line, "%]")
			if start > 0 && end > start {
				progressStr := strings.TrimSpace(line[start:end])
				progress, err := strconv.Atoi(progressStr)
				if err == nil {
					return progress, nil
				}
			}
		}
	}
}

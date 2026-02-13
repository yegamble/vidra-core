package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCaptionFormat_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		format CaptionFormat
		want   bool
	}{
		{"vtt is valid", CaptionFormatVTT, true},
		{"srt is valid", CaptionFormatSRT, true},
		{"empty string is invalid", CaptionFormat(""), false},
		{"ass is invalid", CaptionFormat("ass"), false},
		{"sub is invalid", CaptionFormat("sub"), false},
		{"VTT uppercase is invalid", CaptionFormat("VTT"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.format.IsValid())
		})
	}
}

func TestCaptionFormat_String(t *testing.T) {
	tests := []struct {
		name   string
		format CaptionFormat
		want   string
	}{
		{"vtt string", CaptionFormatVTT, "vtt"},
		{"srt string", CaptionFormatSRT, "srt"},
		{"arbitrary value", CaptionFormat("xyz"), "xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.format.String())
		})
	}
}

func TestCaptionFormat_GetContentType(t *testing.T) {
	tests := []struct {
		name   string
		format CaptionFormat
		want   string
	}{
		{"vtt content type", CaptionFormatVTT, "text/vtt"},
		{"srt content type", CaptionFormatSRT, "application/x-subrip"},
		{"unknown format defaults to text/plain", CaptionFormat("unknown"), "text/plain"},
		{"empty format defaults to text/plain", CaptionFormat(""), "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.format.GetContentType())
		})
	}
}

func TestCaptionGenerationStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name   string
		status CaptionGenerationStatus
		want   bool
	}{
		{"completed is terminal", CaptionGenStatusCompleted, true},
		{"failed is terminal", CaptionGenStatusFailed, true},
		{"pending is not terminal", CaptionGenStatusPending, false},
		{"processing is not terminal", CaptionGenStatusProcessing, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.IsTerminal())
		})
	}
}

func TestCaptionGenerationStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		status CaptionGenerationStatus
		want   string
	}{
		{"pending", CaptionGenStatusPending, "pending"},
		{"processing", CaptionGenStatusProcessing, "processing"},
		{"completed", CaptionGenStatusCompleted, "completed"},
		{"failed", CaptionGenStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}

func TestWhisperModelSize_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		model WhisperModelSize
		want  bool
	}{
		{"tiny is valid", WhisperModelTiny, true},
		{"base is valid", WhisperModelBase, true},
		{"small is valid", WhisperModelSmall, true},
		{"medium is valid", WhisperModelMedium, true},
		{"large is valid", WhisperModelLarge, true},
		{"empty is invalid", WhisperModelSize(""), false},
		{"xlarge is invalid", WhisperModelSize("xlarge"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.model.IsValid())
		})
	}
}

func TestWhisperModelSize_String(t *testing.T) {
	tests := []struct {
		name  string
		model WhisperModelSize
		want  string
	}{
		{"tiny", WhisperModelTiny, "tiny"},
		{"base", WhisperModelBase, "base"},
		{"small", WhisperModelSmall, "small"},
		{"medium", WhisperModelMedium, "medium"},
		{"large", WhisperModelLarge, "large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.model.String())
		})
	}
}

func TestWhisperProvider_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		provider WhisperProvider
		want     bool
	}{
		{"local is valid", WhisperProviderLocal, true},
		{"openai-api is valid", WhisperProviderOpenAI, true},
		{"empty is invalid", WhisperProvider(""), false},
		{"azure is invalid", WhisperProvider("azure"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.provider.IsValid())
		})
	}
}

func TestWhisperProvider_String(t *testing.T) {
	tests := []struct {
		name     string
		provider WhisperProvider
		want     string
	}{
		{"local", WhisperProviderLocal, "local"},
		{"openai-api", WhisperProviderOpenAI, "openai-api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.provider.String())
		})
	}
}

package whisper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		seconds   float64
		separator string
		want      string
	}{
		{
			name:      "zero seconds VTT",
			seconds:   0,
			separator: ".",
			want:      "00:00:00.000",
		},
		{
			name:      "zero seconds SRT",
			seconds:   0,
			separator: ",",
			want:      "00:00:00,000",
		},
		{
			name:      "1 hour 2 min 3.456 sec VTT",
			seconds:   3723.456,
			separator: ".",
			want:      "01:02:03.456",
		},
		{
			name:      "1 hour 2 min 3.456 sec SRT",
			seconds:   3723.456,
			separator: ",",
			want:      "01:02:03,456",
		},
		{
			name:      "millis only VTT",
			seconds:   0.123,
			separator: ".",
			want:      "00:00:00.123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimestamp(tt.seconds, tt.separator)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatVTTTimestamp(t *testing.T) {
	assert.Equal(t, "00:01:30.500", formatVTTTimestamp(90.5))
}

func TestFormatSRTTimestamp(t *testing.T) {
	assert.Equal(t, "00:01:30,500", formatSRTTimestamp(90.5))
}

package encoding

import (
	"testing"
	"time"
)

func TestProgressParser_ParseLine(t *testing.T) {
	tests := []struct {
		name          string
		totalDuration time.Duration
		line          string
		wantProgress  int
		wantFound     bool
	}{
		{
			name:          "valid progress at start",
			totalDuration: 2 * time.Minute,
			line:          "frame=   30 fps=0.0 q=-1.0 size=     256kB time=00:00:00.00 bitrate=N/A",
			wantProgress:  0,
			wantFound:     true,
		},
		{
			name:          "valid progress at 25%",
			totalDuration: 2 * time.Minute,
			line:          "frame= 720 fps=30.0 q=23.0 size=   2048kB time=00:00:30.00 bitrate= 560.0kbits/s",
			wantProgress:  25,
			wantFound:     true,
		},
		{
			name:          "valid progress at 50%",
			totalDuration: 2 * time.Minute,
			line:          "frame=1440 fps=30.0 q=23.0 size=   4096kB time=00:01:00.00 bitrate=1234.5kbits/s",
			wantProgress:  50,
			wantFound:     true,
		},
		{
			name:          "valid progress at 75%",
			totalDuration: 2 * time.Minute,
			line:          "frame=2160 fps=30.0 q=23.0 size=   6144kB time=00:01:30.00 bitrate=1234.5kbits/s",
			wantProgress:  75,
			wantFound:     true,
		},
		{
			name:          "valid progress at 100%",
			totalDuration: 1 * time.Minute,
			line:          "frame=1800 fps=30.0 q=23.0 size=   8192kB time=00:01:00.00 bitrate=1234.5kbits/s",
			wantProgress:  100,
			wantFound:     true,
		},
		{
			name:          "no time in line",
			totalDuration: 2 * time.Minute,
			line:          "frame=1234 bitrate=1234.5kbps",
			wantProgress:  0,
			wantFound:     false,
		},
		{
			name:          "empty line",
			totalDuration: 2 * time.Minute,
			line:          "",
			wantProgress:  0,
			wantFound:     false,
		},
		{
			name:          "progress exceeds 100%",
			totalDuration: 1 * time.Minute,
			line:          "frame=9999 time=00:02:00.00 bitrate=1234.5kbps",
			wantProgress:  100,
			wantFound:     true,
		},
		{
			name:          "zero duration",
			totalDuration: 0,
			line:          "frame=1440 time=00:01:00.00 bitrate=1234.5kbps",
			wantProgress:  0,
			wantFound:     false,
		},
		{
			name:          "malformed time format",
			totalDuration: 2 * time.Minute,
			line:          "frame=1440 time=1:00.00 bitrate=1234.5kbps",
			wantProgress:  0,
			wantFound:     false,
		},
		{
			name:          "time with microseconds",
			totalDuration: 10 * time.Minute,
			line:          "frame=7200 fps=30.0 q=23.0 time=00:04:00.24 bitrate=1234.5kbits/s",
			wantProgress:  40,
			wantFound:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewProgressParser(tt.totalDuration)
			progress, found := parser.ParseLine(tt.line)

			if found != tt.wantFound {
				t.Errorf("ParseLine() found = %v, want %v", found, tt.wantFound)
			}
			if progress != tt.wantProgress {
				t.Errorf("ParseLine() progress = %d, want %d", progress, tt.wantProgress)
			}
		})
	}
}

func TestProgressParser_ParseProgressStats(t *testing.T) {
	tests := []struct {
		name          string
		totalDuration time.Duration
		stats         string
		wantProgress  int
		wantFPS       float64
		wantBitrate   string
		wantFound     bool
	}{
		{
			name:          "valid progress stats with microseconds",
			totalDuration: 2 * time.Minute,
			stats: `frame=720
fps=30.5
bitrate=1234.5kbits/s
out_time_ms=30000000
out_time=00:00:30.000000
dup_frames=0
drop_frames=0`,
			wantProgress: 25,
			wantFPS:      30.5,
			wantBitrate:  "1234.5kbits/s",
			wantFound:    true,
		},
		{
			name:          "valid progress stats at 50%",
			totalDuration: 2 * time.Minute,
			stats: `out_time_ms=60000000
fps=29.97
bitrate=2048.0kbits/s`,
			wantProgress: 50,
			wantFPS:      29.97,
			wantBitrate:  "2048.0kbits/s",
			wantFound:    true,
		},
		{
			name:          "fallback to out_time when out_time_ms missing",
			totalDuration: 2 * time.Minute,
			stats: `frame=1440
fps=30.0
bitrate=1500.0kbits/s
out_time=00:01:00.000000`,
			wantProgress: 50,
			wantFPS:      30.0,
			wantBitrate:  "1500.0kbits/s",
			wantFound:    true,
		},
		{
			name:          "empty stats",
			totalDuration: 2 * time.Minute,
			stats:         "",
			wantProgress:  0,
			wantFPS:       0,
			wantBitrate:   "",
			wantFound:     false,
		},
		{
			name:          "malformed stats",
			totalDuration: 2 * time.Minute,
			stats: `this is not valid
key value format
no equals signs here`,
			wantProgress: 0,
			wantFPS:      0,
			wantBitrate:  "",
			wantFound:    false,
		},
		{
			name:          "progress capped at 100%",
			totalDuration: 1 * time.Minute,
			stats: `out_time_ms=120000000
fps=30.0
bitrate=1234.5kbits/s`,
			wantProgress: 100,
			wantFPS:      30.0,
			wantBitrate:  "1234.5kbits/s",
			wantFound:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewProgressParser(tt.totalDuration)
			progress, fps, bitrate, found := parser.ParseProgressStats(tt.stats)

			if found != tt.wantFound {
				t.Errorf("ParseProgressStats() found = %v, want %v", found, tt.wantFound)
			}
			if progress != tt.wantProgress {
				t.Errorf("ParseProgressStats() progress = %d, want %d", progress, tt.wantProgress)
			}
			if fps != tt.wantFPS {
				t.Errorf("ParseProgressStats() fps = %f, want %f", fps, tt.wantFPS)
			}
			if bitrate != tt.wantBitrate {
				t.Errorf("ParseProgressStats() bitrate = %s, want %s", bitrate, tt.wantBitrate)
			}
		})
	}
}

func TestProgressParser_EdgeCases(t *testing.T) {
	t.Run("very long video", func(t *testing.T) {
		// Test with a 3-hour video
		parser := NewProgressParser(3 * time.Hour)
		progress, found := parser.ParseLine("time=01:30:00.00")
		if !found {
			t.Error("Expected to find progress")
		}
		if progress != 50 {
			t.Errorf("Expected 50%% progress for 1.5 hours of 3 hour video, got %d", progress)
		}
	})

	t.Run("very short video", func(t *testing.T) {
		// Test with a 10-second video
		parser := NewProgressParser(10 * time.Second)
		progress, found := parser.ParseLine("time=00:00:05.00")
		if !found {
			t.Error("Expected to find progress")
		}
		if progress != 50 {
			t.Errorf("Expected 50%% progress for 5 seconds of 10 second video, got %d", progress)
		}
	})

	t.Run("negative duration protection", func(t *testing.T) {
		// Ensure we don't panic with negative duration
		parser := NewProgressParser(-1 * time.Minute)
		progress, found := parser.ParseLine("time=00:01:00.00")
		if found {
			t.Error("Should not find progress with negative duration")
		}
		if progress != 0 {
			t.Errorf("Expected 0 progress, got %d", progress)
		}
	})
}

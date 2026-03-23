package encoding

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ffmpegTimeRegex matches the time format in FFmpeg's progress output
// Example: time=00:01:23.45
var ffmpegTimeRegex = regexp.MustCompile(`time=(\d{2}):(\d{2}):(\d{2})\.(\d{2})`)

// ProgressParser parses FFmpeg output to extract encoding progress
type ProgressParser struct {
	totalDuration time.Duration
}

// NewProgressParser creates a new progress parser with the total video duration
func NewProgressParser(totalDuration time.Duration) *ProgressParser {
	return &ProgressParser{totalDuration: totalDuration}
}

// ParseLine parses a line of FFmpeg output and returns the progress percentage if found
func (p *ProgressParser) ParseLine(line string) (progress int, found bool) {
	// Look for time= pattern in the line
	matches := ffmpegTimeRegex.FindStringSubmatch(line)
	if len(matches) != 5 {
		return 0, false
	}

	// Parse hours, minutes, seconds from the matched groups
	hours, _ := strconv.Atoi(matches[1])
	minutes, _ := strconv.Atoi(matches[2])
	seconds, _ := strconv.Atoi(matches[3])
	// Note: matches[4] is centiseconds, we'll ignore for simplicity

	// Calculate current time position
	currentTime := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second

	// Calculate progress percentage
	if p.totalDuration > 0 {
		progress = int(float64(currentTime) / float64(p.totalDuration) * 100)
		// Cap at 100% to handle slight overruns
		if progress > 100 {
			progress = 100
		}
		return progress, true
	}

	return 0, false
}

// ParseProgressStats parses FFmpeg's -progress output format for more detailed stats
// This format provides structured key=value pairs
func (p *ProgressParser) ParseProgressStats(stats string) (progress int, fps float64, bitrate string, found bool) {
	lines := strings.Split(stats, "\n")
	var timeStr string

	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "out_time_ms":
			// Time in microseconds
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				currentTime := time.Duration(us) * time.Microsecond
				if p.totalDuration > 0 {
					progress = int(float64(currentTime) / float64(p.totalDuration) * 100)
					if progress > 100 {
						progress = 100
					}
					found = true
				}
			}
		case "fps":
			fps, _ = strconv.ParseFloat(value, 64)
		case "bitrate":
			bitrate = value
		case "out_time":
			timeStr = value
		}
	}

	// Fallback to parsing out_time if out_time_ms not available
	if !found && timeStr != "" {
		if matches := ffmpegTimeRegex.FindStringSubmatch("time=" + timeStr); len(matches) == 5 {
			hours, _ := strconv.Atoi(matches[1])
			minutes, _ := strconv.Atoi(matches[2])
			seconds, _ := strconv.Atoi(matches[3])

			currentTime := time.Duration(hours)*time.Hour +
				time.Duration(minutes)*time.Minute +
				time.Duration(seconds)*time.Second

			if p.totalDuration > 0 {
				progress = int(float64(currentTime) / float64(p.totalDuration) * 100)
				if progress > 100 {
					progress = 100
				}
				found = true
			}
		}
	}

	return progress, fps, bitrate, found
}

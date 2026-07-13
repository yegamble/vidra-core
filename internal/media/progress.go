package media

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
)

const (
	TranscodeFormatHLS      = "hls"
	TranscodeFormatWebVideo = "web_video"

	ProgressQueued    = "queued"
	ProgressRunning   = "running"
	ProgressSucceeded = "succeeded"
	ProgressFailed    = "failed"
)

// TranscodeProgress is one safe rendition-level progress update. It contains
// no storage key, command line, or process output and is therefore suitable for
// the operator-facing job projection.
type TranscodeProgress struct {
	Format  string
	Height  int
	Width   int
	State   string
	Stage   string
	Percent int
}

type ProgressFunc func(TranscodeProgress)

func reportProgress(fn ProgressFunc, p TranscodeProgress) {
	if fn == nil {
		return
	}
	if p.Percent < 0 {
		p.Percent = 0
	}
	if p.Percent > 100 {
		p.Percent = 100
	}
	fn(p)
}

// runFFmpegWithProgress consumes ffmpeg's machine-readable -progress stream
// and reports monotonic whole percentages. out_time_us is media time, so the
// percentage remains correct regardless of encode speed or concurrency.
func runFFmpegWithProgress(ctx context.Context, bin string, args []string, durationSeconds int, onPercent func(int)) (string, error) {
	args = append([]string{"-progress", "pipe:1", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return stderr.String(), err
	}

	last := -1
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		key, raw, ok := strings.Cut(scanner.Text(), "=")
		if !ok || (key != "out_time_us" && key != "out_time_ms") || durationSeconds <= 0 {
			continue
		}
		micros, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if parseErr != nil {
			continue
		}
		percent := int(micros * 100 / (int64(durationSeconds) * 1_000_000))
		if percent > 99 {
			percent = 99
		}
		if percent > last {
			last = percent
			onPercent(percent)
		}
	}
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	return stderr.String(), waitErr
}

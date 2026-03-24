package encoding

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

const testFFmpegPath = "/opt/homebrew/bin/ffmpeg"

var (
	testFFmpegEncodersOnce sync.Once
	testFFmpegEncoders     string
	testFFmpegEncodersErr  error
)

func requireTestFFmpeg(t *testing.T) string {
	t.Helper()

	if _, err := os.Stat(testFFmpegPath); os.IsNotExist(err) {
		t.Skip("FFmpeg not available, skipping encoding tests")
	}

	return testFFmpegPath
}

func requireTestFFmpegEncoder(t *testing.T, encoder string) string {
	t.Helper()

	ffmpegPath := requireTestFFmpeg(t)

	testFFmpegEncodersOnce.Do(func() {
		output, err := exec.Command(ffmpegPath, "-hide_banner", "-encoders").CombinedOutput()
		if err != nil {
			testFFmpegEncodersErr = err
			return
		}
		testFFmpegEncoders = string(output)
	})

	if testFFmpegEncodersErr != nil {
		t.Skipf("Could not inspect FFmpeg encoders: %v", testFFmpegEncodersErr)
	}

	if !strings.Contains(testFFmpegEncoders, encoder) {
		t.Skipf("FFmpeg at %s does not support %s, skipping integration test", ffmpegPath, encoder)
	}

	return ffmpegPath
}

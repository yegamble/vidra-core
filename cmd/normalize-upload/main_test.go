package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vidra/vidra-core/internal/processedupload"
)

func TestProfileBudgetsTheWholeDownloadAgainstTheScanCeiling(t *testing.T) {
	for _, seconds := range []float64{1, 900, 3600, 10800, 43200} {
		p, err := chooseProfile(seconds, true)
		if err != nil {
			t.Fatal(err)
		}
		if float64(p.videoKbps+p.audioKbps)*seconds*1000/8 >= float64(processedupload.MaxScannedBytes) {
			t.Fatalf("duration %v, profile %+v exceeds scanner budget", seconds, p)
		}
	}
}

func TestInputOverTwentyGiBIsRejectedBeforeStartingFFmpeg(t *testing.T) {
	dir := t.TempDir()
	source, err := os.Create(filepath.Join(dir, "source"))
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Truncate(processedupload.MaxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	output := filepath.Join(dir, "processed.mp4")
	if err = normalize(context.Background(), source.Name(), output); err == nil || err.Error() != "invalid source file size or type" {
		t.Fatalf("normalize: %v", err)
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("unexpected output: %v", err)
	}
}
func TestSelectedAudioKeepsItsOffsetAfterRebasing(t *testing.T) {
	source := tracks{video: track{true, 10, 15}, audio: track{true, 12, 17}}
	if !source.matches(tracks{video: track{true, 0, 5}, audio: track{true, 2, 7}}) ||
		source.matches(tracks{video: track{true, 2, 7}, audio: track{true, 0, 5}}) {
		t.Fatal("output must preserve the delay between selected tracks")
	}
}

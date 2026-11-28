package media

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/processedupload"
)

func TestQueuedReconstructedSourceRequiresOutputScanning(t *testing.T) {
	id := uuid.New()
	protected := processedupload.SourceKey(id)
	tc := &HLSTranscoder{}
	md := Metadata{Width: 320, Height: 240}
	_, err := tc.TranscodeHLS(t.Context(), id, protected, 1, md, nil)
	if !errors.Is(err, processedupload.ErrOutputScannerRequired) {
		t.Fatalf("HLS has no scanner: %v", err)
	}
	_, err = tc.TranscodeWebVideos(t.Context(), id, protected, 2, md, nil)
	if !errors.Is(err, processedupload.ErrOutputScannerRequired) {
		t.Fatalf("progressive has no scanner: %v", err)
	}
	tc.SetProtectedOutputScanner(playbackScannerFunc(func(context.Context, io.Reader) (bool, error) { return true, nil }))
	for _, key := range []string{protected, "web-videos/" + id.String() + ".mp4"} {
		run := *tc
		if err := run.prepareOutputScan(id, key); err != nil || (run.scannedOutputs != nil) != (key == protected) {
			t.Fatalf("unexpected scan policy for %q, %v", key, err)
		}
	}
	if tc.scannedOutputs != nil {
		t.Fatal("job-specific scan policy escaped into another job")
	}
}

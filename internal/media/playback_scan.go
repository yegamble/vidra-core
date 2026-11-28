package media

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/vidra/vidra-core/internal/processedupload"
	"github.com/vidra/vidra-core/internal/storage"
)

// SetOutputScanner requires a whole-file scan before each playback object
// reaches storage. Source reads and generation cleanup retain the original
// backend's optional presigning, local-path and prefix-deletion capabilities.
func (t *HLSTranscoder) SetOutputScanner(scanner processedupload.Scanner) {
	t.scannedOutputs = &scannedOutputStore{Backend: t.blobs, scanner: scanner}
}

func (t *HLSTranscoder) outputStore() storage.Backend {
	if t.scannedOutputs != nil {
		return t.scannedOutputs
	}
	return t.blobs
}

type scannedOutputStore struct {
	storage.Backend
	scanner processedupload.Scanner
}

// A codec failure may omit the optional WebM alternate. A scan failure must
// remain visible to the job worker even when it occurs on that alternate.
type playbackScanError struct{ cause error }

func (e *playbackScanError) Error() string { return e.cause.Error() }
func (e *playbackScanError) Unwrap() error { return e.cause }

func (s *scannedOutputStore) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	return s.PutSized(ctx, key, body, storage.SizeOf(body))
}

func (s *scannedOutputStore) PutSized(ctx context.Context, key string, body io.Reader, size int64) (int64, error) {
	clean, err := processedupload.ScanFile(ctx, s.scanner, body, size)
	if err != nil {
		failure := fmt.Errorf("media: scan playback object %q: %w", key, err)
		if errors.Is(err, processedupload.ErrScanTooLarge) {
			failure = permanentf("%s (%d-byte maximum)", failure, processedupload.MaxScannedBytes)
		} else if errors.Is(err, processedupload.ErrInfected) {
			failure = NewPermanentError(failure.Error())
		}
		return 0, &playbackScanError{cause: failure}
	}
	defer clean.Close()
	return storage.PutSized(ctx, s.Backend, key, clean, storage.SizeOf(clean))
}

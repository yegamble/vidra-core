package video

import (
	"bytes"
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/processedupload"
)

// WithGeneratedArtworkScanner scans generated posters and both seek-preview
// files before the video service changes their public storage keys.
func WithGeneratedArtworkScanner(scanner processedupload.Scanner) Option {
	return func(s *Service) { s.artworkScanner = scanner }
}

// WithProtectedArtworkScanner keeps the scan policy attached to a reconstructed
// original across worker restarts and changes to upload admission.
func WithProtectedArtworkScanner(scanner processedupload.Scanner) Option {
	return func(s *Service) { s.protectedArtwork = scanner }
}

func (s *Service) generatedArtwork(ctx context.Context, id uuid.UUID, sourceKey string, data []byte) (io.ReadCloser, error) {
	src := bytes.NewReader(data)
	scanner := s.artworkScanner
	if scanner == nil && sourceKey == processedupload.SourceKey(id) {
		scanner = s.protectedArtwork
		if scanner == nil {
			return nil, processedupload.ErrOutputScannerRequired
		}
	}
	if scanner == nil {
		return io.NopCloser(src), nil
	}
	return processedupload.ScanFile(ctx, scanner, src, int64(len(data)))
}

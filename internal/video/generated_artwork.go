package video

import (
	"bytes"
	"context"
	"io"

	"github.com/vidra/vidra-core/internal/processedupload"
)

// WithGeneratedArtworkScanner scans generated posters and both seek-preview
// files before the video service changes their public storage keys.
func WithGeneratedArtworkScanner(scanner processedupload.Scanner) Option {
	return func(s *Service) { s.artworkScanner = scanner }
}

func (s *Service) generatedArtwork(ctx context.Context, data []byte) (io.ReadCloser, error) {
	src := bytes.NewReader(data)
	if s.artworkScanner == nil {
		return io.NopCloser(src), nil
	}
	return processedupload.ScanFile(ctx, s.artworkScanner, src, int64(len(data)))
}

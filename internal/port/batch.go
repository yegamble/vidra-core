package port

import (
	"context"
	"vidra-core/internal/domain"
)

// VideoBatchCreator defines the batch creation interface for videos.
// Separate from VideoRepository to avoid breaking ~28 mock implementations.
type VideoBatchCreator interface {
	CreateBatch(ctx context.Context, videos []*domain.Video) error
}

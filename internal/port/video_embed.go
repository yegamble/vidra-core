package port

import (
	"vidra-core/internal/domain"
	"context"
)

// VideoEmbedPrivacyRepository defines data operations for video embed privacy.
type VideoEmbedPrivacyRepository interface {
	Get(ctx context.Context, videoID string) (*domain.VideoEmbedPrivacy, error)
	Upsert(ctx context.Context, privacy *domain.VideoEmbedPrivacy) error
	IsDomainAllowed(ctx context.Context, videoID string, domainName string) (bool, error)
}

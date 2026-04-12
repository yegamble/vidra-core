package port

import (
	"context"

	"github.com/google/uuid"

	"vidra-core/internal/domain"
)

// ChannelActivityRepository defines operations for channel activity logging.
type ChannelActivityRepository interface {
	CreateActivity(ctx context.Context, activity *domain.ChannelActivity) error
	ListByChannel(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]domain.ChannelActivity, int64, error)
}

package moderation

import (
	"athena/internal/config"
	"athena/internal/repository"
)

type ModerationHandlers struct {
	moderationRepo *repository.ModerationRepository
	cfg            *config.Config
}

func NewModerationHandlers(
	moderationRepo *repository.ModerationRepository,
	cfg *config.Config,
) *ModerationHandlers {
	return &ModerationHandlers{
		moderationRepo: moderationRepo,
		cfg:            cfg,
	}
}

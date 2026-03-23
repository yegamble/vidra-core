package moderation

import (
	"context"

	adminhandlers "athena/internal/httpapi/handlers/admin"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/usecase"
)

// InstanceHandlers is an alias to the real admin instance handlers.
type InstanceHandlers = adminhandlers.InstanceHandlers

// NewInstanceHandlers keeps moderation tests backward compatible while using
// the real implementation from the admin handler package.
func NewInstanceHandlers(
	moderationRepo *repository.ModerationRepository,
	userRepo usecase.UserRepository,
	videoRepo usecase.VideoRepository,
) *InstanceHandlers {
	return adminhandlers.NewInstanceHandlers(moderationRepo, userRepo, videoRepo)
}

// withUserID adds a user ID to the context (test helper)
//
//nolint:unused // used in test files
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}

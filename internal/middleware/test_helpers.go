package middleware

import (
	"context"

	"github.com/google/uuid"
)

func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, UserIDKey, userID.String())
}

func WithUserRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, UserRoleKey, role)
}

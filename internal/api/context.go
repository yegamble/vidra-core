package api

import "context"

// ctxKey is a private type to avoid collisions in context keys.
type ctxKey string

const userIDKey ctxKey = "userID"

// WithUserID stores the authenticated user ID in the context.
func WithUserID(ctx context.Context, id int64) context.Context {
    return context.WithValue(ctx, userIDKey, id)
}

// UserID extracts the user ID from the context. It returns 0 if not set.
func UserID(ctx context.Context) int64 {
    v := ctx.Value(userIDKey)
    if v == nil {
        return 0
    }
    if id, ok := v.(int64); ok {
        return id
    }
    return 0
}
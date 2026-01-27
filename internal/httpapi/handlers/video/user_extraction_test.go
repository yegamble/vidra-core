package video

import (
	"context"
	"testing"

	"athena/internal/middleware"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestVideoHandler_GetUserIDFromContext(t *testing.T) {
	t.Run("valid user ID", func(t *testing.T) {
		expectedID := uuid.New()
		// middleware.GetUserIDFromContext expects the value to be a string
		ctx := context.WithValue(context.Background(), middleware.UserIDKey, expectedID.String())

		gotID := getUserIDFromContext(ctx)
		assert.NotNil(t, gotID)
		assert.Equal(t, expectedID, *gotID)
	})

	t.Run("no user ID in context", func(t *testing.T) {
		ctx := context.Background()

		gotID := getUserIDFromContext(ctx)
		assert.Nil(t, gotID)
	})

	t.Run("invalid UUID format", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.UserIDKey, "not-a-uuid")

		gotID := getUserIDFromContext(ctx)
		assert.Nil(t, gotID)
	})

	t.Run("wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.UserIDKey, 123)

		gotID := getUserIDFromContext(ctx)
		assert.Nil(t, gotID)
	})
}

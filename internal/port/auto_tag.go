package port

import (
	"context"
	"vidra-core/internal/domain"
)

// AutoTagRepository defines data operations for automatic tag policies.
type AutoTagRepository interface {
	// ListByAccount returns all auto-tag policies for the given account (nil = server-level).
	ListByAccount(ctx context.Context, accountName *string) ([]*domain.AutoTagPolicy, error)
	// ReplaceByAccount atomically replaces all policies for an account.
	ReplaceByAccount(ctx context.Context, accountName *string, policies []*domain.AutoTagPolicy) error
}

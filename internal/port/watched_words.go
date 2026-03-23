package port

import (
	"vidra-core/internal/domain"
	"context"
)

// WatchedWordsRepository defines data operations for watched word lists.
type WatchedWordsRepository interface {
	// ListByAccount returns all watched word lists for the given account (nil = server-level).
	ListByAccount(ctx context.Context, accountName *string) ([]*domain.WatchedWordList, error)
	// GetByID returns a single watched word list by ID.
	GetByID(ctx context.Context, id int64) (*domain.WatchedWordList, error)
	// Create inserts a new watched word list.
	Create(ctx context.Context, list *domain.WatchedWordList) error
	// Update modifies an existing watched word list.
	Update(ctx context.Context, list *domain.WatchedWordList) error
	// Delete removes a watched word list by ID.
	Delete(ctx context.Context, id int64) error
}

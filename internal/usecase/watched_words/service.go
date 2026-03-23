package watchedwords

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"athena/internal/port"
)

// Service handles business logic for watched word lists.
type Service struct {
	repo port.WatchedWordsRepository
}

// NewService creates a new watched words service.
func NewService(repo port.WatchedWordsRepository) *Service {
	return &Service{repo: repo}
}

// ListByAccount returns watched word lists scoped to an account or server-wide (nil).
func (s *Service) ListByAccount(ctx context.Context, accountName *string) ([]*domain.WatchedWordList, error) {
	lists, err := s.repo.ListByAccount(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("listing watched word lists: %w", err)
	}
	return lists, nil
}

// Create creates a new watched word list.
func (s *Service) Create(ctx context.Context, accountName *string, req *domain.CreateWatchedWordListRequest) (*domain.WatchedWordList, error) {
	if req.ListName == "" {
		return nil, domain.ErrValidation
	}
	if len(req.Words) == 0 {
		return nil, domain.ErrValidation
	}

	list := &domain.WatchedWordList{
		AccountName: accountName,
		ListName:    req.ListName,
		Words:       req.Words,
	}

	if err := s.repo.Create(ctx, list); err != nil {
		return nil, fmt.Errorf("creating watched word list: %w", err)
	}

	return list, nil
}

// Update updates an existing watched word list.
func (s *Service) Update(ctx context.Context, id int64, req *domain.UpdateWatchedWordListRequest) (*domain.WatchedWordList, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting watched word list: %w", err)
	}

	if req.ListName != nil {
		existing.ListName = *req.ListName
	}
	if len(req.Words) > 0 {
		existing.Words = req.Words
	}

	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, fmt.Errorf("updating watched word list: %w", err)
	}

	return existing, nil
}

// Delete removes a watched word list.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting watched word list: %w", err)
	}
	return nil
}

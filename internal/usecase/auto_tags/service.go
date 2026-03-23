package autotags

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"athena/internal/port"
)

// Service handles business logic for automatic tag policies.
type Service struct {
	repo         port.AutoTagRepository
	watchedWords port.WatchedWordsRepository
}

// NewService creates a new auto-tags service.
func NewService(repo port.AutoTagRepository, watchedWords port.WatchedWordsRepository) *Service {
	return &Service{
		repo:         repo,
		watchedWords: watchedWords,
	}
}

// GetPolicies returns all auto-tag policies for an account (or server-wide if nil).
func (s *Service) GetPolicies(ctx context.Context, accountName *string) ([]*domain.AutoTagPolicy, error) {
	policies, err := s.repo.ListByAccount(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("listing auto tag policies: %w", err)
	}
	return policies, nil
}

// UpdatePolicies replaces all auto-tag policies for an account.
func (s *Service) UpdatePolicies(ctx context.Context, accountName *string, req *domain.UpdateAutoTagPoliciesRequest) ([]*domain.AutoTagPolicy, error) {
	policies := make([]*domain.AutoTagPolicy, len(req.Policies))
	for i, p := range req.Policies {
		policies[i] = &domain.AutoTagPolicy{
			AccountName: accountName,
			TagType:     p.TagType,
			ReviewType:  p.ReviewType,
			ListID:      p.ListID,
		}
	}

	if err := s.repo.ReplaceByAccount(ctx, accountName, policies); err != nil {
		return nil, fmt.Errorf("updating auto tag policies: %w", err)
	}

	// Return the refreshed list
	return s.repo.ListByAccount(ctx, accountName)
}

// GetAvailableTags returns the list of available tag types and their enabled state.
func (s *Service) GetAvailableTags(ctx context.Context, accountName *string) ([]*domain.AutoTag, error) {
	policies, err := s.repo.ListByAccount(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("listing auto tag policies for available tags: %w", err)
	}

	enabledTypes := make(map[string]bool)
	for _, p := range policies {
		enabledTypes[p.TagType] = true
	}

	tags := []*domain.AutoTag{
		{
			Name:    "External link",
			Type:    "external-link",
			Enabled: enabledTypes["external-link"],
		},
		{
			Name:    "Watched words",
			Type:    "watched-words",
			Enabled: enabledTypes["watched-words"],
		},
	}

	return tags, nil
}

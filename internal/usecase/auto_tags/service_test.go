package autotags

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

type mockAutoTagRepo struct {
	policies []*domain.AutoTagPolicy
	err      error
}

func (m *mockAutoTagRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.AutoTagPolicy, error) {
	return m.policies, m.err
}

func (m *mockAutoTagRepo) ReplaceByAccount(_ context.Context, _ *string, policies []*domain.AutoTagPolicy) error {
	if m.err != nil {
		return m.err
	}
	m.policies = policies
	return nil
}

type mockWWRepo struct{}

func (m *mockWWRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.WatchedWordList, error) {
	return nil, nil
}
func (m *mockWWRepo) GetByID(_ context.Context, _ int64) (*domain.WatchedWordList, error) {
	return nil, nil
}
func (m *mockWWRepo) Create(_ context.Context, _ *domain.WatchedWordList) error { return nil }
func (m *mockWWRepo) Update(_ context.Context, _ *domain.WatchedWordList) error { return nil }
func (m *mockWWRepo) Delete(_ context.Context, _ int64) error                   { return nil }

func TestService_GetPolicies(t *testing.T) {
	expected := []*domain.AutoTagPolicy{
		{ID: 1, TagType: "external-link", ReviewType: "review-comments"},
	}
	repo := &mockAutoTagRepo{policies: expected}
	service := NewService(repo, &mockWWRepo{})

	policies, err := service.GetPolicies(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, expected, policies)
}

func TestService_UpdatePolicies(t *testing.T) {
	repo := &mockAutoTagRepo{policies: []*domain.AutoTagPolicy{}}
	service := NewService(repo, &mockWWRepo{})

	req := &domain.UpdateAutoTagPoliciesRequest{
		Policies: []domain.AutoTagPolicyInput{
			{TagType: "external-link", ReviewType: "review-comments"},
			{TagType: "watched-words", ReviewType: "block-comments"},
		},
	}

	policies, err := service.UpdatePolicies(context.Background(), nil, req)
	require.NoError(t, err)
	assert.Len(t, policies, 2)
}

func TestService_GetAvailableTags_NoneEnabled(t *testing.T) {
	repo := &mockAutoTagRepo{policies: []*domain.AutoTagPolicy{}}
	service := NewService(repo, &mockWWRepo{})

	tags, err := service.GetAvailableTags(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tags, 2)

	for _, tag := range tags {
		assert.False(t, tag.Enabled)
	}
}

func TestService_GetAvailableTags_ExternalLinkEnabled(t *testing.T) {
	repo := &mockAutoTagRepo{
		policies: []*domain.AutoTagPolicy{
			{ID: 1, TagType: "external-link", ReviewType: "review-comments"},
		},
	}
	service := NewService(repo, &mockWWRepo{})

	tags, err := service.GetAvailableTags(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tags, 2)

	// external-link should be enabled
	var externalLink, watchedWords *domain.AutoTag
	for _, tag := range tags {
		switch tag.Type {
		case "external-link":
			externalLink = tag
		case "watched-words":
			watchedWords = tag
		}
	}
	assert.True(t, externalLink.Enabled)
	assert.False(t, watchedWords.Enabled)
}

func TestService_GetAvailableTags_BothEnabled(t *testing.T) {
	repo := &mockAutoTagRepo{
		policies: []*domain.AutoTagPolicy{
			{ID: 1, TagType: "external-link", ReviewType: "review-comments"},
			{ID: 2, TagType: "watched-words", ReviewType: "block-comments"},
		},
	}
	service := NewService(repo, &mockWWRepo{})

	tags, err := service.GetAvailableTags(context.Background(), nil)
	require.NoError(t, err)

	for _, tag := range tags {
		assert.True(t, tag.Enabled)
	}
}

package watchedwords

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

type mockRepo struct {
	lists   []*domain.WatchedWordList
	getByID *domain.WatchedWordList
	err     error
}

func (m *mockRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.WatchedWordList, error) {
	return m.lists, m.err
}

func (m *mockRepo) GetByID(_ context.Context, _ int64) (*domain.WatchedWordList, error) {
	if m.getByID == nil && m.err == nil {
		return nil, domain.ErrWatchedWordListNotFound
	}
	return m.getByID, m.err
}

func (m *mockRepo) Create(_ context.Context, list *domain.WatchedWordList) error {
	if m.err != nil {
		return m.err
	}
	list.ID = 1
	list.CreatedAt = time.Now()
	list.UpdatedAt = time.Now()
	return nil
}

func (m *mockRepo) Update(_ context.Context, _ *domain.WatchedWordList) error {
	return m.err
}

func (m *mockRepo) Delete(_ context.Context, _ int64) error {
	return m.err
}

func TestService_Create_Success(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo)

	req := &domain.CreateWatchedWordListRequest{
		ListName: "profanity",
		Words:    []string{"bad", "word"},
	}

	list, err := service.Create(context.Background(), nil, req)
	require.NoError(t, err)
	assert.Equal(t, int64(1), list.ID)
	assert.Equal(t, "profanity", list.ListName)
	assert.Equal(t, []string{"bad", "word"}, list.Words)
}

func TestService_Create_EmptyName(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo)

	req := &domain.CreateWatchedWordListRequest{
		ListName: "",
		Words:    []string{"word"},
	}

	_, err := service.Create(context.Background(), nil, req)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestService_Create_EmptyWords(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo)

	req := &domain.CreateWatchedWordListRequest{
		ListName: "test",
		Words:    []string{},
	}

	_, err := service.Create(context.Background(), nil, req)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestService_ListByAccount(t *testing.T) {
	expected := []*domain.WatchedWordList{
		{ID: 1, ListName: "test", Words: []string{"word"}},
	}
	repo := &mockRepo{lists: expected}
	service := NewService(repo)

	lists, err := service.ListByAccount(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, expected, lists)
}

func TestService_Update_Success(t *testing.T) {
	existing := &domain.WatchedWordList{
		ID:       1,
		ListName: "old",
		Words:    []string{"old-word"},
	}
	repo := &mockRepo{getByID: existing}
	service := NewService(repo)

	newName := "updated"
	req := &domain.UpdateWatchedWordListRequest{
		ListName: &newName,
		Words:    []string{"new-word"},
	}

	result, err := service.Update(context.Background(), 1, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.ListName)
	assert.Equal(t, []string{"new-word"}, result.Words)
}

func TestService_Update_NotFound(t *testing.T) {
	repo := &mockRepo{err: domain.ErrWatchedWordListNotFound}
	service := NewService(repo)

	req := &domain.UpdateWatchedWordListRequest{}
	_, err := service.Update(context.Background(), 999, req)
	assert.Error(t, err)
}

func TestService_Delete_Success(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo)

	err := service.Delete(context.Background(), 1)
	require.NoError(t, err)
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := &mockRepo{err: domain.ErrWatchedWordListNotFound}
	service := NewService(repo)

	err := service.Delete(context.Background(), 999)
	assert.Error(t, err)
}

package watchedwords

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
	ucww "athena/internal/usecase/watched_words"
)

// mockWatchedWordsRepo implements port.WatchedWordsRepository for testing.
type mockWatchedWordsRepo struct {
	lists   []*domain.WatchedWordList
	getByID *domain.WatchedWordList
	err     error
}

func (m *mockWatchedWordsRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.WatchedWordList, error) {
	return m.lists, m.err
}

func (m *mockWatchedWordsRepo) GetByID(_ context.Context, _ int64) (*domain.WatchedWordList, error) {
	if m.getByID == nil && m.err == nil {
		return nil, domain.ErrWatchedWordListNotFound
	}
	return m.getByID, m.err
}

func (m *mockWatchedWordsRepo) Create(_ context.Context, list *domain.WatchedWordList) error {
	if m.err != nil {
		return m.err
	}
	list.ID = 1
	list.CreatedAt = time.Now()
	list.UpdatedAt = time.Now()
	return nil
}

func (m *mockWatchedWordsRepo) Update(_ context.Context, _ *domain.WatchedWordList) error {
	return m.err
}

func (m *mockWatchedWordsRepo) Delete(_ context.Context, _ int64) error {
	return m.err
}

func withUserContext(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, "550e8400-e29b-41d4-a716-446655440000")
	return r.WithContext(ctx)
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestListAccountWatchedWords(t *testing.T) {
	tests := []struct {
		name       string
		account    string
		lists      []*domain.WatchedWordList
		repoErr    error
		wantStatus int
	}{
		{
			name:    "success",
			account: "testuser",
			lists: []*domain.WatchedWordList{
				{ID: 1, ListName: "profanity", Words: []string{"bad"}},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty account",
			account:    "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockWatchedWordsRepo{lists: tt.lists, err: tt.repoErr}
			service := ucww.NewService(repo)
			h := NewHandlers(service)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = withChiParam(req, "accountName", tt.account)
			rec := httptest.NewRecorder()

			h.ListAccountWatchedWords(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestCreateAccountWatchedWordList(t *testing.T) {
	tests := []struct {
		name       string
		account    string
		body       interface{}
		repoErr    error
		withAuth   bool
		wantStatus int
	}{
		{
			name:    "success",
			account: "testuser",
			body: domain.CreateWatchedWordListRequest{
				ListName: "test",
				Words:    []string{"word1", "word2"},
			},
			withAuth:   true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing auth",
			account:    "testuser",
			body:       domain.CreateWatchedWordListRequest{ListName: "test", Words: []string{"word1"}},
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty account",
			account:    "",
			body:       domain.CreateWatchedWordListRequest{ListName: "test", Words: []string{"word1"}},
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "validation error - empty words",
			account:    "testuser",
			body:       domain.CreateWatchedWordListRequest{ListName: "test", Words: []string{}},
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockWatchedWordsRepo{err: tt.repoErr}
			service := ucww.NewService(repo)
			h := NewHandlers(service)

			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			req = withChiParam(req, "accountName", tt.account)
			if tt.withAuth {
				req = withUserContext(req)
			}
			rec := httptest.NewRecorder()

			h.CreateAccountWatchedWordList(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestDeleteAccountWatchedWordList(t *testing.T) {
	tests := []struct {
		name       string
		listID     string
		repoErr    error
		withAuth   bool
		wantStatus int
	}{
		{
			name:       "success",
			listID:     "1",
			withAuth:   true,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "not found",
			listID:     "999",
			repoErr:    domain.ErrWatchedWordListNotFound,
			withAuth:   true,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid ID",
			listID:     "abc",
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "no auth",
			listID:     "1",
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockWatchedWordsRepo{err: tt.repoErr}
			service := ucww.NewService(repo)
			h := NewHandlers(service)

			req := httptest.NewRequest(http.MethodDelete, "/", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("accountName", "testuser")
			rctx.URLParams.Add("listId", tt.listID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			if tt.withAuth {
				req = withUserContext(req)
			}
			rec := httptest.NewRecorder()

			h.DeleteAccountWatchedWordList(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestListServerWatchedWords(t *testing.T) {
	repo := &mockWatchedWordsRepo{
		lists: []*domain.WatchedWordList{
			{ID: 1, ListName: "server-profanity", Words: []string{"bad"}},
		},
	}
	service := ucww.NewService(repo)
	h := NewHandlers(service)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.ListServerWatchedWords(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp["success"].(bool))
}

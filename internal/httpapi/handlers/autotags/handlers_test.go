package autotags

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	ucat "vidra-core/internal/usecase/auto_tags"
)

// mockAutoTagRepo implements port.AutoTagRepository for testing.
type mockAutoTagRepo struct {
	policies []*domain.AutoTagPolicy
	err      error
}

func (m *mockAutoTagRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.AutoTagPolicy, error) {
	return m.policies, m.err
}

func (m *mockAutoTagRepo) ReplaceByAccount(_ context.Context, _ *string, _ []*domain.AutoTagPolicy) error {
	return m.err
}

// mockWatchedWordsRepo implements port.WatchedWordsRepository for testing.
type mockWatchedWordsRepo struct{}

func (m *mockWatchedWordsRepo) ListByAccount(_ context.Context, _ *string) ([]*domain.WatchedWordList, error) {
	return nil, nil
}
func (m *mockWatchedWordsRepo) GetByID(_ context.Context, _ int64) (*domain.WatchedWordList, error) {
	return nil, nil
}
func (m *mockWatchedWordsRepo) Create(_ context.Context, _ *domain.WatchedWordList) error {
	return nil
}
func (m *mockWatchedWordsRepo) Update(_ context.Context, _ *domain.WatchedWordList) error {
	return nil
}
func (m *mockWatchedWordsRepo) Delete(_ context.Context, _ int64) error { return nil }

func withUserContext(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, "550e8400-e29b-41d4-a716-446655440000")
	return r.WithContext(ctx)
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetAccountAutoTagPolicies(t *testing.T) {
	tests := []struct {
		name       string
		account    string
		policies   []*domain.AutoTagPolicy
		repoErr    error
		wantStatus int
	}{
		{
			name:    "success with policies",
			account: "testuser",
			policies: []*domain.AutoTagPolicy{
				{ID: 1, TagType: "external-link", ReviewType: "review-comments"},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "success empty",
			account:    "testuser",
			policies:   []*domain.AutoTagPolicy{},
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
			repo := &mockAutoTagRepo{policies: tt.policies, err: tt.repoErr}
			service := ucat.NewService(repo, &mockWatchedWordsRepo{})
			h := NewHandlers(service)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = withChiParam(req, "accountName", tt.account)
			rec := httptest.NewRecorder()

			h.GetAccountAutoTagPolicies(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestUpdateAccountAutoTagPolicies(t *testing.T) {
	tests := []struct {
		name       string
		account    string
		body       interface{}
		withAuth   bool
		repoErr    error
		wantStatus int
	}{
		{
			name:    "success",
			account: "testuser",
			body: domain.UpdateAutoTagPoliciesRequest{
				Policies: []domain.AutoTagPolicyInput{
					{TagType: "external-link", ReviewType: "review-comments"},
				},
			},
			withAuth:   true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "no auth",
			account:    "testuser",
			body:       domain.UpdateAutoTagPoliciesRequest{Policies: []domain.AutoTagPolicyInput{}},
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty account",
			account:    "",
			body:       domain.UpdateAutoTagPoliciesRequest{},
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockAutoTagRepo{policies: []*domain.AutoTagPolicy{}, err: tt.repoErr}
			service := ucat.NewService(repo, &mockWatchedWordsRepo{})
			h := NewHandlers(service)

			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			req = withChiParam(req, "accountName", tt.account)
			if tt.withAuth {
				req = withUserContext(req)
			}
			rec := httptest.NewRecorder()

			h.UpdateAccountAutoTagPolicies(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestGetAccountAvailableTags(t *testing.T) {
	repo := &mockAutoTagRepo{
		policies: []*domain.AutoTagPolicy{
			{ID: 1, TagType: "external-link", ReviewType: "review-comments"},
		},
	}
	service := ucat.NewService(repo, &mockWatchedWordsRepo{})
	h := NewHandlers(service)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "accountName", "testuser")
	rec := httptest.NewRecorder()

	h.GetAccountAvailableTags(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp["success"].(bool))
}

func TestGetServerAvailableTags(t *testing.T) {
	repo := &mockAutoTagRepo{policies: []*domain.AutoTagPolicy{}}
	service := ucat.NewService(repo, &mockWatchedWordsRepo{})
	h := NewHandlers(service)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.GetServerAvailableTags(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	tags := resp["data"].([]interface{})
	assert.Len(t, tags, 2)
}

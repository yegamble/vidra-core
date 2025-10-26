package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/usecase"
)

type mockSubRepo struct {
	usecase.SubscriptionRepository
	gotLimit  int
	gotOffset int
}

func (m *mockSubRepo) Subscribe(_ context.Context, _, _ string) error   { return nil }
func (m *mockSubRepo) Unsubscribe(_ context.Context, _, _ string) error { return nil }
func (m *mockSubRepo) ListSubscriptions(_ context.Context, _ string, limit, offset int) ([]*domain.User, int64, error) {
	m.gotLimit, m.gotOffset = limit, offset
	return []*domain.User{}, 0, nil
}
func (m *mockSubRepo) ListSubscriptionVideos(_ context.Context, _ string, limit, offset int) ([]*domain.Video, int64, error) {
	m.gotLimit, m.gotOffset = limit, offset
	return []*domain.Video{}, 0, nil
}

func TestListMySubscriptions_Pagination_PageParams(t *testing.T) {
	repo := &mockSubRepo{}
	h := ListMySubscriptionsHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/subscriptions?page=2&pageSize=10", nil)
	// Inject auth user id
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.gotLimit != 10 || repo.gotOffset != 10 {
		t.Fatalf("repo got limit/offset = %d/%d", repo.gotLimit, repo.gotOffset)
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta == nil || resp.Meta.Page != 2 || resp.Meta.PageSize != 10 {
		t.Fatalf("unexpected meta: %+v", resp.Meta)
	}
}

func TestListSubscriptionVideos_Pagination_PageParams(t *testing.T) {
	repo := &mockSubRepo{}
	h := ListSubscriptionVideosHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/subscriptions?page=3&pageSize=5", nil)
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.gotLimit != 5 || repo.gotOffset != 10 {
		t.Fatalf("repo got limit/offset = %d/%d", repo.gotLimit, repo.gotOffset)
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta == nil || resp.Meta.Page != 3 || resp.Meta.PageSize != 5 {
		t.Fatalf("unexpected meta: %+v", resp.Meta)
	}
}

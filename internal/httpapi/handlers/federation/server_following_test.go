package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
)

type mockServerFollowingRepo struct {
	followers []*domain.ServerFollowing
	following []*domain.ServerFollowing
	err       error
	lastHost  string
	lastState domain.ServerFollowingState
}

func (m *mockServerFollowingRepo) ListFollowers(_ context.Context) ([]*domain.ServerFollowing, error) {
	return m.followers, m.err
}
func (m *mockServerFollowingRepo) ListFollowing(_ context.Context) ([]*domain.ServerFollowing, error) {
	return m.following, m.err
}
func (m *mockServerFollowingRepo) Follow(_ context.Context, host string) error {
	m.lastHost = host
	return m.err
}
func (m *mockServerFollowingRepo) Unfollow(_ context.Context, host string) error {
	m.lastHost = host
	return m.err
}
func (m *mockServerFollowingRepo) SetFollowerState(_ context.Context, host string, state domain.ServerFollowingState) error {
	m.lastHost = host
	m.lastState = state
	return m.err
}
func (m *mockServerFollowingRepo) DeleteFollower(_ context.Context, host string) error {
	m.lastHost = host
	return m.err
}

func TestListFollowers_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{
		followers: []*domain.ServerFollowing{
			{ID: "1", Host: "peer.example.com", State: domain.ServerFollowingStateAccepted, Follower: true},
		},
	}
	h := NewServerFollowingHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/server/followers", nil)
	rr := httptest.NewRecorder()
	h.ListFollowers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []*domain.ServerFollowing `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 follower, got %d", len(resp.Data))
	}
}

func TestListFollowing_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{
		following: []*domain.ServerFollowing{
			{ID: "2", Host: "upstream.example.com", State: domain.ServerFollowingStateAccepted, Follower: false},
		},
	}
	h := NewServerFollowingHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/server/following", nil)
	rr := httptest.NewRecorder()
	h.ListFollowing(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFollowInstance_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo)

	body := `{"host":"new.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/server/following", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FollowInstance(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastHost != "new.example.com" {
		t.Fatalf("expected host=new.example.com, got %q", repo.lastHost)
	}
}

func TestUnfollowInstance_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo)

	r := chi.NewRouter()
	r.Delete("/server/following/{host}", h.UnfollowInstance)

	req := httptest.NewRequest(http.MethodDelete, "/server/following/old.example.com", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAcceptFollower_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo)

	r := chi.NewRouter()
	r.Post("/server/followers/{host}/accept", h.AcceptFollower)

	req := httptest.NewRequest(http.MethodPost, "/server/followers/peer.example.com/accept", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastState != domain.ServerFollowingStateAccepted {
		t.Fatalf("expected state=accepted, got %q", repo.lastState)
	}
}

func TestRejectFollower_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo)

	r := chi.NewRouter()
	r.Post("/server/followers/{host}/reject", h.RejectFollower)

	req := httptest.NewRequest(http.MethodPost, "/server/followers/peer.example.com/reject", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastState != domain.ServerFollowingStateRejected {
		t.Fatalf("expected state=rejected, got %q", repo.lastState)
	}
}

func TestDeleteFollower_OK(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo)

	r := chi.NewRouter()
	r.Delete("/server/followers/{host}", h.DeleteFollower)

	req := httptest.NewRequest(http.MethodDelete, "/server/followers/peer.example.com", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastHost != "peer.example.com" {
		t.Fatalf("expected host=peer.example.com, got %q", repo.lastHost)
	}
}

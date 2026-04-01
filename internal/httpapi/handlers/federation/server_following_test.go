package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// mockActivityPubService is a minimal ActivityPubService mock for testing
// Follow activity emission in FollowInstance.
type mockActivityPubService struct {
	fetchCh    chan string
	deliverCh  chan string
	fetchErr   error
	deliverErr error
}

func newMockAPService() *mockActivityPubService {
	return &mockActivityPubService{
		fetchCh:   make(chan string, 1),
		deliverCh: make(chan string, 1),
	}
}

func (m *mockActivityPubService) FetchRemoteActor(_ context.Context, uri string) (*domain.APRemoteActor, error) {
	m.fetchCh <- uri
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	return &domain.APRemoteActor{
		ActorURI: uri,
		InboxURL: "https://peer.example.com/inbox",
	}, nil
}

func (m *mockActivityPubService) DeliverActivity(_ context.Context, _, inboxURL string, _ interface{}) error {
	m.deliverCh <- inboxURL
	return m.deliverErr
}

// Stub implementations for unused methods.
func (m *mockActivityPubService) GetLocalActor(_ context.Context, _ string) (*domain.Actor, error) {
	return nil, nil
}
func (m *mockActivityPubService) HandleInboxActivity(_ context.Context, _ map[string]interface{}, _ *http.Request) error {
	return nil
}
func (m *mockActivityPubService) GetOutbox(_ context.Context, _ string, _, _ int) (*domain.OrderedCollectionPage, error) {
	return nil, nil
}
func (m *mockActivityPubService) GetFollowers(_ context.Context, _ string, _, _ int) (*domain.OrderedCollectionPage, error) {
	return nil, nil
}
func (m *mockActivityPubService) GetFollowing(_ context.Context, _ string, _, _ int) (*domain.OrderedCollectionPage, error) {
	return nil, nil
}
func (m *mockActivityPubService) GetOutboxCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (m *mockActivityPubService) GetFollowersCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (m *mockActivityPubService) GetFollowingCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

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

// TestFollowInstance_WithAPService_EmitsFollowActivity verifies that when an
// ActivityPub service is configured, FollowInstance asynchronously delivers
// a Follow activity to the target instance's service actor inbox.
func TestFollowInstance_WithAPService_EmitsFollowActivity(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	apSvc := newMockAPService()
	h := NewServerFollowingHandlers(repo)
	h.SetActivityPubService(apSvc, "https://my.instance.com")

	actorID := uuid.New().String()
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, actorID)
	body := `{"host":"peer.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/server/following", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FollowInstance(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastHost != "peer.example.com" {
		t.Fatalf("expected host=peer.example.com, got %q", repo.lastHost)
	}

	// Verify FetchRemoteActor called with PeerTube-compatible server actor URI.
	select {
	case fetched := <-apSvc.fetchCh:
		if fetched != "https://peer.example.com/accounts/peertube" {
			t.Fatalf("expected fetch for peertube server actor, got %q", fetched)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AP service FetchRemoteActor was not called within 100ms")
	}

	// Verify DeliverActivity called with the resolved inbox URL.
	select {
	case delivered := <-apSvc.deliverCh:
		if delivered != "https://peer.example.com/inbox" {
			t.Fatalf("expected delivery to peer inbox, got %q", delivered)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AP service DeliverActivity was not called within 100ms")
	}
}

// TestFollowInstance_WithAPService_FetchError verifies that a FetchRemoteActor
// failure is logged and does not prevent the 204 response.
func TestFollowInstance_WithAPService_FetchError(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	apSvc := newMockAPService()
	apSvc.fetchErr = context.DeadlineExceeded
	h := NewServerFollowingHandlers(repo)
	h.SetActivityPubService(apSvc, "https://my.instance.com")

	actorID := uuid.New().String()
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, actorID)
	body := `{"host":"slow.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/server/following", strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.FollowInstance(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 even on AP error, got %d", rr.Code)
	}

	// FetchRemoteActor should still have been attempted.
	select {
	case <-apSvc.fetchCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AP service FetchRemoteActor was not called within 100ms")
	}
}

// TestFollowInstance_NoAPService_StillReturns204 verifies that when no AP
// service is configured (nil), FollowInstance still works correctly.
func TestFollowInstance_NoAPService_StillReturns204(t *testing.T) {
	repo := &mockServerFollowingRepo{}
	h := NewServerFollowingHandlers(repo) // no AP service

	body := `{"host":"new.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/server/following", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FollowInstance(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

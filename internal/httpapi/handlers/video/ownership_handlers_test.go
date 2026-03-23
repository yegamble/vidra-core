package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mockOwnershipRepo is a minimal VideoOwnershipRepository for handler tests.
type mockOwnershipRepo struct {
	port.VideoOwnershipRepository
	created    []*domain.VideoOwnershipChange
	changes    map[string]*domain.VideoOwnershipChange // id → change
	pendingFor map[string][]*domain.VideoOwnershipChange
	createErr  error
	updateErr  error
}

func (m *mockOwnershipRepo) Create(_ context.Context, c *domain.VideoOwnershipChange) error {
	if m.createErr != nil {
		return m.createErr
	}
	if m.changes == nil {
		m.changes = make(map[string]*domain.VideoOwnershipChange)
	}
	c.ID = uuid.New()
	m.created = append(m.created, c)
	m.changes[c.ID.String()] = c
	return nil
}

func (m *mockOwnershipRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.VideoOwnershipChange, error) {
	if c, ok := m.changes[id.String()]; ok {
		return c, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockOwnershipRepo) ListPendingForUser(_ context.Context, userID string) ([]*domain.VideoOwnershipChange, error) {
	return m.pendingFor[userID], nil
}

func (m *mockOwnershipRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.VideoOwnershipChangeStatus) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if c, ok := m.changes[id.String()]; ok {
		c.Status = status
	}
	return nil
}

// mockVideoRepoOwnership is a minimal VideoRepository for ownership handler tests.
type mockVideoRepoOwnership struct {
	usecase interface{}
	videos  map[string]*domain.Video
}

func (m *mockVideoRepoOwnership) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if v, ok := m.videos[id]; ok {
		return v, nil
	}
	return nil, domain.ErrVideoNotFound
}

func (m *mockVideoRepoOwnership) Update(_ context.Context, v *domain.Video) error {
	if m.videos == nil {
		m.videos = make(map[string]*domain.Video)
	}
	m.videos[v.ID] = v
	return nil
}

func withVideoIDParam(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withOwnershipIDParam(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestGiveOwnership_OK verifies 204 when a video owner initiates transfer.
func TestGiveOwnership_OK(t *testing.T) {
	ownerID := uuid.New().String()
	videoID := uuid.New().String()
	newOwnerID := uuid.New().String()

	videoRepo := &mockVideoRepoOwnership{
		videos: map[string]*domain.Video{
			videoID: {ID: videoID, UserID: ownerID},
		},
	}
	ownershipRepo := &mockOwnershipRepo{}

	h := GiveOwnershipHandler(ownershipRepo, videoRepo)

	body := `{"username":"` + newOwnerID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/videos/"+videoID+"/give-ownership", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, ownerID)
	req = withVideoIDParam(req, videoID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(ownershipRepo.created) != 1 {
		t.Fatalf("expected 1 ownership change created, got %d", len(ownershipRepo.created))
	}
}

// TestGiveOwnership_NotOwner verifies 403 when a non-owner attempts transfer.
func TestGiveOwnership_NotOwner(t *testing.T) {
	ownerID := uuid.New().String()
	otherID := uuid.New().String()
	videoID := uuid.New().String()

	videoRepo := &mockVideoRepoOwnership{
		videos: map[string]*domain.Video{
			videoID: {ID: videoID, UserID: ownerID},
		},
	}
	ownershipRepo := &mockOwnershipRepo{}
	h := GiveOwnershipHandler(ownershipRepo, videoRepo)

	body := `{"username":"someone"}`
	req := httptest.NewRequest(http.MethodPost, "/videos/"+videoID+"/give-ownership", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, otherID)
	req = withVideoIDParam(req, videoID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGiveOwnership_VideoNotFound verifies 404 for unknown video.
func TestGiveOwnership_VideoNotFound(t *testing.T) {
	ownerID := uuid.New().String()
	videoRepo := &mockVideoRepoOwnership{videos: map[string]*domain.Video{}}
	ownershipRepo := &mockOwnershipRepo{}
	h := GiveOwnershipHandler(ownershipRepo, videoRepo)

	req := httptest.NewRequest(http.MethodPost, "/videos/x/give-ownership", strings.NewReader(`{"username":"u"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, ownerID)
	req = withVideoIDParam(req, "missing-id")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGiveOwnership_Unauthenticated verifies 401 with no auth.
func TestGiveOwnership_Unauthenticated(t *testing.T) {
	videoRepo := &mockVideoRepoOwnership{}
	ownershipRepo := &mockOwnershipRepo{}
	h := GiveOwnershipHandler(ownershipRepo, videoRepo)

	req := httptest.NewRequest(http.MethodPost, "/videos/x/give-ownership", strings.NewReader(`{"username":"u"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestListOwnershipChanges_ReturnsChanges verifies the list endpoint returns pending requests.
func TestListOwnershipChanges_ReturnsChanges(t *testing.T) {
	userID := uuid.New().String()
	change := &domain.VideoOwnershipChange{
		ID:          uuid.New(),
		VideoID:     "vid-1",
		InitiatorID: "other-user",
		NextOwnerID: userID,
		Status:      domain.VideoOwnershipChangePending,
	}
	repo := &mockOwnershipRepo{
		pendingFor: map[string][]*domain.VideoOwnershipChange{
			userID: {change},
		},
	}
	h := ListOwnershipChangesHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/users/me/videos/ownership", nil)
	req = withUserID(req, userID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data struct {
			Total int `json:"total"`
			Data  []struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Total != 1 {
		t.Errorf("expected total=1, got %d", resp.Data.Total)
	}
}

// TestAcceptOwnership_OK verifies 204 + video owner updated on accept.
func TestAcceptOwnership_OK(t *testing.T) {
	newOwnerID := uuid.New().String()
	videoID := uuid.New().String()
	changeID := uuid.New()

	change := &domain.VideoOwnershipChange{
		ID:          changeID,
		VideoID:     videoID,
		InitiatorID: uuid.New().String(),
		NextOwnerID: newOwnerID,
		Status:      domain.VideoOwnershipChangePending,
	}
	ownershipRepo := &mockOwnershipRepo{
		changes: map[string]*domain.VideoOwnershipChange{
			changeID.String(): change,
		},
	}
	videoRepo := &mockVideoRepoOwnership{
		videos: map[string]*domain.Video{
			videoID: {ID: videoID, UserID: "old-owner"},
		},
	}
	h := AcceptOwnershipHandler(ownershipRepo, videoRepo)

	req := httptest.NewRequest(http.MethodPost, "/users/me/videos/ownership/"+changeID.String()+"/accept", nil)
	req = withUserID(req, newOwnerID)
	req = withOwnershipIDParam(req, changeID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if change.Status != domain.VideoOwnershipChangeAccepted {
		t.Errorf("expected status=accepted, got %s", change.Status)
	}
	if videoRepo.videos[videoID].UserID != newOwnerID {
		t.Errorf("expected video owner to be updated to %s, got %s", newOwnerID, videoRepo.videos[videoID].UserID)
	}
}

// TestAcceptOwnership_WrongUser verifies 403 when non-next-owner tries to accept.
func TestAcceptOwnership_WrongUser(t *testing.T) {
	changeID := uuid.New()
	change := &domain.VideoOwnershipChange{
		ID:          changeID,
		NextOwnerID: uuid.New().String(),
		Status:      domain.VideoOwnershipChangePending,
	}
	ownershipRepo := &mockOwnershipRepo{
		changes: map[string]*domain.VideoOwnershipChange{
			changeID.String(): change,
		},
	}
	videoRepo := &mockVideoRepoOwnership{}
	h := AcceptOwnershipHandler(ownershipRepo, videoRepo)

	req := httptest.NewRequest(http.MethodPost, "/users/me/videos/ownership/"+changeID.String()+"/accept", nil)
	req = withUserID(req, uuid.New().String()) // different user
	req = withOwnershipIDParam(req, changeID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestRefuseOwnership_OK verifies 204 on refuse.
func TestRefuseOwnership_OK(t *testing.T) {
	userID := uuid.New().String()
	changeID := uuid.New()
	change := &domain.VideoOwnershipChange{
		ID:          changeID,
		NextOwnerID: userID,
		Status:      domain.VideoOwnershipChangePending,
	}
	ownershipRepo := &mockOwnershipRepo{
		changes: map[string]*domain.VideoOwnershipChange{
			changeID.String(): change,
		},
	}
	h := RefuseOwnershipHandler(ownershipRepo)

	req := httptest.NewRequest(http.MethodPost, "/users/me/videos/ownership/"+changeID.String()+"/refuse", nil)
	req = withUserID(req, userID)
	req = withOwnershipIDParam(req, changeID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if change.Status != domain.VideoOwnershipChangeRefused {
		t.Errorf("expected status=refused, got %s", change.Status)
	}
}

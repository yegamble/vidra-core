package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
)

// mockRegistrationRepo satisfies RegistrationRepository for tests.
type mockRegistrationRepo struct {
	registrations []*domain.UserRegistration
	err           error
}

func (m *mockRegistrationRepo) ListPending(_ context.Context) ([]*domain.UserRegistration, error) {
	if m.err != nil {
		return nil, m.err
	}
	var pending []*domain.UserRegistration
	for _, r := range m.registrations {
		if r.Status == "pending" {
			pending = append(pending, r)
		}
	}
	return pending, nil
}

func (m *mockRegistrationRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.UserRegistration, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, r := range m.registrations {
		if r.ID == id {
			c := *r
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *mockRegistrationRepo) UpdateStatus(_ context.Context, id uuid.UUID, status, response string) error {
	if m.err != nil {
		return m.err
	}
	for _, r := range m.registrations {
		if r.ID == id {
			r.Status = status
			r.ModeratorResponse = response
			return nil
		}
	}
	return domain.ErrNotFound
}

func (m *mockRegistrationRepo) Delete(_ context.Context, id uuid.UUID) error {
	if m.err != nil {
		return m.err
	}
	for i, r := range m.registrations {
		if r.ID == id {
			m.registrations = append(m.registrations[:i], m.registrations[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

type mockUserCreator struct {
	created []*domain.User
	err     error
}

func (m *mockUserCreator) Create(_ context.Context, user *domain.User, _ string) error {
	if m.err != nil {
		return m.err
	}
	m.created = append(m.created, user)
	return nil
}

func newTestRegistrations() []*domain.UserRegistration {
	now := time.Now()
	return []*domain.UserRegistration{
		{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Username: "alice", Email: "alice@example.com", Status: "pending", CreatedAt: now},
		{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Username: "bob", Email: "bob@example.com", Status: "accepted", CreatedAt: now},
	}
}

func TestListRegistrations_OK(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	h := NewRegistrationHandlers(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/registrations", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.ListRegistrations(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool                       `json:"success"`
		Data    []*domain.UserRegistration `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 pending registration, got %d", len(resp.Data))
	}
}

func TestAcceptRegistration_OK(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	creator := &mockUserCreator{}
	h := NewRegistrationHandlers(repo, creator)

	r := chi.NewRouter()
	r.Post("/admin/registrations/{id}/accept", h.AcceptRegistration)

	body := `{"moderatorMessage":"Welcome!"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/registrations/11111111-1111-1111-1111-111111111111/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.registrations[0].Status != "accepted" {
		t.Fatalf("expected status=accepted, got %q", repo.registrations[0].Status)
	}
	if len(creator.created) != 1 || creator.created[0].Username != "alice" {
		t.Fatalf("expected user alice to be created, got %v", creator.created)
	}
}

func TestAcceptRegistration_AliasParam_OK(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	creator := &mockUserCreator{}
	h := NewRegistrationHandlers(repo, creator)

	r := chi.NewRouter()
	r.Post("/users/registrations/{registrationId}/accept", h.AcceptRegistration)

	req := httptest.NewRequest(http.MethodPost, "/users/registrations/11111111-1111-1111-1111-111111111111/accept", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.registrations[0].Status != "accepted" {
		t.Fatalf("expected status=accepted, got %q", repo.registrations[0].Status)
	}
}

func TestRejectRegistration_OK(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	h := NewRegistrationHandlers(repo, nil)

	r := chi.NewRouter()
	r.Post("/admin/registrations/{id}/reject", h.RejectRegistration)

	body := `{"moderatorMessage":"Spam account"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/registrations/11111111-1111-1111-1111-111111111111/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.registrations[0].Status != "rejected" {
		t.Fatalf("expected status=rejected, got %q", repo.registrations[0].Status)
	}
}

func TestDeleteRegistration_OK(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	h := NewRegistrationHandlers(repo, nil)

	r := chi.NewRouter()
	r.Delete("/admin/registrations/{id}", h.DeleteRegistration)

	req := httptest.NewRequest(http.MethodDelete, "/admin/registrations/11111111-1111-1111-1111-111111111111", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(repo.registrations) != 1 {
		t.Fatalf("expected 1 registration remaining, got %d", len(repo.registrations))
	}
}

func TestDeleteRegistration_NotFound(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	h := NewRegistrationHandlers(repo, nil)

	r := chi.NewRouter()
	r.Delete("/admin/registrations/{id}", h.DeleteRegistration)

	req := httptest.NewRequest(http.MethodDelete, "/admin/registrations/ffffffff-ffff-ffff-ffff-ffffffffffff", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAcceptRegistration_NotFound(t *testing.T) {
	repo := &mockRegistrationRepo{registrations: newTestRegistrations()}
	h := NewRegistrationHandlers(repo, nil)

	r := chi.NewRouter()
	r.Post("/admin/registrations/{id}/accept", h.AcceptRegistration)

	req := httptest.NewRequest(http.MethodPost, "/admin/registrations/ffffffff-ffff-ffff-ffff-ffffffffffff/accept", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

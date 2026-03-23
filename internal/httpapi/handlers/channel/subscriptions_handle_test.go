package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mockSubRepoHandle provides minimal SubscriptionRepository for handle-based subscription tests.
type mockSubRepoHandle struct {
	usecase.SubscriptionRepository
	subscriptions  map[string]bool // channelID.String() → bool
	subscribeErr   error
	unsubscribeErr error
}

func (m *mockSubRepoHandle) IsSubscribed(_ context.Context, _ uuid.UUID, channelID uuid.UUID) (bool, error) {
	return m.subscriptions[channelID.String()], nil
}

func (m *mockSubRepoHandle) SubscribeToChannel(_ context.Context, _ uuid.UUID, channelID uuid.UUID) error {
	if m.subscribeErr != nil {
		return m.subscribeErr
	}
	if m.subscriptions == nil {
		m.subscriptions = make(map[string]bool)
	}
	m.subscriptions[channelID.String()] = true
	return nil
}

func (m *mockSubRepoHandle) UnsubscribeFromChannel(_ context.Context, _ uuid.UUID, channelID uuid.UUID) error {
	if m.unsubscribeErr != nil {
		return m.unsubscribeErr
	}
	delete(m.subscriptions, channelID.String())
	return nil
}

func withSubscriptionHandle(r *http.Request, handle string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("subscriptionHandle", handle)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestGetSubscriptionByHandle_Subscribed verifies 200 when subscribed.
func TestGetSubscriptionByHandle_Subscribed(t *testing.T) {
	subID := uuid.New()
	chID := uuid.New()
	repo := &mockSubRepoHandle{subscriptions: map[string]bool{chID.String(): true}}
	h := GetSubscriptionByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/"+chID.String(), nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetSubscriptionByHandle_NotSubscribed verifies 404 when not subscribed.
func TestGetSubscriptionByHandle_NotSubscribed(t *testing.T) {
	subID := uuid.New()
	chID := uuid.New()
	repo := &mockSubRepoHandle{subscriptions: map[string]bool{}}
	h := GetSubscriptionByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/"+chID.String(), nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetSubscriptionByHandle_Unauthenticated verifies 401 when not logged in.
func TestGetSubscriptionByHandle_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockSubRepoHandle{}
	h := GetSubscriptionByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/"+chID.String(), nil)
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestSubscribeByHandle_OK verifies 204 on successful subscribe.
func TestSubscribeByHandle_OK(t *testing.T) {
	subID := uuid.New()
	chID := uuid.New()
	repo := &mockSubRepoHandle{subscriptions: map[string]bool{}}
	h := SubscribeByHandleHandler(repo, nil)

	body := `{"uri":"` + chID.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/users/me/subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !repo.subscriptions[chID.String()] {
		t.Fatal("expected channel to be subscribed after handler")
	}
}

// TestSubscribeByHandle_MissingURI verifies 400 when URI is empty.
func TestSubscribeByHandle_MissingURI(t *testing.T) {
	subID := uuid.New()
	repo := &mockSubRepoHandle{}
	h := SubscribeByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/users/me/subscriptions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSubscribeByHandle_Unauthenticated verifies 401 when not logged in.
func TestSubscribeByHandle_Unauthenticated(t *testing.T) {
	repo := &mockSubRepoHandle{}
	h := SubscribeByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/users/me/subscriptions", strings.NewReader(`{"uri":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestUnsubscribeByHandle_OK verifies 204 on successful unsubscribe.
func TestUnsubscribeByHandle_OK(t *testing.T) {
	subID := uuid.New()
	chID := uuid.New()
	repo := &mockSubRepoHandle{subscriptions: map[string]bool{chID.String(): true}}
	h := UnsubscribeByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodDelete, "/users/me/subscriptions/"+chID.String(), nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestUnsubscribeByHandle_Unauthenticated verifies 401 when not logged in.
func TestUnsubscribeByHandle_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockSubRepoHandle{}
	h := UnsubscribeByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodDelete, "/users/me/subscriptions/"+chID.String(), nil)
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestGetSubscriptionByHandle_InvalidHandle verifies 404 for unknown handle (nil channelSvc).
func TestGetSubscriptionByHandle_InvalidHandle(t *testing.T) {
	subID := uuid.New()
	repo := &mockSubRepoHandle{}
	h := GetSubscriptionByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/not-a-uuid", nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	req = withSubscriptionHandle(req, "not-a-uuid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetSubscriptionByHandle_ResponseHasChannelID verifies response shape.
func TestGetSubscriptionByHandle_ResponseHasChannelID(t *testing.T) {
	subID := uuid.New()
	chID := uuid.New()
	repo := &mockSubRepoHandle{subscriptions: map[string]bool{chID.String(): true}}
	h := GetSubscriptionByHandleHandler(repo, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/"+chID.String(), nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	req = withSubscriptionHandle(req, chID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct {
		Data struct {
			Subscribed bool   `json:"subscribed"`
			ChannelID  string `json:"channelId"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Data.Subscribed {
		t.Errorf("expected subscribed=true")
	}
	if resp.Data.ChannelID != chID.String() {
		t.Errorf("expected channelId=%s, got %s", chID.String(), resp.Data.ChannelID)
	}
}

package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/usecase"

	"github.com/google/uuid"
)

type mockSubRepoExist struct {
	usecase.SubscriptionRepository
	subscribed map[string]bool // channelID.String() → bool
}

func (m *mockSubRepoExist) IsSubscribed(_ context.Context, _ uuid.UUID, channelID uuid.UUID) (bool, error) {
	return m.subscribed[channelID.String()], nil
}

func TestCheckSubscriptionsExist_SubscribedAndNot(t *testing.T) {
	subID := uuid.New()
	ch1 := uuid.New()
	ch2 := uuid.New()

	repo := &mockSubRepoExist{
		subscribed: map[string]bool{ch1.String(): true},
	}

	h := CheckSubscriptionsExistHandler(repo, nil)
	url := "/users/me/subscriptions/exist?uris=" + ch1.String() + "," + ch2.String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool            `json:"success"`
		Data    map[string]bool `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Data[ch1.String()] {
		t.Fatalf("expected ch1 to be subscribed, got false")
	}
	if resp.Data[ch2.String()] {
		t.Fatalf("expected ch2 to not be subscribed, got true")
	}
}

func TestCheckSubscriptionsExist_Unauthenticated(t *testing.T) {
	repo := &mockSubRepoExist{subscribed: map[string]bool{}}
	h := CheckSubscriptionsExistHandler(repo, nil)
	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/exist?uris="+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestCheckSubscriptionsExist_EmptyURIs(t *testing.T) {
	subID := uuid.New()
	repo := &mockSubRepoExist{subscribed: map[string]bool{}}
	h := CheckSubscriptionsExistHandler(repo, nil)
	req := httptest.NewRequest(http.MethodGet, "/users/me/subscriptions/exist", nil)
	req = req.WithContext(withUserID(req.Context(), subID.String()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

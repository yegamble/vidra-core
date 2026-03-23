package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// mockNotificationPrefRepo satisfies NotificationPrefRepository for handler tests.
type mockNotificationPrefRepo struct {
	prefs *domain.NotificationPreferences
	err   error
}

func (m *mockNotificationPrefRepo) GetPreferences(_ context.Context, userID string) (*domain.NotificationPreferences, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.prefs != nil {
		return m.prefs, nil
	}
	return domain.DefaultNotificationPreferences(userID), nil
}

func (m *mockNotificationPrefRepo) UpsertPreferences(_ context.Context, prefs *domain.NotificationPreferences) error {
	if m.err != nil {
		return m.err
	}
	m.prefs = prefs
	return nil
}

func withUserContext(req *http.Request, userID string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
}

func decodeNotifPrefResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestGetNotificationPreferences_Success(t *testing.T) {
	repo := &mockNotificationPrefRepo{}
	h := GetNotificationPreferencesHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/users/me/notification-preferences", nil)
	req = withUserContext(req, "user-123")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeNotifPrefResponse(t, rr)
	if !resp["success"].(bool) {
		t.Fatal("expected success=true")
	}
	data := resp["data"].(map[string]interface{})
	// Default preferences should have all toggles enabled
	for _, field := range []string{"comment", "like", "subscribe", "mention", "reply", "upload", "system", "email_enabled"} {
		if !data[field].(bool) {
			t.Errorf("expected default preference %q to be true", field)
		}
	}
}

func TestGetNotificationPreferences_Unauthenticated(t *testing.T) {
	repo := &mockNotificationPrefRepo{}
	h := GetNotificationPreferencesHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/users/me/notification-preferences", nil)
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestGetNotificationPreferences_RepoError(t *testing.T) {
	repo := &mockNotificationPrefRepo{err: domain.ErrInternalServer}
	h := GetNotificationPreferencesHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/users/me/notification-preferences", nil)
	req = withUserContext(req, "user-123")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUpdateNotificationPreferences_Success(t *testing.T) {
	repo := &mockNotificationPrefRepo{}
	h := UpdateNotificationPreferencesHandler(repo)

	body := `{"comment":false,"like":true,"email_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/users/me/notification-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user-123")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// Verify upserted value
	if repo.prefs == nil {
		t.Fatal("expected prefs to be stored")
	}
	if repo.prefs.Comment {
		t.Error("expected comment=false after update")
	}
	if !repo.prefs.Like {
		t.Error("expected like=true after update")
	}
	if repo.prefs.EmailEnabled {
		t.Error("expected email_enabled=false after update")
	}
}

func TestUpdateNotificationPreferences_Unauthenticated(t *testing.T) {
	repo := &mockNotificationPrefRepo{}
	h := UpdateNotificationPreferencesHandler(repo)

	req := httptest.NewRequest(http.MethodPut, "/users/me/notification-preferences", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUpdateNotificationPreferences_InvalidBody(t *testing.T) {
	repo := &mockNotificationPrefRepo{}
	h := UpdateNotificationPreferencesHandler(repo)

	req := httptest.NewRequest(http.MethodPut, "/users/me/notification-preferences", strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user-123")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func adminUsers(t *testing.T, srv *Server, query, token string) adminUserListResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users"+query, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin users list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body adminUserListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func userIDByName(t *testing.T, users []adminUserView, name string) string {
	t.Helper()
	for _, u := range users {
		if u.Username == name {
			return u.ID
		}
	}
	t.Fatalf("user %q not in list", name)
	return ""
}

func TestAdminUserManagement(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// A non-admin cannot list users.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list = %d, want 403", rec.Code)
	}

	all := adminUsers(t, srv, "", adminTok)
	if len(all.Users) != 2 {
		t.Fatalf("users = %d, want 2 (ada, bob)", len(all.Users))
	}
	adaID := userIDByName(t, all.Users, "ada")
	bobID := userIDByName(t, all.Users, "bob")

	// Search filter.
	if only := adminUsers(t, srv, "?q=bob", adminTok); len(only.Users) != 1 || only.Users[0].Username != "bob" {
		t.Errorf("search bob = %+v, want [bob]", only.Users)
	}

	// Promote bob to moderator.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"role":"moderator"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote = %d; body=%s", rec.Code, rec.Body.String())
	}
	var updated adminUserView
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Role != "moderator" {
		t.Errorf("role after promote = %q, want moderator", updated.Role)
	}

	// Deactivate bob.
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"is_active":false}`, adminTok)
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || updated.IsActive {
		t.Errorf("deactivate = %d active=%v, want 200/false", rec.Code, updated.IsActive)
	}

	// The admin cannot demote or deactivate themselves.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+adaID, `{"role":"user"}`, adminTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-demote = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+adaID, `{"is_active":false}`, adminTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-deactivate = %d, want 422", rec.Code)
	}

	// Validation + not-found.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{}`, adminTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty body = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"role":"superuser"}`, adminTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad role = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), `{"role":"moderator"}`, adminTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user = %d, want 404", rec.Code)
	}
}

func TestAdminUsersRequireAuth(t *testing.T) {
	srv := videoServer(t)
	someID := uuid.New().String()
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/admin/users", ""},
		{http.MethodPatch, "/api/v1/admin/users/" + someID, `{"role":"moderator"}`},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
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

// TestAdminSetsEmailVerified proves the §10 admin edit: email_verified flips on
// and off via PATCH /admin/users/{id}, the change survives a fresh read (the
// target's /auth/me), other fields are untouched, and each edit leaves an audit
// event naming the flag.
func TestAdminSetsEmailVerified(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Fresh accounts start unverified.
	users := adminUsers(t, srv, "?q=bob", adminTok)
	if len(users.Users) != 1 || users.Users[0].EmailVerified {
		t.Fatalf("fresh bob = %+v, want unverified", users.Users)
	}

	// Flip on: the response and the target's own /auth/me agree.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"email_verified":true}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("set email_verified = %d; body=%s", rec.Code, rec.Body.String())
	}
	var updated adminUserView
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if !updated.EmailVerified {
		t.Error("email_verified not true in the PATCH response")
	}
	if updated.Role != "user" || !updated.IsActive {
		t.Errorf("unrelated fields changed: %+v", updated)
	}
	var me userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", bobTok).Body.Bytes(), &me)
	if !me.EmailVerified {
		t.Error("target /auth/me does not reflect admin-set email_verified=true")
	}

	// Flip off (revoke the confirmation).
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"email_verified":false}`, adminTok)
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || updated.EmailVerified {
		t.Errorf("revoke = %d verified=%v, want 200/false", rec.Code, updated.EmailVerified)
	}

	// A non-admin cannot set it.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"email_verified":true}`, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin set = %d, want 403", rec.Code)
	}

	// Both edits are audited with the flag value in the reason.
	events := auditEvents(t, &buf)
	var sawOn, sawOff bool
	for _, e := range events {
		if e["action"] != observability.ActionAdminUserUpdate || e["result"] != observability.ResultSuccess {
			continue
		}
		reason, _ := e["reason"].(string)
		if strings.Contains(reason, "email_verified=true") {
			sawOn = true
		}
		if strings.Contains(reason, "email_verified=false") {
			sawOff = true
		}
	}
	if !sawOn || !sawOff {
		t.Errorf("audit events missing email_verified changes (on=%v off=%v)", sawOn, sawOff)
	}
}

// The total must describe the SAME set as the page it labels: filtered when the
// request is filtered, and unchanged by page size. Before this the endpoint
// returned no total at all, so the admin UI hardcoded a 100-row page and an
// instance with thousands of accounts had no way to reach the rest.
func TestAdminUserListTotalTracksFilterAndPaging(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	registerAndToken(t, srv, `{"username":"bobby","email":"bobby@example.test","password":"supersecret"}`)

	all := adminUsers(t, srv, "", adminTok)
	if all.Total != 3 || len(all.Users) != 3 {
		t.Fatalf("unfiltered = %d users / total %d, want 3/3", len(all.Users), all.Total)
	}

	// A filtered page reports the size of its own result set.
	filtered := adminUsers(t, srv, "?q=bob", adminTok)
	if filtered.Total != 2 {
		t.Errorf("total for q=bob is %d, want 2 (bob, bobby)", filtered.Total)
	}

	// Paging shrinks the page, never the total — that is what makes the page
	// count derivable by a client.
	paged := adminUsers(t, srv, "?limit=1", adminTok)
	if len(paged.Users) != 1 || paged.Limit != 1 {
		t.Fatalf("limit=1 returned %d users, limit echoed %d", len(paged.Users), paged.Limit)
	}
	if paged.Total != 3 {
		t.Errorf("total under limit=1 is %d, want 3", paged.Total)
	}
}

// TestRoleChangeReachesTheExistingSession pins ADM-01's effect-timing rule: a
// role change must bite on the token the demoted account is ALREADY holding,
// not only after it signs in again. Every authenticated request already
// re-reads the account (AUTH-05 slice (c) added the sessions⋈users lookup that
// makes deactivation and deletion immediate), so trusting the JWT's COPY of the
// role was the last piece of stale principal state left: a demoted moderator
// kept every staff route for the rest of the access token's life, and a
// promoted one could not use them until the token turned over.
func TestRoleChangeReachesTheexistingSession(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	bobID := userIDByName(t, adminUsers(t, srv, "?q=bob", adminTok).Users, "bob")

	// Baseline: an ordinary user reaches neither the admin nor the staff route.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", bobTok); rec.Code != http.StatusForbidden {
		t.Fatalf("user on /admin/users = %d, want 403", rec.Code)
	}

	// PROMOTION reaches the token bob is already holding.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"role":"admin"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("promote = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", bobTok); rec.Code != http.StatusOK {
		t.Errorf("promoted admin on the SAME token = %d, want 200 (role still read from the JWT?)", rec.Code)
	}

	// A token minted while bob WAS an admin must stop working the moment the
	// role is taken away — this is the direction that matters for security.
	var fresh authResponse
	_ = json.Unmarshal(postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`).Body.Bytes(), &fresh)
	if fresh.Token == "" {
		t.Fatal("bob could not sign in after promotion")
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", fresh.Token); rec.Code != http.StatusOK {
		t.Fatalf("admin on a fresh token = %d, want 200", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"role":"user"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("demote = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", fresh.Token); rec.Code != http.StatusForbidden {
		t.Errorf("demoted admin on its own live token = %d, want 403", rec.Code)
	}
	// Demotion removes STAFF access, not the account: ordinary routes keep
	// working on the very same token.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", fresh.Token); rec.Code != http.StatusOK {
		t.Errorf("demoted user on /auth/me = %d, want 200 — demotion must not sign anyone out", rec.Code)
	}
}

// TestModeratorDemotionReachesTheModerationRoutes is the same rule on the
// moderator tier, which is the one an instance actually demotes: a moderator
// route must refuse the demoted principal's live token.
func TestModeratorDemotionReachesTheModerationRoutes(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	registerAndToken(t, srv, `{"username":"mod","email":"mod@example.test","password":"supersecret"}`)
	modID := userIDByName(t, adminUsers(t, srv, "?q=mod", adminTok).Users, "mod")

	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+modID, `{"role":"moderator"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("promote to moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	var fresh authResponse
	_ = json.Unmarshal(postTo(srv, "/api/v1/auth/login", `{"email":"mod@example.test","password":"supersecret"}`).Body.Bytes(), &fresh)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/videos", "", fresh.Token); rec.Code != http.StatusOK {
		t.Fatalf("moderator on /admin/videos = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// A moderator never had the admin tier.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/users", "", fresh.Token); rec.Code != http.StatusForbidden {
		t.Errorf("moderator on /admin/users = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+modID, `{"role":"user"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("demote = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/videos", "", fresh.Token); rec.Code != http.StatusForbidden {
		t.Errorf("demoted moderator on /admin/videos with its live token = %d, want 403", rec.Code)
	}
}

// TestReactivatingATombstonedAccountIsRefused pins the A12 finding: the admin
// list still shows a hard-deleted account (as `deleted-<suffix>`) with a
// Reactivate action, and flipping is_active back to true made a public profile
// page answer 200 again for a person who had asked to be erased. A tombstone is
// irreversible by design — the username, address, display name and bio are gone
// and no re-activation can bring them back — so the write is refused rather
// than half-honoured. Deletion stays idempotent and the row stays readable.
func TestReactivatingATombstonedAccountIsRefused(t *testing.T) {
	// The account env is the fixture that wires BOTH the account service (whose
	// hard delete writes the tombstone) and the admin service reading the same
	// user map, so the tombstone is observable through /admin/users.
	env := newAccountEnv(t)
	srv := env.srv
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/users/"+bobID, "", adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The tombstone is visible to the admin, and says so.
	list := adminUsers(t, srv, "", adminTok)
	var tomb *adminUserView
	for i := range list.Users {
		if list.Users[i].ID == bobID {
			tomb = &list.Users[i]
		}
	}
	if tomb == nil {
		t.Fatal("the deleted account left no row in the admin list")
	}
	if tomb.DeletedAt == nil {
		t.Error("admin user view does not carry deleted_at, so the UI cannot tell a tombstone from a deactivation")
	}
	if tomb.IsActive {
		t.Error("tombstoned account is still is_active")
	}

	// The refusal itself.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"is_active":true}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reactivate a tombstone = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if after := adminUsers(t, srv, "", adminTok); func() bool {
		for _, u := range after.Users {
			if u.ID == bobID {
				return u.IsActive
			}
		}
		return false
	}() {
		t.Error("the refused PATCH still flipped is_active")
	}

	// The refusal is audited as a failure, like the self-change guard beside it.
	var sawFailure bool
	for _, e := range auditEvents(t, &buf) {
		if e["action"] == observability.ActionAdminUserUpdate && e["result"] == observability.ResultFailure {
			if reason, _ := e["reason"].(string); strings.Contains(reason, "deleted") {
				sawFailure = true
			}
		}
	}
	if !sawFailure {
		t.Error("no admin.user.update failure audit event for the refused reactivation")
	}
}

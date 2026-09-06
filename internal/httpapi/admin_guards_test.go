package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// claimOwnerIn redeems a freshly minted setup token on env's server, so the
// account it creates carries the 0131 owner marker exactly as a real first run
// would. Returns the owner's access token.
func claimOwnerIn(t *testing.T, env *accountEnv, username string) string {
	t.Helper()
	raw, minted, _, err := env.srv.authsvc.EnsureOwnerClaimToken(context.Background())
	if err != nil || !minted {
		t.Fatalf("EnsureOwnerClaimToken: minted=%v err=%v", minted, err)
	}
	rec := postTo(env.srv, "/api/v1/setup/claim-owner",
		`{"token":"`+raw+`","username":"`+username+`","email":"`+username+`@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim-owner = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.AccessToken == "" {
		t.Fatalf("claim-owner returned no access token; body=%s", rec.Body.String())
	}
	return body.AccessToken
}

// loginToken signs in and returns the access token, so a test can hold a
// session that was minted AFTER a role change rather than before it.
func loginToken(t *testing.T, srv *Server, email, password string) string {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/login", `{"email":"`+email+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s = %d; body=%s", email, rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.AccessToken == "" {
		t.Fatalf("login %s returned no access token", email)
	}
	return body.AccessToken
}

// TestAdminCannotRemoveTheInstanceOwner is A16 slice 1's finding as an HTTP
// test: "admin A demoted the instance owner M to user and got 200".
func TestAdminCannotRemoveTheInstanceOwner(t *testing.T) {
	env := newAccountEnv(t)
	ownerTok := claimOwnerIn(t, env, "mona")

	// A second admin, promoted by the owner the way an instance grows staff.
	registerAndToken(t, env.srv, `{"username":"avery","email":"avery@example.test","password":"supersecret"}`)
	list := adminUsers(t, env.srv, "", ownerTok)
	ownerID := userIDByName(t, list.Users, "mona")
	averyID := userIDByName(t, list.Users, "avery")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+averyID, `{"role":"admin"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote avery = %d; body=%s", rec.Code, rec.Body.String())
	}
	averyTok := loginToken(t, env.srv, "avery@example.test", "supersecret")

	// The list marks the owner, and marks nobody else.
	list = adminUsers(t, env.srv, "", ownerTok)
	for _, u := range list.Users {
		if want := u.Username == "mona"; u.IsOwner != want {
			t.Errorf("is_owner for %s = %v, want %v", u.Username, u.IsOwner, want)
		}
	}

	// Every removal the other admin can attempt is refused with a stable code.
	for _, tc := range []struct{ name, body string }{
		{"demote to user", `{"role":"user"}`},
		{"demote to moderator", `{"role":"moderator"}`},
		{"deactivate", `{"is_active":false}`},
	} {
		rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+ownerID, tc.body, averyTok)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s of the owner = %d, want 422; body=%s", tc.name, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "owner_protected" {
			t.Errorf("%s code = %q, want owner_protected", tc.name, code)
		}
	}
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+ownerID, averyTok, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete of the owner = %d, want 422; body=%s", rec.Code, rec.Body.String())
	} else if code := errorCode(t, rec); code != "owner_protected" {
		t.Errorf("delete of the owner code = %q, want owner_protected", code)
	}

	// Nothing moved.
	after := adminUsers(t, env.srv, "?q=mona", ownerTok)
	if len(after.Users) != 1 || after.Users[0].Role != "admin" || !after.Users[0].IsActive || after.Users[0].DeletedAt != nil {
		t.Errorf("owner after the refusals = %+v, want admin/active/live", after.Users)
	}

	// Each refusal is audited as a failure, not silently swallowed.
	events := auditEvents(t, env.logs)
	if ev := findAudit(events, observability.ActionAdminUserUpdate, observability.ResultFailure); ev == nil {
		t.Error("expected an admin.user.update failure audit event for the owner refusal")
	}
	if ev := findAudit(events, observability.ActionAdminUserDelete, observability.ResultFailure); ev == nil {
		t.Error("expected an admin.user.delete failure audit event for the owner refusal")
	}

	// The owner's own self-guards are untouched: still 422, still the self message.
	rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+ownerID, `{"role":"user"}`, ownerTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("owner self-demote = %d, want 422", rec.Code)
	}
	if code := errorCode(t, rec); code == "owner_protected" {
		t.Error("owner self-demote answered owner_protected; the self guard's own message must not move")
	}

	// And the owner can still be edited in ways that lock nobody out.
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+ownerID, `{"storage_quota_bytes":4096}`, averyTok); rec.Code != http.StatusOK {
		t.Errorf("quota on the owner by another admin = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestLastAdminCannotStandDown covers the reachable half of the last-admin
// guard: the sole admin's OWN account routes, where the admin routes' self
// guard does not apply and an instance really can reach zero admins.
func TestLastAdminCannotStandDown(t *testing.T) {
	env := newAccountEnv(t)
	ownerTok := claimOwnerIn(t, env, "mona")

	// Self-deactivation and self-deletion are both refused while mona is alone.
	rec := doJSON(env.srv, http.MethodPost, "/api/v1/auth/me/deactivate", ownerTok, `{"password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("sole admin self-deactivate = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "last_admin" {
		t.Errorf("self-deactivate code = %q, want last_admin", code)
	}
	rec = doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", ownerTok, `{"password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("sole admin self-delete = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "last_admin" {
		t.Errorf("self-delete code = %q, want last_admin", code)
	}
	// Still signed in, still an admin: the refusal changed nothing.
	if rec := doJSON(env.srv, http.MethodGet, "/api/v1/auth/me", ownerTok, ""); rec.Code != http.StatusOK {
		t.Errorf("/auth/me after the refusals = %d, want 200", rec.Code)
	}

	// A plain user is never the last admin, so their own account still closes.
	userTok := registerAndToken(t, env.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", userTok, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Errorf("plain user self-delete = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// With a second live admin the guard releases — and the owner marker is the
	// only thing still standing between mona and the exit.
	registerAndToken(t, env.srv, `{"username":"avery","email":"avery@example.test","password":"supersecret"}`)
	list := adminUsers(t, env.srv, "", ownerTok)
	averyID := userIDByName(t, list.Users, "avery")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+averyID, `{"role":"admin"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote avery = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/auth/me/deactivate", ownerTok, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Errorf("self-deactivate with a second admin = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminWritesToATombstoneAreRefused pins SC4: every field of a hard-deleted
// account is refused, with the reactivation refusal's own 422 shape.
func TestAdminWritesToATombstoneAreRefused(t *testing.T) {
	env := newAccountEnv(t)
	ownerTok := claimOwnerIn(t, env, "mona")
	registerAndToken(t, env.srv, `{"username":"ghost","email":"ghost@example.test","password":"supersecret"}`)
	list := adminUsers(t, env.srv, "", ownerTok)
	ghostID := userIDByName(t, list.Users, "ghost")
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+ghostID, ownerTok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete ghost = %d; body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct{ name, body string }{
		{"reactivate", `{"is_active":true}`},
		{"deactivate again", `{"is_active":false}`},
		{"role", `{"role":"moderator"}`},
		{"quota", `{"storage_quota_bytes":99}`},
		{"email_verified", `{"email_verified":true}`},
		{"bypass_quarantine", `{"bypass_quarantine":true}`},
	} {
		rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+ghostID, tc.body, ownerTok)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s on a tombstone = %d, want 422; body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
	// Deleting an already-deleted row keeps its shipped 404.
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+ghostID, ownerTok, ""); rec.Code != http.StatusNotFound {
		t.Errorf("re-delete of a tombstone = %d, want 404", rec.Code)
	}
}

// TestAdminUserUpdateAuditsStructuredChanges pins SC6: the ledger carries the
// envelope's `changes` array, so a consumer no longer has to parse prose to
// learn what an admin edited.
func TestAdminUserUpdateAuditsStructuredChanges(t *testing.T) {
	ledger := &httpAuditFakeRepo{}
	env := newAccountEnv(t, WithAuditLog(audit.NewService(ledger)))
	ownerTok := claimOwnerIn(t, env, "mona")
	registerAndToken(t, env.srv, `{"username":"avery","email":"avery@example.test","password":"supersecret"}`)
	list := adminUsers(t, env.srv, "", ownerTok)
	averyID := userIDByName(t, list.Users, "avery")

	rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+averyID,
		`{"role":"moderator","email_verified":true,"bypass_quarantine":true,"storage_quota_bytes":2048}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d; body=%s", rec.Code, rec.Body.String())
	}

	var row *sqlcgen.ListAuditLogRow
	for i := range ledger.rows {
		if ledger.rows[i].Action == observability.ActionAdminUserUpdate && ledger.rows[i].Result == observability.ResultSuccess {
			row = &ledger.rows[i]
		}
	}
	if row == nil {
		t.Fatal("no admin.user.update success row in the durable ledger")
	}
	if row.ResourceType != "user" || row.ResourceID != averyID {
		t.Errorf("resource = %q/%q, want user/%s", row.ResourceType, row.ResourceID, averyID)
	}
	var changes []audit.Change
	if err := json.Unmarshal(row.Changes, &changes); err != nil {
		t.Fatalf("decode changes %s: %v", row.Changes, err)
	}
	if len(changes) == 0 {
		t.Fatalf("changes = %s, want a non-empty structured field list", row.Changes)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Field] = c.Before + "→" + c.After
	}
	for field, want := range map[string]string{
		"role":              "user→moderator",
		"email_verified":    "false→true",
		"bypass_quarantine": "false→true",
	} {
		if got[field] != want {
			t.Errorf("changes[%s] = %q, want %q", field, got[field], want)
		}
	}
	if _, ok := got["storage_quota_bytes"]; !ok {
		t.Errorf("changes = %v, want a storage_quota_bytes entry", got)
	}
}

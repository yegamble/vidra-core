package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ownerOf returns the username the admin list badges as this instance's owner,
// or "" when nobody is marked. The marker is read back from the list rather than
// from the transfer's own response, so a green test cannot come from a handler
// that reports a swap it never performed.
func ownerOf(t *testing.T, env *accountEnv, token string) string {
	t.Helper()
	list := adminUsers(t, env.srv, "", token)
	owner := ""
	for _, u := range list.Users {
		if !u.IsOwner {
			continue
		}
		if owner != "" {
			t.Fatalf("two accounts are marked owner (%s and %s) — users_single_owner_idx exists to make that impossible", owner, u.Username)
		}
		owner = u.Username
	}
	return owner
}

func roleOf(t *testing.T, env *accountEnv, token, username string) string {
	t.Helper()
	for _, u := range adminUsers(t, env.srv, "", token).Users {
		if u.Username == username {
			return u.Role
		}
	}
	t.Fatalf("no account named %s", username)
	return ""
}

// TestOwnershipTransfer is the ruling's main claim: the instance owner can hand
// the marker to another administrator, and nobody else can move it.
//
// Before this route existed `users.is_owner` had exactly one writer — the
// first-run claim, which can never run again on a populated instance — so the
// marker was permanent. An owner who left either kept an account they no longer
// wanted or took the instance's ownership with them, leaving every administrator
// equal and the guards protecting nobody.
func TestOwnershipTransfer(t *testing.T) {
	ledger := &httpAuditFakeRepo{}
	env := newAccountEnv(t, WithAuditLog(audit.NewService(ledger)))
	ownerTok := claimOwnerIn(t, env, "mona")
	averyTok := registerAndToken(t, env.srv, `{"username":"avery","email":"avery@example.test","password":"supersecret"}`)
	registerAndToken(t, env.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	list := adminUsers(t, env.srv, "", ownerTok)
	averyID, bobID := userIDByName(t, list.Users, "avery"), userIDByName(t, list.Users, "bob")
	monaID := userIDByName(t, list.Users, "mona")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+averyID, `{"role":"admin"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote avery = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := ownerOf(t, env, ownerTok); got != "mona" {
		t.Fatalf("owner before the transfer = %q, want mona", got)
	}

	// The wrong actors first, each with a readback proving nothing moved.
	for _, tc := range []struct {
		name, token, body string
		want              int
		code              string
	}{
		{"another admin", averyTok, `{"user_id":"` + bobID + `","password":"supersecret"}`, http.StatusForbidden, "owner_only"},
		{"an ordinary user", registerAndToken(t, env.srv, `{"username":"uma","email":"uma@example.test","password":"supersecret"}`), `{"user_id":"` + averyID + `","password":"supersecret"}`, http.StatusForbidden, ""},
		{"anonymous", "", `{"user_id":"` + averyID + `","password":"supersecret"}`, http.StatusUnauthorized, ""},
	} {
		rec := sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer", tc.body, tc.token)
		if rec.Code != tc.want {
			t.Errorf("%s transfer = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
		if tc.code != "" {
			if got := errorCode(t, rec); got != tc.code {
				t.Errorf("%s transfer code = %q, want %q", tc.name, got, tc.code)
			}
		}
		if got := ownerOf(t, env, ownerTok); got != "mona" {
			t.Fatalf("owner after a refused %s transfer = %q, want mona", tc.name, got)
		}
	}

	// The owner with the WRONG password is refused too — a stolen access token
	// alone must not be able to give the instance away.
	rec := sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+averyID+`","password":"not-the-password"}`, ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("owner transfer with a wrong password = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := ownerOf(t, env, ownerTok); got != "mona" {
		t.Fatalf("owner after a wrong-password transfer = %q, want mona", got)
	}

	// Ineligible targets: a non-admin, an unknown id, and the caller themselves.
	for _, tc := range []struct {
		name, body string
		want       int
		code       string
	}{
		{"a non-admin", `{"user_id":"` + bobID + `","password":"supersecret"}`, http.StatusUnprocessableEntity, "owner_target_invalid"},
		{"the caller", `{"user_id":"` + monaID + `","password":"supersecret"}`, http.StatusUnprocessableEntity, "owner_target_invalid"},
		{"an unknown account", `{"user_id":"` + uuid.NewString() + `","password":"supersecret"}`, http.StatusNotFound, ""},
		{"a malformed id", `{"user_id":"not-a-uuid","password":"supersecret"}`, http.StatusNotFound, ""},
	} {
		rec := sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer", tc.body, ownerTok)
		if rec.Code != tc.want {
			t.Errorf("transfer to %s = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
		if tc.code != "" {
			if got := errorCode(t, rec); got != tc.code {
				t.Errorf("transfer to %s code = %q, want %q", tc.name, got, tc.code)
			}
		}
		if got := ownerOf(t, env, ownerTok); got != "mona" {
			t.Fatalf("owner after a refused transfer to %s = %q, want mona", tc.name, got)
		}
	}

	// A deactivated admin is ineligible: the marker must sit on an account that
	// can sign in, or the transfer manufactures the unowned instance it exists
	// to prevent.
	registerAndToken(t, env.srv, `{"username":"pat","email":"pat@example.test","password":"supersecret"}`)
	patID := userIDByName(t, adminUsers(t, env.srv, "", ownerTok).Users, "pat")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+patID, `{"role":"admin","is_active":false}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote+disable pat = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+patID+`","password":"supersecret"}`, ownerTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "owner_target_invalid" {
		t.Errorf("transfer to a deactivated admin = %d/%s, want 422/owner_target_invalid; body=%s", rec.Code, errorCode(t, rec), rec.Body.String())
	}

	// The real thing.
	rec = sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+averyID+`","password":"supersecret"}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := ownerOf(t, env, ownerTok); got != "avery" {
		t.Fatalf("owner after the transfer = %q, want avery", got)
	}
	// The former owner keeps their ADMIN role: this moves the marker, not the
	// role, and demoting them would be a second decision nobody asked for.
	if got := roleOf(t, env, ownerTok, "mona"); got != "admin" {
		t.Errorf("mona's role after handing ownership over = %q, want admin", got)
	}
	// Audited structurally, in the envelope's own vocabulary. The durable row is
	// the proof: the envelope validates against a closed key allowlist and
	// DELETES an invalid event rather than storing a partial one, so a row that
	// is here at all is a row the allowlist accepted.
	var row *sqlcgen.ListAuditLogRow
	for i := range ledger.rows {
		if ledger.rows[i].Action == observability.ActionOwnerTransfer && ledger.rows[i].Result == observability.ResultSuccess {
			row = &ledger.rows[i]
		}
	}
	if row == nil {
		t.Fatal("no admin.owner.transfer success row in the durable ledger")
	}
	if row.ResourceType != "user" || row.ResourceID != averyID {
		t.Errorf("resource = %q/%q, want user/%s (the account that GAINED the marker)", row.ResourceType, row.ResourceID, averyID)
	}
	var changes []audit.Change
	if err := json.Unmarshal(row.Changes, &changes); err != nil {
		t.Fatalf("decode changes %s: %v", row.Changes, err)
	}
	if len(changes) != 1 || changes[0].Field != "is_owner" || changes[0].Before != "false" || changes[0].After != "true" {
		t.Errorf("changes = %s, want one is_owner false→true entry", row.Changes)
	}
	if !strings.Contains(string(row.Metadata), `"count"`) {
		t.Errorf("metadata = %s, want the count of markers cleared", row.Metadata)
	}
	if strings.Contains(env.logs.String(), "supersecret") {
		t.Error("the confirming password reached the log stream")
	}
	if strings.Contains(string(row.Changes)+string(row.Metadata)+row.Reason, "supersecret") {
		t.Error("the confirming password reached the audit ledger")
	}

	// The marker really moved: mona can no longer transfer, avery can.
	rec = sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+monaID+`","password":"supersecret"}`, ownerTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "owner_only" {
		t.Errorf("the former owner transferring back = %d/%s, want 403/owner_only", rec.Code, errorCode(t, rec))
	}
	if rec := sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+monaID+`","password":"supersecret"}`, averyTok); rec.Code != http.StatusOK {
		t.Fatalf("the new owner transferring back = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := ownerOf(t, env, ownerTok); got != "mona" {
		t.Fatalf("owner after transferring back = %q, want mona", got)
	}
}

// TestOwnerCannotCloseTheirAccountUntilTransferred is the other half of the
// ruling. The marker has one slot and only its holder can move it, so an owner
// who deactivates or deletes themselves leaves the instance permanently unowned
// — the state `vidra doctor` can only report and an operator can only repair
// with a hand-written UPDATE. Both self-closing routes now refuse, and both open
// the moment ownership has moved.
func TestOwnerCannotCloseTheirAccountUntilTransferred(t *testing.T) {
	env := newAccountEnv(t)
	ownerTok := claimOwnerIn(t, env, "mona")
	registerAndToken(t, env.srv, `{"username":"avery","email":"avery@example.test","password":"supersecret"}`)
	averyID := userIDByName(t, adminUsers(t, env.srv, "", ownerTok).Users, "avery")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+averyID, `{"role":"admin"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote avery = %d; body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct{ name, method, path string }{
		{"deactivate", http.MethodPost, "/api/v1/auth/me/deactivate"},
		{"delete", http.MethodDelete, "/api/v1/auth/me"},
	} {
		rec := doJSON(env.srv, tc.method, tc.path, ownerTok, `{"password":"supersecret"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("owner self-%s = %d, want 422; body=%s", tc.name, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "owner_must_transfer" {
			t.Errorf("owner self-%s code = %q, want owner_must_transfer", tc.name, code)
		}
	}
	// The refusal is not a sign-out and changed nothing.
	if rec := doJSON(env.srv, http.MethodGet, "/api/v1/auth/me", ownerTok, ""); rec.Code != http.StatusOK {
		t.Errorf("/auth/me after the refusals = %d, want 200", rec.Code)
	}
	if got := ownerOf(t, env, ownerTok); got != "mona" {
		t.Fatalf("owner after the refusals = %q, want mona", got)
	}

	// Hand it over, and the exit opens.
	if rec := sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/owner/transfer",
		`{"user_id":"`+averyID+`","password":"supersecret"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("transfer = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", ownerTok, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("former owner self-delete after transferring = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// An ordinary account was never gated by this at all.
	userTok := registerAndToken(t, env.srv, `{"username":"uma","email":"uma@example.test","password":"supersecret"}`)
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", userTok, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Errorf("plain user self-delete = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

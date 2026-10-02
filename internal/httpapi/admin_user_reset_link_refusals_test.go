package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// TestAdminPasswordResetLinkRefusals: every guard, one table. A refusal never
// carries a link and never leaves a token behind.
func TestAdminPasswordResetLinkRefusals(t *testing.T) {
	env, ownerTok, userID := resetLinkFixture(t, &httpAuditFakeRepo{})
	userTok := loginToken(t, env.srv, "uma@example.test", "supersecret")
	// otto is deactivated, avery promoted to admin, milo to moderator.
	for _, n := range []string{"otto", "avery", "milo"} {
		registerAndToken(t, env.srv, `{"username":"`+n+`","email":"`+n+`@example.test","password":"supersecret"}`)
	}
	id := map[string]string{}
	for _, u := range adminUsers(t, env.srv, "", ownerTok).Users {
		id[u.Username] = u.ID
	}
	for who, patch := range map[string]string{"otto": `{"is_active":false}`, "avery": `{"role":"admin"}`, "milo": `{"role":"moderator"}`} {
		if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+id[who], patch, ownerTok); rec.Code != http.StatusOK {
			t.Fatalf("setup %s = %d; body=%s", who, rec.Code, rec.Body.String())
		}
	}
	adminTok := loginToken(t, env.srv, "avery@example.test", "supersecret")
	modTok := loginToken(t, env.srv, "milo@example.test", "supersecret")
	for _, tc := range []struct {
		name, id, body, token string
		noBase                bool
		want                  int
		msg                   string
	}{
		{"anonymous", userID, pw, "", false, http.StatusUnauthorized, ""},
		{"ordinary caller", userID, pw, userTok, false, http.StatusForbidden, ""},
		{"moderator caller", userID, pw, modTok, false, http.StatusForbidden, ""},
		{"wrong password", userID, `{"password":"nope-nope-nope"}`, ownerTok, false, http.StatusForbidden, ""},
		{"no password", userID, `{}`, ownerTok, false, http.StatusUnprocessableEntity, ""},
		{"unknown target", "00000000-0000-4000-8000-000000000000", pw, ownerTok, false, http.StatusNotFound, ""},
		{"malformed id", "not-a-uuid", pw, ownerTok, false, http.StatusNotFound, ""},
		{"deactivated target", id["otto"], pw, ownerTok, false, http.StatusConflict, ""},
		{"owner target", id["mona"], pw, adminTok, false, http.StatusForbidden, "instance owner"},
		{"admin target", id["avery"], pw, ownerTok, false, http.StatusForbidden, "staff recover"},
		{"moderator target", id["milo"], pw, ownerTok, false, http.StatusForbidden, "staff recover"},
		{"no PUBLIC_BASE_URL", userID, pw, ownerTok, true, http.StatusConflict, ""},
	} {
		env.srv.cfg.PublicBaseURL = "https://vidra.example"
		if tc.noBase {
			env.srv.cfg.PublicBaseURL = ""
		}
		rec := mintResetLink(env, tc.id, tc.body, tc.token)
		if rec.Code != tc.want {
			t.Errorf("%s = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.msg) {
			t.Errorf("%s: body %q lacks %q", tc.name, rec.Body.String(), tc.msg)
		}
		if strings.Contains(rec.Body.String(), "reset_url") || len(env.authRepo.resets) != 0 {
			t.Errorf("%s: a refusal carried a link or left %d tokens", tc.name, len(env.authRepo.resets))
		}
	}
}

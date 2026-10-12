package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

type createUserResp struct {
	User      adminUserView `json:"user"`
	LinkSent  bool          `json:"link_emailed"`
	ResetURL  string        `json:"reset_url"`
	ExpiresAt string        `json:"expires_at"`
}

func createUser(env *accountEnv, body, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/users", body, token)
}

func decodeCreated(t *testing.T, rec *httptest.ResponseRecorder) (out createUserResp) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestAdminCreateUserReturnsRedeemableLinkAndAudits(t *testing.T) {
	ledger := &httpAuditFakeRepo{}
	env, ownerTok, _ := resetLinkFixture(t, ledger)
	rec := createUser(env, `{"username":"nina","email":"nina@example.test","role":"moderator","storage_quota_bytes":5000,"password":"supersecret"}`, ownerTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	out := decodeCreated(t, rec)
	if out.User.Username != "nina" || out.User.Role != "moderator" || !out.User.EmailVerified ||
		out.User.StorageQuotaBytes == nil || *out.User.StorageQuotaBytes != 5000 || out.LinkSent {
		t.Fatalf("created view = %+v", out)
	}
	token := tokenOf(t, out.ResetURL)

	ev := findAudit(auditEvents(t, env.logs), observability.ActionAdminUserCreate, observability.ResultSuccess)
	if ev == nil || ev["resource_id"] != out.User.ID || ev["resource_type"] != "user" {
		t.Fatalf("audit event = %v", ev)
	}
	dump, _ := json.Marshal(ledger.rows)
	for name, hay := range map[string]string{"logs": env.logs.String(), "audit rows": string(dump)} {
		for _, secret := range []string{token, "nina@example.test", "supersecret"} {
			if strings.Contains(hay, secret) {
				t.Errorf("%q leaked into the %s", secret, name)
			}
		}
	}

	// The link sets the first password; the account then signs in.
	if rec := postTo(env.srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":"brand-new-pass"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postTo(env.srv, "/api/v1/auth/login", `{"email":"nina@example.test","password":"brand-new-pass"}`); rec.Code != http.StatusOK {
		t.Errorf("login after set-password = %d", rec.Code)
	}
}

func TestAdminCreateUserEmailsLinkWhenMailConfigured(t *testing.T) {
	env, ownerTok, _ := resetLinkFixture(t, &httpAuditFakeRepo{}, WithContactMailer(auth.NewCaptureMailer()))
	rec := createUser(env, `{"username":"nina","email":"nina@example.test","role":"user","password":"supersecret"}`, ownerTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	out := decodeCreated(t, rec)
	if !out.LinkSent || out.ResetURL != "" {
		t.Errorf("emailed account must not also return the link: %+v", out)
	}
}

func TestAdminCreateUserRefusals(t *testing.T) {
	env, ownerTok, _ := resetLinkFixture(t, &httpAuditFakeRepo{})
	userTok := registerAndToken(t, env.srv, `{"username":"vic","email":"vic@example.test","password":"supersecret"}`)
	cases := []struct {
		name, body, tok string
		want            int
	}{
		{"duplicate username", `{"username":"uma","email":"new@example.test","role":"user","password":"supersecret"}`, ownerTok, http.StatusConflict},
		{"duplicate email", `{"username":"newname","email":"UMA@example.test","role":"user","password":"supersecret"}`, ownerTok, http.StatusConflict},
		{"bad role", `{"username":"newname","email":"n@example.test","role":"owner","password":"supersecret"}`, ownerTok, http.StatusUnprocessableEntity},
		{"bad email", `{"username":"newname","email":"nope","role":"user","password":"supersecret"}`, ownerTok, http.StatusUnprocessableEntity},
		{"bad username", `{"username":"a b","email":"n@example.test","role":"user","password":"supersecret"}`, ownerTok, http.StatusUnprocessableEntity},
		{"negative quota", `{"username":"newname","email":"n@example.test","role":"user","storage_quota_bytes":-1,"password":"supersecret"}`, ownerTok, http.StatusUnprocessableEntity},
		{"no step-up password", `{"username":"newname","email":"n@example.test","role":"user"}`, ownerTok, http.StatusUnprocessableEntity},
		{"wrong step-up password", `{"username":"newname","email":"n@example.test","role":"user","password":"wrongwrong"}`, ownerTok, http.StatusForbidden},
		{"non-admin", `{"username":"newname","email":"n@example.test","role":"user","password":"supersecret"}`, userTok, http.StatusForbidden},
		{"anonymous", `{"username":"newname","email":"n@example.test","role":"user","password":"supersecret"}`, "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		if rec := createUser(env, tc.body, tc.tok); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	if n := len(adminUsers(t, env.srv, "", ownerTok).Users); n != 3 {
		t.Errorf("refusals created accounts: %d users, want 3", n)
	}
}

// leftAccount reports the (active?) state of an account named name, and how many
// active admins the instance has.
func leftAccount(t *testing.T, env *accountEnv, tok, name string) (found, active bool, activeAdmins int) {
	t.Helper()
	for _, u := range adminUsers(t, env.srv, "", tok).Users {
		if u.Role == "admin" && u.IsActive {
			activeAdmins++
		}
		if u.Username == name {
			found, active = true, u.IsActive
		}
	}
	return found, active, activeAdmins
}

// A dead ledger must refuse on EVERY delivery path (link returned or emailed),
// and must not leave a usable account behind, least of all a staff one.
func TestAdminCreateUserFailsClosedWithoutAudit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []Option
	}{
		{"link returned", nil},
		{"link emailed", []Option{WithContactMailer(auth.NewCaptureMailer())}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failing := &failingAuditRepo{}
			env, ownerTok, _ := resetLinkFixture(t, failing, tc.extra...)
			_, _, adminsBefore := leftAccount(t, env, ownerTok, "nina")
			failing.fail = true
			rec := createUser(env, `{"username":"nina","email":"nina@example.test","role":"admin","password":"supersecret"}`, ownerTok)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("create with a dead ledger = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "reset-password") {
				t.Error("a link was returned although the audit write failed")
			}
			failing.fail = false
			found, active, admins := leftAccount(t, env, ownerTok, "nina")
			if found && active {
				t.Error("the refused account is still active")
			}
			if admins != adminsBefore {
				t.Errorf("active admins = %d, want %d", admins, adminsBefore)
			}
			// It can never sign in: no password was ever set and it is inactive.
			if rec := postTo(env.srv, "/api/v1/auth/login", `{"email":"nina@example.test","password":"supersecret"}`); rec.Code == http.StatusOK {
				t.Error("the refused account can sign in")
			}
		})
	}
}

func TestAdminCreateUserRefusesModerators(t *testing.T) {
	env, ownerTok, userID := resetLinkFixture(t, &httpAuditFakeRepo{})
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+userID, `{"role":"moderator"}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("promote = %d; body=%s", rec.Code, rec.Body.String())
	}
	modTok := loginToken(t, env.srv, "uma@example.test", "supersecret")
	rec := createUser(env, `{"username":"nina","email":"nina@example.test","role":"user","password":"supersecret"}`, modTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("moderator create = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

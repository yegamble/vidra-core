package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
)

const pw = `{"password":"supersecret"}`

type resetLinkResp struct {
	ResetURL  string    `json:"reset_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// resetLinkFixture is the account harness with a durable audit ledger, a public
// origin, the owner (mona) and one ordinary user (uma).
func resetLinkFixture(t *testing.T, ledger audit.Repository, extra ...Option) (env *accountEnv, ownerTok, userID string) {
	t.Helper()
	env = newAccountEnv(t, append([]Option{WithAuditLog(audit.NewService(ledger))}, extra...)...)
	env.srv.cfg.PublicBaseURL = "https://vidra.example"
	ownerTok = claimOwnerIn(t, env, "mona")
	registerAndToken(t, env.srv, `{"username":"uma","email":"uma@example.test","password":"supersecret"}`)
	return env, ownerTok, userIDByName(t, adminUsers(t, env.srv, "", ownerTok).Users, "uma")
}

func mintResetLink(env *accountEnv, id, body, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(env.srv, http.MethodPost, "/api/v1/admin/users/"+id+"/password-reset-link", body, token)
}

func decodeResetLink(t *testing.T, rec *httptest.ResponseRecorder) (out resetLinkResp) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Query().Get("token") == "" {
		t.Fatalf("reset_url %q carries no token (%v)", link, err)
	}
	return u.Query().Get("token")
}

func TestAdminPasswordResetLinkMintsRedeemableLink(t *testing.T) {
	ledger := &httpAuditFakeRepo{}
	env, ownerTok, userID := resetLinkFixture(t, ledger)
	rec := mintResetLink(env, userID, pw, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	out := decodeResetLink(t, rec)
	if !strings.HasPrefix(out.ResetURL, "https://vidra.example/reset-password/confirm?token=") {
		t.Errorf("reset_url = %q", out.ResetURL)
	}
	if d := time.Until(out.ExpiresAt); d < 50*time.Minute || d > 61*time.Minute {
		t.Errorf("expires_at is %v away, want about 1h", d)
	}
	token := tokenOf(t, out.ResetURL)

	// Audited with the target named, before the link left the server.
	ev := findAudit(auditEvents(t, env.logs), observability.ActionAdminPasswordResetLink, observability.ResultSuccess)
	if ev == nil || ev["resource_id"] != userID || ev["resource_type"] != "user" || ev["actor_id"] != userIDByName(t, adminUsers(t, env.srv, "", ownerTok).Users, "mona") {
		t.Fatalf("audit event = %v", ev)
	}
	durable := 0
	for _, row := range ledger.rows {
		if row.Action == observability.ActionAdminPasswordResetLink {
			durable++
		}
	}
	if durable != 1 {
		t.Errorf("durable reset-link audit rows = %d, want 1", durable)
	}
	// The credential must not be recoverable from any log or audit row.
	dump, _ := json.Marshal(ledger.rows)
	for name, hay := range map[string]string{"logs": env.logs.String(), "audit rows": string(dump)} {
		if strings.Contains(hay, token) {
			t.Errorf("the reset token leaked into the %s", name)
		}
	}

	// Redeems once, sets the password, and is spent.
	if rec := postTo(env.srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":"brand-new-pass"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postTo(env.srv, "/api/v1/auth/login", `{"email":"uma@example.test","password":"brand-new-pass"}`); rec.Code != http.StatusOK {
		t.Errorf("login with the new password = %d", rec.Code)
	}
	if rec := postTo(env.srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":"another-pass-1"}`); rec.Code == http.StatusNoContent {
		t.Error("a redeemed link redeemed twice")
	}
}

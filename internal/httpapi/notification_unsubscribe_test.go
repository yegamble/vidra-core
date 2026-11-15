package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/unsubscribe"
)

const unsubscribeRoute = "/api/v1/notifications/unsubscribe"

func unsubscribeServer(t *testing.T) (*Server, string, uuid.UUID) {
	t.Helper()
	cfg := testConfig()
	cfg.JWTSecret = "unsubscribe-test-secret-0123456789abcdef"
	cfg.PublicBaseURL = "https://vidra.example"
	srv := videoServerCfg(t, cfg)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	id, err := uuid.Parse(reg.User.ID)
	if err != nil {
		t.Fatalf("user id %q: %v", reg.User.ID, err)
	}
	// Opt in to every email type, as a user who then receives the mail would.
	body := `{"email":{"comment":true,"comment_reply":true,"new_video":true}}`
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", body, reg.Token); rec.Code != http.StatusOK {
		t.Fatalf("opt in = %d; %s", rec.Code, rec.Body.String())
	}
	return srv, reg.Token, id
}

func emailPrefs(t *testing.T, srv *Server, token string) map[string]bool {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/notification-prefs", "", token)
	var body notificationPrefsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Email
}

func postUnsubscribe(srv *Server, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, unsubscribeRoute+"?token="+url.QueryEscape(token),
		strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func allOff(m map[string]bool) bool {
	for _, typ := range notification.EmailTypes() {
		if m[typ] {
			return false
		}
	}
	return true
}

// A valid token POST turns every email type off through the existing
// preference (the settings page then shows it off), repeats harmlessly, and
// signs nobody in.
func TestUnsubscribePostTurnsEmailOffAndIsIdempotent(t *testing.T) {
	srv, tok, id := unsubscribeServer(t)
	signer := unsubscribe.New([]byte(srv.cfg.JWTSecret))
	for i := 0; i < 2; i++ {
		rec := postUnsubscribe(srv, signer.Token(id))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("POST #%d = %d, want 204; body=%s", i+1, rec.Code, rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Authorization") != "" || rec.Body.Len() != 0 {
			t.Errorf("POST #%d set a cookie/credential or a body: %v %q", i+1, rec.Result().Cookies(), rec.Body.String())
		}
	}
	if got := emailPrefs(t, srv, tok); !allOff(got) {
		t.Errorf("email prefs after unsubscribe = %v, want all off", got)
	}
}

// A forged, truncated, wrong-key or empty token changes nothing and is
// indistinguishable on the wire from a good one — the endpoint must not become
// an oracle for which accounts exist or which tokens are valid.
func TestUnsubscribeForgedTokenChangesNothingAndLooksTheSame(t *testing.T) {
	srv, tok, id := unsubscribeServer(t)
	good := unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(id)
	other := unsubscribe.New([]byte("a-different-secret-a-different-secret"))
	for name, bad := range map[string]string{
		"empty":       "",
		"garbage":     "not-a-token",
		"wrong key":   other.Token(id),
		"unknown id":  unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(uuid.New()),
		"truncated":   good[:len(good)-3],
		"id only":     strings.SplitN(good, ".", 2)[0],
		"bad sig":     strings.SplitN(good, ".", 2)[0] + ".AAAA",
		"long string": strings.Repeat("a", 5000),
	} {
		rec := postUnsubscribe(srv, bad)
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q, want the same 204 as a good token", name, rec.Code, rec.Body.String())
		}
		if got := emailPrefs(t, srv, tok); allOff(got) {
			t.Fatalf("%s: a bad token changed the preferences: %v", name, got)
		}
	}
}

// Mail scanners and link previewers GET every URL in a message. A GET must
// never unsubscribe; it sends a person to the settings page instead.
func TestUnsubscribeGetDoesNotUnsubscribe(t *testing.T) {
	srv, tok, id := unsubscribeServer(t)
	token := unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(id)
	rec := sendJSONAuth(srv, http.MethodGet, unsubscribeRoute+"?token="+url.QueryEscape(token), "", "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://vidra.example/settings/notifications" {
		t.Fatalf("GET = %d Location=%q, want 302 to the settings page", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Header().Get("Location"), token) {
		t.Error("the redirect echoes the token")
	}
	if got := emailPrefs(t, srv, tok); allOff(got) {
		t.Errorf("GET unsubscribed the user: %v", got)
	}
}

// One user's token never touches another user's preferences.
func TestUnsubscribeIsScopedToTheTokensUser(t *testing.T) {
	srv, tok, _ := unsubscribeServer(t)
	bob := registerTokens(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	bobID, _ := uuid.Parse(bob.User.ID)
	if rec := postUnsubscribe(srv, unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(bobID)); rec.Code != http.StatusNoContent {
		t.Fatalf("POST = %d", rec.Code)
	}
	if got := emailPrefs(t, srv, tok); allOff(got) {
		t.Errorf("bob's token changed ada's preferences: %v", got)
	}
}

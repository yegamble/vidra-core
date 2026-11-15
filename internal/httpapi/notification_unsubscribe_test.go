package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	netmail "net/mail"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
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
	srv, tok, id, _ := unsubscribeServerWith(t, nil)
	return srv, tok, id
}

func unsubscribeServerWith(t *testing.T, httpOpts []Option, beforeOptIn ...func()) (*Server, string, uuid.UUID, *notifFakeRepo) {
	t.Helper()
	cfg := testConfig()
	cfg.JWTSecret = "unsubscribe-test-secret-0123456789abcdef"
	cfg.PublicBaseURL = "https://vidra.example"
	srv, _, _, notifRepo, _ := videoServerFullWith(t, cfg, httpOpts)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	id, err := uuid.Parse(reg.User.ID)
	if err != nil {
		t.Fatalf("user id %q: %v", reg.User.ID, err)
	}
	for _, f := range beforeOptIn {
		f() // e.g. clear rate-limit counters the registration spent
	}
	// Opt in to every email type, as a user who then receives the mail would.
	body := `{"email":{"comment":true,"comment_reply":true,"new_video":true}}`
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", body, reg.Token); rec.Code != http.StatusOK {
		t.Fatalf("opt in = %d; %s", rec.Code, rec.Body.String())
	}
	return srv, reg.Token, id, notifRepo
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

// gmailPost is the request shape a mailbox provider sends: a form POST with the
// fixed RFC 8058 body.
func gmailPost(srv *Server, rawURL string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// A burst of mailbox-provider POSTs must not be starved by the general per-IP
// budget (behind a CDN every caller shares the edge IP with all API traffic),
// and a repeat must do no database write.
func TestUnsubscribeHasItsOwnBudgetAndRepeatsDoNotWrite(t *testing.T) {
	fc := &fakeCounter{}
	srv, tok, id, repo := unsubscribeServerWith(t, []Option{
		WithRateLimiter(ratelimit.NewLimiter(fc, 1, time.Minute)),
		WithMediaRateLimiter(ratelimit.NewLimiter(fc, 100, time.Minute)),
	}, func() { fc.counts = nil }) // registration spent budget; opt-in and the test start clean
	fc.counts = nil
	// Exhaust the general budget: the second call is already refused.
	for i := 0; i < 3; i++ {
		sendJSONAuth(srv, http.MethodGet, "/api/v1/nodeinfo", "", "")
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/nodeinfo", "", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("general budget not exhausted: %d", rec.Code)
	}

	u := unsubscribe.New([]byte(srv.cfg.JWTSecret)).URL("https://vidra.example", UnsubscribeURLPath, id)
	path := strings.TrimPrefix(u, "https://vidra.example")
	rec := gmailPost(srv, path)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST with the general budget exhausted = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "100" {
		t.Errorf("X-RateLimit-Limit = %q, want the large budget 100", got)
	}
	if got := emailPrefs(t, srv, tok); !allOff(got) {
		t.Fatalf("prefs after POST = %v, want all off", got)
	}
	writes := repo.emailWrites
	if rec := gmailPost(srv, path); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat POST = %d", rec.Code)
	}
	if repo.emailWrites != writes {
		t.Errorf("repeat POST performed %d writes, want none", repo.emailWrites-writes)
	}
}

// A successful unsubscribe is audited (user id, source one_click, never the
// token); a repeat that changes nothing and a forged token are not.
func TestUnsubscribeIsAudited(t *testing.T) {
	var buf bytes.Buffer
	srv, _, id, _ := unsubscribeServerWith(t, []Option{WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))})
	token := unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(id)
	path := unsubscribeRoute + "?token=" + url.QueryEscape(token)

	buf.Reset()
	gmailPost(srv, path)
	gmailPost(srv, path)                                   // no change: no event
	gmailPost(srv, unsubscribeRoute+"?token=forged.token") // forged: no event
	var n int
	for _, e := range auditEvents(t, &buf) {
		if e["action"] != observability.ActionNotificationEmailUnsubscribe {
			continue
		}
		n++
		if e["result"] != observability.ResultSuccess {
			t.Errorf("result = %v", e["result"])
		}
		raw, _ := json.Marshal(e)
		if !strings.Contains(string(raw), id.String()) {
			// Metadata (source=one_click) is persisted with the row, not logged.
			t.Errorf("event lacks the user id: %s", raw)
		}
	}
	if n != 1 {
		t.Fatalf("got %d unsubscribe audit events, want exactly 1", n)
	}
	if strings.Contains(buf.String(), token) {
		t.Error("the token leaked into the logs")
	}
}

// A real storage failure is a 500 (the mailbox provider retries instead of
// believing the user is unsubscribed); a foreign-key violation — the user was
// deleted between the read and the write — is the one failure that is swallowed.
func TestUnsubscribeStorageErrors(t *testing.T) {
	srv, _, id, repo := unsubscribeServerWith(t, nil)
	path := unsubscribeRoute + "?token=" + url.QueryEscape(unsubscribe.New([]byte(srv.cfg.JWTSecret)).Token(id))

	repo.emailWriteErr = errors.New("connection refused")
	if rec := gmailPost(srv, path); rec.Code != http.StatusInternalServerError {
		t.Errorf("non-FK storage error = %d, want 500", rec.Code)
	}
	repo.emailWriteErr = &pgconn.PgError{Code: "23503"}
	if rec := gmailPost(srv, path); rec.Code != http.StatusNoContent {
		t.Errorf("FK violation = %d, want 204", rec.Code)
	}
	repo.emailWriteErr = &pgconn.PgError{Code: "40001"} // another PG error is NOT swallowed
	if rec := gmailPost(srv, path); rec.Code != http.StatusInternalServerError {
		t.Errorf("other PG error = %d, want 500", rec.Code)
	}
}

type captureTransport struct{ msgs []mail.Message }

func (c *captureTransport) Kind() string { return "capture" }
func (c *captureTransport) Send(_ context.Context, m mail.Message) error {
	c.msgs = append(c.msgs, m)
	return nil
}
func (c *captureTransport) Probe(context.Context) mail.ProbeResult {
	return mail.ProbeResult{Verified: true}
}

// The whole chain: the composer builds the header URL with the signer, the
// header URL is POSTed exactly as a mailbox provider would, and the server
// verifies it and turns the preference off.
func TestUnsubscribeHeaderURLRoundTrip(t *testing.T) {
	srv, tok, id := unsubscribeServer(t)
	signer := unsubscribe.New([]byte(srv.cfg.JWTSecret))
	tr := &captureTransport{}
	c := mail.NewComposer(mail.ComposerConfig{InstanceName: "Vidra Test", PublicBaseURL: "https://vidra.example"},
		mail.NewStaticResolver(tr, netmail.Address{Address: "no-reply@vidra.example"}, ""),
		mail.WithUnsubscribeURLFunc(func(userID string) string {
			uid, err := uuid.Parse(userID)
			if err != nil {
				return ""
			}
			return signer.URL("https://vidra.example", UnsubscribeURLPath, uid)
		}))
	if err := c.SendNotificationEmail(context.Background(), "ada@example.test", mail.NotificationEmail{
		Type: "comment", UserID: id.String(), ActorName: "bob", VideoID: "v", VideoTitle: "t"}); err != nil {
		t.Fatal(err)
	}
	header := tr.msgs[0].ListUnsubscribe
	u, err := url.Parse(header)
	if err != nil || u.Scheme != "https" || u.Path != UnsubscribeURLPath {
		t.Fatalf("header URL = %q (%v)", header, err)
	}
	if rec := gmailPost(srv, u.RequestURI()); rec.Code != http.StatusNoContent {
		t.Fatalf("POST of the header URL = %d", rec.Code)
	}
	if got := emailPrefs(t, srv, tok); !allOff(got) {
		t.Errorf("prefs after the round trip = %v, want all off", got)
	}
}

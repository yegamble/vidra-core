package httpapi

// The A05 trust rulings at the HTTP layer: connecting a provider from settings,
// the second factor on a provider sign-in, and a callback that can no longer
// switch accounts under a signed-in browser.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// linkStart runs POST /auth/oauth/fake/link/start for a bearer token and
// returns the recorder plus the sealed state cookie (nil when refused).
func (e *oauthEnv) linkStart(t *testing.T, bearer, returnTo string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	body := `{"return_to":` + jsonQuote(returnTo) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/fake/link/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	e.srv.Handler().ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == oauthStateCookieName && ck.Value != "" {
			return rec, ck
		}
	}
	return rec, nil
}

// jsonQuote JSON-quotes a string for the small request bodies below.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// linkCallback finishes a link attempt with the given id_token claims.
func (e *oauthEnv) linkCallback(t *testing.T, start *httptest.ResponseRecorder, cookie *http.Cookie, claims map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	var out linkStartResponse
	if err := json.Unmarshal(start.Body.Bytes(), &out); err != nil {
		t.Fatalf("link start body %q: %v", start.Body.String(), err)
	}
	loc, err := url.Parse(out.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.setNonce(loc.Query().Get("nonce"))
	e.fake.setClaims(claims)
	return e.callback(t, "?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")), cookie)
}

// linkFromSettings is the whole happy path, asserted, for tests that need a
// linked identity rather than the linking itself.
func (e *oauthEnv) linkFromSettings(t *testing.T, bearer, subject, email, returnTo string) {
	t.Helper()
	start, cookie := e.linkStart(t, bearer, returnTo)
	if start.Code != http.StatusOK || cookie == nil {
		t.Fatalf("link start = %d (body=%s)", start.Code, start.Body.String())
	}
	cb := e.linkCallback(t, start, cookie, map[string]any{
		"sub": subject, "email": email, "email_verified": true,
	})
	if cb.Code != http.StatusFound {
		t.Fatalf("link callback = %d (body=%s)", cb.Code, cb.Body.String())
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("link") != "fake" {
		t.Fatalf("link callback Location = %q, want ?link=fake", cb.Header().Get("Location"))
	}
}

// TestOAuthLinkFromSettings covers the contract the settings "Connect" control
// speaks: anonymous 401, already-linked 422, the happy path, and a subject that
// belongs to somebody else — which is refused rather than moved.
func TestOAuthLinkFromSettings(t *testing.T) {
	env := newOAuthEnv(t)

	// 401 without a session: the attempt binds to the caller, so there has to
	// be one.
	if rec, _ := env.linkStart(t, "", "/settings"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous link start = %d, want 401", rec.Code)
	}

	ada := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, env.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// The happy path. ada's provider address IS ada's address, verified, so the
	// link also settles email_verified — the fact A05 recorded being learned
	// and discarded.
	env.linkFromSettings(t, ada, "sub-ada", "ada@example.test", "/settings")
	if link := findAudit(auditEvents(t, env.buf), observability.ActionOAuthLink, observability.ResultSuccess); link == nil || link["reason"] != "oauth:fake" {
		t.Errorf("link audit = %v, want auth.oauth.link success oauth:fake", link)
	}
	me := meWithToken(t, env.srv, ada)
	if !me.EmailVerified {
		t.Error("a provider-verified link on the account's own address left email_verified false")
	}

	// 422: this account already has an identity for this provider, and the
	// refusal comes BEFORE the round trip — no state cookie is sealed.
	rec, cookie := env.linkStart(t, ada, "/settings")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second link start = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
	}
	if cookie != nil {
		t.Error("a refused link start must not seal an attempt cookie")
	}
	if code := errorCodeOf(t, rec); code != "provider_already_linked" {
		t.Errorf("code = %q, want provider_already_linked", code)
	}

	// 409-shaped refusal: bob completes a round trip as ada's provider account.
	// The identity does not move, and bob gets no session of ada's.
	start, cookie := env.linkStart(t, bob, "/settings")
	if start.Code != http.StatusOK || cookie == nil {
		t.Fatalf("bob link start = %d", start.Code)
	}
	cb := env.linkCallback(t, start, cookie, map[string]any{
		"sub": "sub-ada", "email": "ada@example.test", "email_verified": true,
	})
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("link_error") != "identity_belongs_to_another_account" {
		t.Fatalf("Location = %q, want ?link_error=identity_belongs_to_another_account", cb.Header().Get("Location"))
	}
	if sessionCookieFrom(cb) != nil {
		t.Error("a refused link must not mint a session")
	}
	ident, err := env.repo.GetOAuthIdentity(context.Background(), sqlcgen.GetOAuthIdentityParams{
		Provider: "fake", Subject: "sub-ada",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ident.UserID.String() != meWithToken(t, env.srv, ada).ID {
		t.Fatalf("identity owner = %v, want ada — a refused link MOVED it", ident.UserID)
	}
}

// TestOAuthLinkStartRefusesWhenNoProviderIsConfigured pins the typed 503 the
// A05 ruling replaced a bare 404 with, on an instance that does SSO not at all.
func TestOAuthLinkStartRefusesWhenNoProviderIsConfigured(t *testing.T) {
	repo := newOAuthHTTPFakeRepo()
	cfg := testConfig()
	cfg.PublicBaseURL = "http://vidra.test"
	cfg.JWTSecret = "oauth-test-http-secret-0123456789abcdef"
	issuer := auth.NewTokenIssuer(cfg.JWTSecret, "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(repo.authFakeRepo, issuer, 720*time.Hour)
	buf := &bytes.Buffer{}
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithOAuthService(auth.NewOAuthService(repo, authsvc, nil)),
		WithLogger(slog.New(slog.NewJSONHandler(buf, nil))),
	)

	// Begin: 503 with a code, not the 404 A05 measured.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/anything", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("begin with no providers = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := errorCodeOf(t, rec); code != "oauth_not_configured" {
		t.Errorf("code = %q, want oauth_not_configured", code)
	}

	// …and the same for the link start, so a client renders one answer.
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/anything/link/start", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("link start with no providers = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestOAuthLoginCallbackInsideASessionLinksRatherThanSwitches is the other half
// of A05 finding 3. The lab watched a signed-in browser go from `newcomer` to a
// freshly created `mfauser` by starting an ordinary login flow; that is an
// account switch nobody asked for.
func TestOAuthLoginCallbackInsideASessionLinksRatherThanSwitches(t *testing.T) {
	env := newOAuthEnv(t)

	// A provider-created account exists, owned by somebody else's subject.
	first := env.completeFlow(t, map[string]any{
		"sub": "sub-owner", "email": "owner@example.test", "email_verified": true,
	}, "")
	ownerSession := sessionCookieFrom(first)
	if ownerSession == nil {
		t.Fatal("first flow must mint a session")
	}
	if env.meViaRefresh(t, ownerSession).Username == "" {
		t.Fatal("harness: the provider-created account had no username")
	}

	// A DIFFERENT person, signed in with a password, starts a plain login flow
	// and authenticates as an UNLINKED provider identity: it attaches to their
	// own account, and no second account is created.
	ada := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	adaSession := loginCookie(t, env.srv, "ada@example.test", "supersecret")
	before, _ := env.repo.CountUsers(context.Background())

	cb := env.completeFlowWithCookie(t, adaSession, map[string]any{
		"sub": "sub-fresh", "email": "fresh@example.test", "email_verified": true,
	}, "/login")
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("link") != "fake" {
		t.Fatalf("Location = %q, want ?link=fake (a link, not a login)", cb.Header().Get("Location"))
	}
	if after, _ := env.repo.CountUsers(context.Background()); after != before {
		t.Errorf("users %d → %d: a login-inside-a-session created an account", before, after)
	}
	if list := identitiesWithToken(t, env.srv, ada); len(list) != 1 || list[0].Provider != "fake" {
		t.Errorf("ada's identities = %+v, want the one just connected", list)
	}

	// And the switch itself: signing in as the OTHER account's subject while
	// ada's session is live is refused outright.
	cb = env.completeFlowWithCookie(t, adaSession, map[string]any{
		"sub": "sub-owner", "email": "owner@example.test", "email_verified": true,
	}, "/login")
	loc, err = url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	// The failure key is the LOGIN page's, because that is where this attempt
	// lands: a refusal the landing page does not render is a silent one, which
	// is the only thing worse than the switch it prevents.
	if loc.Query().Get("oauth_error") != "identity_belongs_to_another_account" {
		t.Fatalf("Location = %q, want ?oauth_error=identity_belongs_to_another_account", cb.Header().Get("Location"))
	}
	if sessionCookieFrom(cb) != nil {
		t.Fatal("the account switch minted a session")
	}
	// ada's own session is untouched, and still ada's.
	if me := env.meViaRefresh(t, adaSession); me.Username != "ada" {
		t.Fatalf("the live session now belongs to %q, want ada", me.Username)
	}
}

// TestOAuthSignInIsChallengedByTheSecondFactor is the A05/A30 finding closed:
// the same account, in the same test, refuses a password login without a code
// and now refuses a provider login without one too.
func TestOAuthSignInIsChallengedByTheSecondFactor(t *testing.T) {
	env := newOAuthEnv(t, auth.WithMFA(newMFAFakeRepo(), nil, "Vidra Test"))

	token, secret, recovery := enrollAndEnable(t, env.srv)
	env.linkFromSettings(t, token, "sub-ada", "ada@example.test", "/settings")

	// The provider sign-in: NO session, the challenge flag on the landing URL,
	// and the token in an httpOnly cookie rather than the query.
	cb := env.completeFlow(t, map[string]any{
		"sub": "sub-ada", "email": "ada@example.test", "email_verified": true,
	}, "/login?oauth=1")
	if cb.Code != http.StatusFound {
		t.Fatalf("callback = %d, want 302 (body=%s)", cb.Code, cb.Body.String())
	}
	if sessionCookieFrom(cb) != nil {
		t.Fatal("an MFA-enabled account was handed a session by a provider redirect")
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("mfa") != "required" {
		t.Fatalf("Location = %q, want ?mfa=required", cb.Header().Get("Location"))
	}
	if loc.Query().Get("oauth") != "" {
		t.Errorf("Location = %q still claims a completed sign-in", cb.Header().Get("Location"))
	}
	pending := cookieFrom(cb, mfaPendingCookieName)
	if pending == nil || pending.Value == "" {
		t.Fatal("no pending-challenge cookie was set")
	}
	if !pending.HttpOnly || pending.SameSite != http.SameSiteLaxMode {
		t.Errorf("pending cookie = httpOnly:%v samesite:%v, want httpOnly + Lax", pending.HttpOnly, pending.SameSite)
	}
	if pending.MaxAge > int(mfaPendingTTL.Seconds()) || pending.MaxAge <= 0 {
		t.Errorf("pending cookie Max-Age = %d, want 0 < n <= %d", pending.MaxAge, int(mfaPendingTTL.Seconds()))
	}
	if pending.Path != mfaPendingCookiePath {
		t.Errorf("pending cookie path = %q, want %q", pending.Path, mfaPendingCookiePath)
	}
	// The token is NOT anywhere in the URL — that is the whole transport
	// decision, so assert it rather than trusting the code above.
	if strings.Contains(cb.Header().Get("Location"), pending.Value) {
		t.Fatal("the mfa token leaked into the landing URL")
	}

	// A wrong code is refused and KEEPS the cookie: a typo is not a use.
	rec := challengeWithCookie(t, env.srv, pending, `{"code":"000000","cookie_mode":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d, want 401", rec.Code)
	}
	if cleared := cookieFrom(rec, mfaPendingCookieName); cleared != nil && cleared.MaxAge < 0 {
		t.Error("a wrong code cleared the pending challenge — a typo would end the sign-in")
	}

	// The real code completes the sign-in, in cookie mode, and the cookie is
	// spent: SINGLE USE.
	rec = challengeWithCookie(t, env.srv, pending, `{"code":"`+challengeCode(t, secret)+`","cookie_mode":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if sessionCookieFrom(rec) == nil {
		t.Fatal("the completed challenge did not set the session cookie")
	}
	spent := cookieFrom(rec, mfaPendingCookieName)
	if spent == nil || spent.MaxAge >= 0 {
		t.Fatalf("the pending cookie was not cleared on success: %+v", spent)
	}

	// A recovery code satisfies a provider challenge too — the account that
	// lost its authenticator must not be locked out of its OWN sign-in method.
	cb = env.completeFlow(t, map[string]any{
		"sub": "sub-ada", "email": "ada@example.test", "email_verified": true,
	}, "/login?oauth=1")
	pending = cookieFrom(cb, mfaPendingCookieName)
	if pending == nil {
		t.Fatal("second provider sign-in set no pending cookie")
	}
	rec = challengeWithCookie(t, env.srv, pending, `{"code":"`+recovery[0]+`","cookie_mode":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery-code challenge = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// A transport token that does not resolve is refused AND swept, so a stale
	// cookie cannot make every later attempt in this browser fail with no way
	// to clear it. (That the token itself dies in five minutes is the login
	// path's own contract, pinned in internal/auth — the cookie carries the
	// SAME token and the same Max-Age, asserted above.)
	rec = challengeWithCookie(t, env.srv,
		&http.Cookie{Name: mfaPendingCookieName, Value: "not-a-token"},
		`{"code":"`+totpNow(t, secret)+`","cookie_mode":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unusable transport = %d, want 401", rec.Code)
	}
	if cleared := cookieFrom(rec, mfaPendingCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Error("an unusable transport token was left in the browser")
	}
}

// challengeWithCookie posts the MFA challenge with the pending cookie and NO
// mfa_token in the body — the provider path's shape.
func challengeWithCookie(t *testing.T, srv *Server, pending *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/challenge", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: pending.Name, Value: pending.Value})
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// cookieFrom returns the named Set-Cookie from a response, or nil.
func cookieFrom(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// errorCodeOf reads error.code out of an ErrorResponse body.
func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// meWithToken loads /auth/me with a bearer token.
func meWithToken(t *testing.T, srv *Server, token string) userView {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// identitiesWithToken lists the caller's linked identities.
func identitiesWithToken(t *testing.T, srv *Server, token string) []oauthIdentityView {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/oauth-identities", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("identities = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var out oauthIdentitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Identities
}

// loginCookie signs in with a password in cookie mode and returns the session
// cookie — the credential a top-level provider callback can actually carry.
func loginCookie(t *testing.T, srv *Server, email, password string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"email":`+jsonQuote(email)+`,"password":`+jsonQuote(password)+`,"cookie_mode":true}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d (body=%s)", rec.Code, rec.Body.String())
	}
	ck := sessionCookieFrom(rec)
	if ck == nil {
		t.Fatal("cookie-mode login set no session cookie")
	}
	return ck
}

// completeFlowWithCookie is completeFlow with a live session cookie riding the
// callback, which is what a signed-in browser actually sends.
func (e *oauthEnv) completeFlowWithCookie(t *testing.T, session *http.Cookie, claims map[string]any, returnTo string) *httptest.ResponseRecorder {
	t.Helper()
	q := ""
	if returnTo != "" {
		q = "?return_to=" + url.QueryEscape(returnTo)
	}
	loc, stateCookie := e.begin(t, q)
	e.fake.setNonce(loc.Query().Get("nonce"))
	e.fake.setClaims(claims)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oauth/fake/callback?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	req.AddCookie(&http.Cookie{Name: stateCookie.Name, Value: stateCookie.Value})
	req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

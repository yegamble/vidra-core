package httpapi

// OAuth/OIDC login flow tests, driven end-to-end against an httptest fake OIDC
// provider that serves real discovery + JWKS + token endpoints and signs real
// RS256 id_tokens. The browser hops (begin redirect → provider → callback) are
// simulated by extracting state/nonce from the authorization URL, exactly as a
// user agent would carry them.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeOIDC is a minimal but real OIDC provider: discovery, JWKS, and a token
// endpoint that signs RS256 id_tokens with a fresh test key.
type fakeOIDC struct {
	t   *testing.T
	key *rsa.PrivateKey
	srv *httptest.Server

	mu sync.Mutex
	// nonce is embedded in the next id_token (tests copy it from the begin
	// redirect, or set a wrong one to prove tamper rejection).
	nonce string
	// claims are merged over the id_token defaults (sub/email/…).
	claims map[string]any
	// lastTokenForm records the most recent token-endpoint request form.
	lastTokenForm url.Values
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDC{t: t, key: key, claims: map[string]any{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := f.key.PublicKey
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test-key",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		clientID := r.PostForm.Get("client_id")
		if clientID == "" {
			clientID, _, _ = r.BasicAuth()
		}
		f.mu.Lock()
		form := r.PostForm
		f.lastTokenForm = form
		now := time.Now()
		claims := jwt.MapClaims{
			"iss":   f.srv.URL,
			"sub":   "default-sub",
			"aud":   clientID,
			"exp":   now.Add(time.Hour).Unix(),
			"iat":   now.Unix(),
			"nonce": f.nonce,
		}
		for k, v := range f.claims {
			claims[k] = v
		}
		f.mu.Unlock()
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test-key"
		signed, err := tok.SignedString(f.key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-provider-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     signed,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// setNonce points the next id_token at an attempt nonce (or a wrong one).
func (f *fakeOIDC) setNonce(n string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce = n
}

// setClaims overrides id_token claims for the next exchange.
func (f *fakeOIDC) setClaims(c map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = c
}

func (f *fakeOIDC) tokenForm() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTokenForm
}

// oauthHTTPFakeRepo layers in-memory oauth identities over authFakeRepo.
type oauthHTTPFakeRepo struct {
	*authFakeRepo
	identities map[string]sqlcgen.OauthIdentity // keyed provider+"\x00"+subject
}

func newOAuthHTTPFakeRepo() *oauthHTTPFakeRepo {
	return &oauthHTTPFakeRepo{authFakeRepo: newAuthFakeRepo(), identities: map[string]sqlcgen.OauthIdentity{}}
}

func (f *oauthHTTPFakeRepo) GetOAuthIdentity(_ context.Context, a sqlcgen.GetOAuthIdentityParams) (sqlcgen.OauthIdentity, error) {
	if id, ok := f.identities[a.Provider+"\x00"+a.Subject]; ok {
		return id, nil
	}
	return sqlcgen.OauthIdentity{}, pgx.ErrNoRows
}

func (f *oauthHTTPFakeRepo) CreateOAuthIdentity(_ context.Context, a sqlcgen.CreateOAuthIdentityParams) (sqlcgen.OauthIdentity, error) {
	key := a.Provider + "\x00" + a.Subject
	if _, ok := f.identities[key]; ok {
		return sqlcgen.OauthIdentity{}, &pgconn.PgError{Code: "23505"}
	}
	for _, id := range f.identities {
		if id.UserID == a.UserID && id.Provider == a.Provider {
			return sqlcgen.OauthIdentity{}, &pgconn.PgError{Code: "23505"}
		}
	}
	id := sqlcgen.OauthIdentity{
		ID: uuid.New(), Provider: a.Provider, Subject: a.Subject,
		UserID: a.UserID, Email: a.Email, Handle: a.Handle, CreatedAt: time.Now(),
	}
	f.identities[key] = id
	return id, nil
}

func (f *oauthHTTPFakeRepo) UpdateOAuthIdentityHandle(_ context.Context, a sqlcgen.UpdateOAuthIdentityHandleParams) error {
	key := a.Provider + "\x00" + a.Subject
	if id, ok := f.identities[key]; ok {
		id.Handle = a.Handle
		f.identities[key] = id
	}
	return nil
}

// GetPublicUserProfileByUsername mirrors the real LEFT JOIN oauth_identities on
// provider='atproto' so the fake surfaces the linked Bluesky handle the same way.
func (f *oauthHTTPFakeRepo) GetPublicUserProfileByUsername(ctx context.Context, username string) (sqlcgen.GetPublicUserProfileByUsernameRow, error) {
	row, err := f.authFakeRepo.GetPublicUserProfileByUsername(ctx, username)
	if err != nil {
		return row, err
	}
	for _, id := range f.identities {
		if id.UserID == row.ID && id.Provider == "atproto" {
			row.BlueskyHandle = id.Handle
			break
		}
	}
	return row, nil
}

func (f *oauthHTTPFakeRepo) ListOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error) {
	var out []sqlcgen.OauthIdentity
	for _, id := range f.identities {
		if id.UserID == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *oauthHTTPFakeRepo) CountOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, id := range f.identities {
		if id.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (f *oauthHTTPFakeRepo) DeleteOAuthIdentity(_ context.Context, a sqlcgen.DeleteOAuthIdentityParams) (int64, error) {
	for k, id := range f.identities {
		if id.UserID == a.UserID && id.Provider == a.Provider {
			delete(f.identities, k)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *oauthHTTPFakeRepo) UsernameExists(_ context.Context, name string) (bool, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Username, name) {
			return true, nil
		}
	}
	return false, nil
}

// oauthEnv is a fully wired OAuth test harness.
type oauthEnv struct {
	srv  *Server
	fake *fakeOIDC
	repo *oauthHTTPFakeRepo
	cfg  *config.Config
	buf  *bytes.Buffer
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	fake := newFakeOIDC(t)
	repo := newOAuthHTTPFakeRepo()
	cfg := testConfig()
	cfg.PublicBaseURL = "http://vidra.test"
	cfg.JWTSecret = "oauth-test-http-secret-0123456789abcdef"
	cfg.OAuthProviders = []config.OAuthProviderConfig{{Name: "fake", IssuerURL: fake.srv.URL}}

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(repo.authFakeRepo, issuer, 720*time.Hour)
	oauthsvc := auth.NewOAuthService(repo, authsvc, []auth.OAuthProvider{{
		Name: "fake", IssuerURL: fake.srv.URL, ClientID: "test-client", ClientSecret: "test-client-secret",
	}})
	buf := &bytes.Buffer{}
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithOAuthService(oauthsvc),
		WithLogger(slog.New(slog.NewJSONHandler(buf, nil))),
	)
	return &oauthEnv{srv: srv, fake: fake, repo: repo, cfg: cfg, buf: buf}
}

// begin performs GET /auth/oauth/fake and returns the parsed authorization
// URL plus the state cookie the server sealed.
func (e *oauthEnv) begin(t *testing.T, query string) (*url.URL, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/fake"+query, nil)
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("begin status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	var stateCookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == oauthStateCookieName {
			stateCookie = ck
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("begin did not set the oauth state cookie")
	}
	if !stateCookie.HttpOnly {
		t.Error("state cookie must be httpOnly")
	}
	return loc, stateCookie
}

// callback performs the provider redirect with the given query and cookie.
func (e *oauthEnv) callback(t *testing.T, query string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/fake/callback"+query, nil)
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// completeFlow runs begin → provider → callback for the given id_token claims
// and returns the callback response.
func (e *oauthEnv) completeFlow(t *testing.T, claims map[string]any, returnTo string) *httptest.ResponseRecorder {
	t.Helper()
	q := ""
	if returnTo != "" {
		q = "?return_to=" + url.QueryEscape(returnTo)
	}
	loc, stateCookie := e.begin(t, q)
	e.fake.setNonce(loc.Query().Get("nonce"))
	e.fake.setClaims(claims)
	return e.callback(t, "?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
}

// sessionCookieFrom extracts a LIVE vidra_refresh session cookie from a
// response (non-empty, not a clearing Set-Cookie), or nil.
func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == refreshCookieName && ck.Value != "" && ck.MaxAge >= 0 {
			return ck
		}
	}
	return nil
}

// meViaRefresh exchanges the session cookie for an access token and loads /me.
func (e *oauthEnv) meViaRefresh(t *testing.T, session *http.Cookie) userView {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatal(err)
	}
	if ar.RefreshToken != "" {
		t.Error("cookie-mode refresh must omit refresh_token from the body")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+ar.Token)
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOAuthBeginRedirectsToProvider(t *testing.T) {
	env := newOAuthEnv(t)
	loc, _ := env.begin(t, "?return_to=/welcome")

	if !strings.HasPrefix(loc.String(), env.fake.srv.URL+"/authorize") {
		t.Fatalf("Location = %q, want the provider authorize endpoint", loc)
	}
	q := loc.Query()
	if q.Get("client_id") != "test-client" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	// P15: the redirect URI is derived from PUBLIC_BASE_URL server-side.
	wantRedirect := "http://vidra.test/api/v1/auth/oauth/fake/callback"
	if q.Get("redirect_uri") != wantRedirect {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), wantRedirect)
	}
	if q.Get("state") == "" || q.Get("nonce") == "" {
		t.Error("authorization URL must carry state and nonce")
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Error("authorization URL must carry an S256 PKCE challenge")
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope = %q, want it to include openid", q.Get("scope"))
	}
}

func TestOAuthBeginValidation(t *testing.T) {
	env := newOAuthEnv(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/unknown", nil)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider status = %d, want 404", rec.Code)
	}

	for _, bad := range []string{"https://evil.example/", "//evil.example", "/ok\\..", "relative"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/fake?return_to="+url.QueryEscape(bad), nil)
		env.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("return_to %q status = %d, want 422", bad, rec.Code)
		}
	}
}

func TestOAuthFullFlowCreatesAccountAndSession(t *testing.T) {
	env := newOAuthEnv(t)
	rec := env.completeFlow(t, map[string]any{
		"sub": "sub-100", "email": "New.Person@example.com", "email_verified": true, "name": "New Person",
	}, "/welcome")

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/welcome" {
		t.Fatalf("callback = %d → %q, want 302 → /welcome (body=%s)", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	session := sessionCookieFrom(rec)
	if session == nil {
		t.Fatal("callback must set the vidra_refresh session cookie")
	}
	// The single-use state cookie is cleared alongside.
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == oauthStateCookieName && ck.MaxAge >= 0 {
			t.Error("state cookie must be cleared by the callback")
		}
	}
	// PKCE verifier and server-derived redirect_uri reached the token endpoint.
	form := env.fake.tokenForm()
	if form.Get("code_verifier") == "" {
		t.Error("token exchange must carry the PKCE code_verifier")
	}
	if got := form.Get("redirect_uri"); got != "http://vidra.test/api/v1/auth/oauth/fake/callback" {
		t.Errorf("token exchange redirect_uri = %q", got)
	}

	// The session works: cookie → refresh → me. Username derived from the
	// name claim; email_verified inherited; first account bootstraps admin.
	me := env.meViaRefresh(t, session)
	if me.Username != "new-person" || me.Email != "New.Person@example.com" || !me.EmailVerified || me.Role != "admin" {
		t.Errorf("me = %+v; want new-person / verified / admin", me)
	}

	// Audit: an oauth register + login success, naming the provider.
	events := auditEvents(t, env.buf)
	if reg := findAudit(events, observability.ActionRegister, observability.ResultSuccess); reg == nil || reg["reason"] != "oauth:fake" {
		t.Errorf("register audit = %v, want reason oauth:fake", reg)
	}
	if login := findAudit(events, observability.ActionLogin, observability.ResultSuccess); login == nil || login["reason"] != "oauth:fake" {
		t.Errorf("login audit = %v, want reason oauth:fake", login)
	}

	// Second flow with the same subject → LOGIN to the same account.
	rec2 := env.completeFlow(t, map[string]any{
		"sub": "sub-100", "email": "New.Person@example.com", "email_verified": true, "name": "New Person",
	}, "")
	if rec2.Code != http.StatusFound || rec2.Header().Get("Location") != "/" {
		t.Fatalf("second callback = %d → %q, want 302 → /", rec2.Code, rec2.Header().Get("Location"))
	}
	session2 := sessionCookieFrom(rec2)
	if session2 == nil {
		t.Fatal("second callback must set a session cookie")
	}
	me2 := env.meViaRefresh(t, session2)
	if me2.ID != me.ID {
		t.Errorf("second flow logged into %s, want the same account %s", me2.ID, me.ID)
	}
	if n, _ := env.repo.CountUsers(context.Background()); n != 1 {
		t.Errorf("users = %d, want 1 (login must not duplicate)", n)
	}
}

func TestOAuthCallbackTamperRejected(t *testing.T) {
	env := newOAuthEnv(t)

	// (a) state mismatch.
	loc, stateCookie := env.begin(t, "")
	env.fake.setNonce(loc.Query().Get("nonce"))
	rec := env.callback(t, "?code=x&state=forged-state", stateCookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("forged state status = %d, want 400", rec.Code)
	}
	if sessionCookieFrom(rec) != nil {
		t.Error("forged state must not mint a session")
	}

	// (b) missing state cookie.
	loc2, _ := env.begin(t, "")
	rec = env.callback(t, "?code=x&state="+url.QueryEscape(loc2.Query().Get("state")), nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing cookie status = %d, want 400", rec.Code)
	}

	// (c) tampered cookie payload (signature no longer matches).
	loc3, stateCookie3 := env.begin(t, "")
	body, _, _ := strings.Cut(stateCookie3.Value, ".")
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(raw), `"return_to":"/"`, `"return_to":"/pwn"`, 1)
	tampered := &http.Cookie{Name: stateCookie3.Name, Value: base64.RawURLEncoding.EncodeToString([]byte(forged)) + "." + strings.SplitN(stateCookie3.Value, ".", 2)[1]}
	rec = env.callback(t, "?code=x&state="+url.QueryEscape(loc3.Query().Get("state")), tampered)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("tampered cookie status = %d, want 400", rec.Code)
	}

	// (d) nonce mismatch: the provider signs an id_token for a different attempt.
	loc4, stateCookie4 := env.begin(t, "")
	env.fake.setNonce("a-nonce-from-some-other-attempt")
	env.fake.setClaims(map[string]any{"sub": "sub-tamper", "email": "t@example.com", "email_verified": true})
	rec = env.callback(t, "?code=x&state="+url.QueryEscape(loc4.Query().Get("state")), stateCookie4)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("nonce mismatch status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if sessionCookieFrom(rec) != nil {
		t.Error("nonce mismatch must not mint a session")
	}
	if fail := findAudit(auditEvents(t, env.buf), observability.ActionLogin, observability.ResultFailure); fail == nil {
		t.Error("tampered attempts must emit login failure audit events")
	}
}

func TestOAuthLinksVerifiedEmailToExistingAccount(t *testing.T) {
	env := newOAuthEnv(t)
	// An existing password account…
	rec := postTo(env.srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d", rec.Code)
	}

	// …logs in via a NEW oauth identity whose verified email matches → linked.
	cb := env.completeFlow(t, map[string]any{
		"sub": "sub-ada", "email": "ADA@example.test", "email_verified": true,
	}, "")
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d → %q", cb.Code, cb.Header().Get("Location"))
	}
	session := sessionCookieFrom(cb)
	if session == nil {
		t.Fatal("link flow must mint a session")
	}
	me := env.meViaRefresh(t, session)
	if me.Username != "ada" {
		t.Errorf("linked into %q, want the existing ada account", me.Username)
	}
	if n, _ := env.repo.CountUsers(context.Background()); n != 1 {
		t.Errorf("users = %d, want 1 (link must not create)", n)
	}
	if link := findAudit(auditEvents(t, env.buf), observability.ActionOAuthLink, observability.ResultSuccess); link == nil || link["reason"] != "oauth:fake" {
		t.Errorf("link audit = %v, want auth.oauth.link success", link)
	}
}

func TestOAuthUnverifiedEmailConflictRedirectsWithError(t *testing.T) {
	env := newOAuthEnv(t)
	rec := postTo(env.srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d", rec.Code)
	}

	cb := env.completeFlow(t, map[string]any{
		"sub": "sub-bob", "email": "bob@example.test", "email_verified": false,
	}, "/login")
	if cb.Code != http.StatusFound {
		t.Fatalf("callback = %d, want 302 (body=%s)", cb.Code, cb.Body.String())
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil || loc.Path != "/login" || loc.Query().Get("oauth_error") != "email_conflict" {
		t.Errorf("Location = %q, want /login?oauth_error=email_conflict", cb.Header().Get("Location"))
	}
	if sessionCookieFrom(cb) != nil {
		t.Error("an unverified email match must never mint a session")
	}
}

func TestOAuthProviderDeniedRedirectsWithError(t *testing.T) {
	env := newOAuthEnv(t)
	loc, stateCookie := env.begin(t, "?return_to=/settings")
	_ = loc
	rec := env.callback(t, "?error=access_denied&error_description=user+denied", stateCookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("denied callback = %d, want 302", rec.Code)
	}
	got, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || got.Path != "/settings" || got.Query().Get("oauth_error") != "access_denied" {
		t.Errorf("Location = %q, want /settings?oauth_error=access_denied", rec.Header().Get("Location"))
	}
}

func TestOAuthIdentityListAndUnlink(t *testing.T) {
	env := newOAuthEnv(t)

	// OAuth-created (passwordless) account.
	cb := env.completeFlow(t, map[string]any{
		"sub": "sub-solo", "email": "solo@example.test", "email_verified": true,
	}, "")
	session := sessionCookieFrom(cb)
	if session == nil {
		t.Fatal("flow must mint a session")
	}
	// Get an access token for the identity endpoints.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	env.srv.Handler().ServeHTTP(rec, req)
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatal(err)
	}
	bearer := "Bearer " + ar.Token

	// List shows the linked identity (and its email snapshot).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me/oauth-identities", nil)
	req.Header.Set(echo.HeaderAuthorization, bearer)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, body=%s", rec.Code, rec.Body.String())
	}
	var list oauthIdentitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Identities) != 1 || list.Identities[0].Provider != "fake" || list.Identities[0].Email != "solo@example.test" {
		t.Errorf("identities = %+v", list.Identities)
	}

	// Unlinking the only identity of a passwordless account is refused (422).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/fake", nil)
	req.Header.Set(echo.HeaderAuthorization, bearer)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unlink last credential = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
	}

	// A password account that LINKED an identity may unlink it; then 404.
	if rec := postTo(env.srv, "/api/v1/auth/register", `{"username":"carol","email":"carol@example.test","password":"supersecret"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register = %d", rec.Code)
	}
	cb2 := env.completeFlow(t, map[string]any{
		"sub": "sub-carol", "email": "carol@example.test", "email_verified": true,
	}, "")
	session2 := sessionCookieFrom(cb2)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: session2.Name, Value: session2.Value})
	env.srv.Handler().ServeHTTP(rec, req)
	var ar2 authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar2); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{http.StatusNoContent, http.StatusNotFound} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/fake", nil)
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+ar2.Token)
		env.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("unlink #%d = %d, want %d (body=%s)", i+1, rec.Code, want, rec.Body.String())
		}
	}
	if unlink := findAudit(auditEvents(t, env.buf), observability.ActionOAuthUnlink, observability.ResultSuccess); unlink == nil {
		t.Error("unlink must emit an audit event")
	}

	// Auth required on both identity endpoints.
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/me/oauth-identities", nil),
		httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/fake", nil),
	} {
		rec := httptest.NewRecorder()
		env.srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anon = %d, want 401", r.Method, r.URL.Path, rec.Code)
		}
	}
}

func TestInstanceExposesOAuthProvidersAndFederation(t *testing.T) {
	cfg := testConfig()
	cfg.OAuthProviders = []config.OAuthProviderConfig{{Name: "google"}, {Name: "github-oidc"}}
	cfg.FederationEnabled = true
	srv := New(cfg, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance = %d", rec.Code)
	}
	var body struct {
		OAuthProviders    []string `json:"oauth_providers"`
		FederationEnabled bool     `json:"federation_enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.OAuthProviders) != 2 || body.OAuthProviders[0] != "google" || body.OAuthProviders[1] != "github-oidc" {
		t.Errorf("oauth_providers = %v", body.OAuthProviders)
	}
	if !body.FederationEnabled {
		t.Error("federation_enabled should be true")
	}

	// Defaults: empty array (not null) and false.
	srv2 := New(testConfig(), nil, nil)
	rec = httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if !strings.Contains(rec.Body.String(), `"oauth_providers":[]`) {
		t.Errorf("default oauth_providers should serialize as []: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"federation_enabled":false`) {
		t.Errorf("default federation_enabled should be false: %s", rec.Body.String())
	}
}

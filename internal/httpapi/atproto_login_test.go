package httpapi

// ATProto identity-login flow tests, driven end-to-end against a single httptest
// backend that stands in for the PDS + authorization server (DID document,
// protected-resource + auth-server discovery, PAR with the use_dpop_nonce dance,
// and the token endpoint). The REAL atproto client runs against it with the SSRF
// guard relaxed (allowPrivate) and a fake DNS resolver + PLC base pointing at the
// backend. The browser hops are simulated as a user agent would carry them.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/branding"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// fakeATProtoBackend is the combined PDS + auth server for the login flow.
type fakeATProtoBackend struct {
	srv    *httptest.Server
	did    string
	handle string

	parCalls int
	parState string // the state form value captured from the PAR request
}

func newFakeATProtoBackend(t *testing.T) *fakeATProtoBackend {
	t.Helper()
	b := &fakeATProtoBackend{did: "did:plc:alice", handle: "alice.example"}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"authorization_servers": []string{b.srv.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"issuer":                                         b.srv.URL,
			"pushed_authorization_request_endpoint":          b.srv.URL + "/par",
			"authorization_endpoint":                         b.srv.URL + "/authorize",
			"token_endpoint":                                 b.srv.URL + "/token",
			"scopes_supported":                               []string{"atproto"},
			"response_types_supported":                       []string{"code"},
			"grant_types_supported":                          []string{"authorization_code"},
			"code_challenge_methods_supported":               []string{"S256"},
			"dpop_signing_alg_values_supported":              []string{"ES256"},
			"authorization_response_iss_parameter_supported": true,
			"require_pushed_authorization_requests":          true,
			"client_id_metadata_document_supported":          true,
		})
	})
	mux.HandleFunc("/par", func(w http.ResponseWriter, r *http.Request) {
		b.parCalls++
		_ = r.ParseForm()
		if s := r.PostForm.Get("state"); s != "" {
			b.parState = s
		}
		// First call (no DPoP nonce) → demand one; second call succeeds.
		if b.parCalls == 1 {
			w.Header().Set("DPoP-Nonce", "par-nonce")
			w.WriteHeader(http.StatusBadRequest)
			writeTestJSON(w, map[string]any{"error": "use_dpop_nonce"})
			return
		}
		w.Header().Set("DPoP-Nonce", "token-nonce")
		writeTestJSON(w, map[string]any{"request_uri": "urn:ietf:params:oauth:request_uri:xyz", "expires_in": 60})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"access_token": "at", "token_type": "DPoP", "expires_in": 3600,
			"sub": b.did, "scope": "atproto",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/did:") {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, map[string]any{
			"id":          b.did,
			"alsoKnownAs": []string{"at://" + b.handle},
			"service": []map[string]any{{
				"id":              "#atproto_pds",
				"type":            "AtprotoPersonalDataServer",
				"serviceEndpoint": b.srv.URL,
			}},
		})
	})

	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	return b
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// atprotoLoginEnv wires a Server with the ATProto login service over the real
// atproto client pointed at the fake backend.
type atprotoLoginEnv struct {
	srv     *Server
	backend *fakeATProtoBackend
	repo    *oauthHTTPFakeRepo
	// settingssvc is the overlay the client-metadata document reads its
	// client_name through, so a test can flip the white-label toggle at runtime.
	settingssvc *instancesettings.Service
	// authsvc and cfg are kept so a test can stand a SECOND server over the
	// same accounts and sessions with a different feature flag — the only way
	// to exercise "this instance turned ATProto login off after I signed in",
	// which is exactly the step-up start's typed 503.
	authsvc *auth.Service
	cfg     *config.Config
	// mailer is wired as BOTH the auth mailer and the contact mailer, which is
	// this deployment's mail-capability signal. The email-change route refuses
	// to start a change it could never confirm, so an ATProto harness with no
	// mail path could not exercise the one recovery step that matters most for
	// a placeholder address.
	mailer *captureResetMailer
}

func newATProtoLoginEnv(t *testing.T, enabled bool) *atprotoLoginEnv {
	t.Helper()
	backend := newFakeATProtoBackend(t)
	repo := newOAuthHTTPFakeRepo()

	cfg := testConfig()
	cfg.PublicBaseURL = "https://vidra.test"
	cfg.JWTSecret = "atproto-login-test-secret-0123456789abcdef"

	client := atproto.NewOAuthClient(
		atproto.WithOAuthClientAllowPrivate(true),
		atproto.WithTXTResolver(func(_ context.Context, name string) ([]string, error) {
			if name == "_atproto."+backend.handle {
				return []string{"did=" + backend.did}, nil
			}
			return nil, nil
		}),
		atproto.WithPLCURL(backend.srv.URL),
	)

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	authsvc := auth.NewService(repo.authFakeRepo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	loginsvc := auth.NewATProtoOAuthService(repo, authsvc, client,
		auth.WithATProtoEnabled(enabled),
		auth.WithATProtoPublicBaseURL(cfg.PublicBaseURL),
		// The same wiring cmd/api uses: the consent screen's client_name is
		// resolved per request from the settings overlay (see AttributionName).
		auth.WithATProtoClientNameFunc(settingssvc.AttributionName),
	)
	buf := &bytes.Buffer{}
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithATProtoLoginService(loginsvc),
		// An instance may enable ATProto login with NO OIDC provider configured.
		// The identity list/unlink routes still have to be there — an ATProto
		// account is passwordless, so that unlink guard is the only thing
		// standing between the user and an account they cannot sign in to.
		WithOAuthService(auth.NewOAuthService(repo, authsvc, nil)),
		WithContactMailer(mailer),
		WithSettingsService(settingssvc),
		WithLogger(slog.New(slog.NewJSONHandler(buf, nil))),
	)
	return &atprotoLoginEnv{srv: srv, backend: backend, repo: repo, settingssvc: settingssvc,
		authsvc: authsvc, cfg: cfg, mailer: mailer}
}

// clientName fetches the public client-metadata document and returns the
// client_name a PDS would render on its consent screen.
func (e *atprotoLoginEnv) clientName(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/atproto/client-metadata.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("client-metadata = %d, want 200", rec.Code)
	}
	var meta struct {
		ClientName string `json:"client_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal client-metadata: %v", err)
	}
	return meta.ClientName
}

// setSetting applies one runtime-setting override through the settings service.
func (e *atprotoLoginEnv) setSetting(t *testing.T, key, value string) {
	t.Helper()
	if err := e.settingssvc.Apply(context.Background(),
		map[string]instancesettings.Update{key: {Value: value}}, uuid.New()); err != nil {
		t.Fatalf("set %s=%s: %v", key, value, err)
	}
}

// start POSTs /auth/atproto/start and returns the state cookie the server sealed.
func (e *atprotoLoginEnv) start(t *testing.T, body string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/atproto/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	e.srv.Handler().ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == atprotoStateCookieName && ck.Value != "" {
			cookie = ck
		}
	}
	return rec, cookie
}

// callback GETs /auth/atproto/callback with the given query and cookie.
func (e *atprotoLoginEnv) callback(t *testing.T, query string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/atproto/callback"+query, nil)
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestATProtoLoginFullFlow(t *testing.T) {
	env := newATProtoLoginEnv(t, true)

	rec, cookie := env.start(t, `{"handle":"alice.example","return_to":"/welcome"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cookie == nil {
		t.Fatal("start did not set the atproto state cookie")
	}
	if !cookie.HttpOnly {
		t.Error("state cookie must be httpOnly")
	}
	var startResp struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startResp); err != nil {
		t.Fatal(err)
	}
	// The authorize URL points at the auth server and carries only client_id +
	// request_uri (PAR pushed everything else).
	au, err := url.Parse(startResp.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(startResp.AuthorizationURL, env.backend.srv.URL+"/authorize") {
		t.Errorf("authorization_url = %q", startResp.AuthorizationURL)
	}
	if au.Query().Get("request_uri") == "" || au.Query().Get("client_id") == "" {
		t.Errorf("authorize query = %v", au.Query())
	}
	// The nonce dance ran during PAR.
	if env.backend.parCalls != 2 {
		t.Errorf("PAR calls = %d, want 2 (nonce retry)", env.backend.parCalls)
	}

	// Callback with the state the auth server received and the correct iss.
	cb := env.callback(t, "?code=the-code&state="+url.QueryEscape(env.backend.parState)+"&iss="+url.QueryEscape(env.backend.srv.URL), cookie)
	if cb.Code != http.StatusFound {
		t.Fatalf("callback = %d, want 302; body=%s", cb.Code, cb.Body.String())
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Path != "/welcome" || loc.Query().Get("oauth") != "1" {
		t.Errorf("Location = %q, want /welcome?oauth=1", cb.Header().Get("Location"))
	}
	session := atprotoSessionCookie(cb)
	if session == nil {
		t.Fatal("callback must set a live vidra_refresh session cookie")
	}
	// The single-use state cookie is cleared alongside.
	for _, ck := range cb.Result().Cookies() {
		if ck.Name == atprotoStateCookieName && ck.MaxAge >= 0 {
			t.Error("state cookie must be cleared by the callback")
		}
	}

	// The session works and the account was created from the handle/DID —
	// always a plain user (0104: the admin exists only via the owner-claim
	// flow, never a signup path).
	me := env.meViaRefresh(t, session)
	if me.Username != "alice" || me.Email != "did-plc-alice@atproto.invalid" || me.Role != "user" {
		t.Errorf("me = %+v; want alice / did-plc-alice@atproto.invalid / user", me)
	}

	// A replay after the cookie was cleared (browser no longer has it) is a 400.
	replay := env.callback(t, "?code=the-code&state="+url.QueryEscape(env.backend.parState)+"&iss="+url.QueryEscape(env.backend.srv.URL), nil)
	if replay.Code != http.StatusBadRequest {
		t.Errorf("replay without cookie = %d, want 400", replay.Code)
	}
}

func TestATProtoLoginDisabled(t *testing.T) {
	env := newATProtoLoginEnv(t, false)
	rec, _ := env.start(t, `{"handle":"alice.example"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("start when disabled = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "atproto_disabled" {
		t.Errorf("code = %q, want atproto_disabled", er.Error.Code)
	}
}

func TestATProtoLoginBadHandle(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	for _, body := range []string{`{"handle":""}`, `{"handle":"nodot"}`} {
		rec, _ := env.start(t, body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("start %s = %d, want 422", body, rec.Code)
		}
	}
	// A bad return_to is also 422.
	rec, _ := env.start(t, `{"handle":"alice.example","return_to":"https://evil.example"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad return_to = %d, want 422", rec.Code)
	}
}

func TestATProtoLoginCallbackRejections(t *testing.T) {
	t.Run("state mismatch", func(t *testing.T) {
		env := newATProtoLoginEnv(t, true)
		_, cookie := env.start(t, `{"handle":"alice.example"}`)
		cb := env.callback(t, "?code=x&state=forged&iss="+url.QueryEscape(env.backend.srv.URL), cookie)
		if cb.Code != http.StatusBadRequest {
			t.Errorf("state mismatch = %d, want 400", cb.Code)
		}
		if atprotoSessionCookie(cb) != nil {
			t.Error("state mismatch must not mint a session")
		}
	})

	t.Run("missing cookie", func(t *testing.T) {
		env := newATProtoLoginEnv(t, true)
		env.start(t, `{"handle":"alice.example"}`)
		cb := env.callback(t, "?code=x&state=y&iss=z", nil)
		if cb.Code != http.StatusBadRequest {
			t.Errorf("missing cookie = %d, want 400", cb.Code)
		}
	})

	t.Run("tampered cookie", func(t *testing.T) {
		env := newATProtoLoginEnv(t, true)
		_, cookie := env.start(t, `{"handle":"alice.example"}`)
		body, _, _ := strings.Cut(cookie.Value, ".")
		raw, err := base64.RawURLEncoding.DecodeString(body)
		if err != nil {
			t.Fatal(err)
		}
		// Flip the bound DID (guaranteed present) so the signed body no longer
		// matches its HMAC.
		forged := strings.Replace(string(raw), "did:plc:alice", "did:plc:evil0", 1)
		if forged == string(raw) {
			t.Fatal("tamper did not change the payload")
		}
		tampered := &http.Cookie{Name: cookie.Name, Value: base64.RawURLEncoding.EncodeToString([]byte(forged)) + "." + strings.SplitN(cookie.Value, ".", 2)[1]}
		cb := env.callback(t, "?code=x&state="+url.QueryEscape(env.backend.parState)+"&iss="+url.QueryEscape(env.backend.srv.URL), tampered)
		if cb.Code != http.StatusBadRequest {
			t.Errorf("tampered cookie = %d, want 400", cb.Code)
		}
	})

	t.Run("iss mismatch redirects with error", func(t *testing.T) {
		env := newATProtoLoginEnv(t, true)
		_, cookie := env.start(t, `{"handle":"alice.example","return_to":"/login"}`)
		cb := env.callback(t, "?code=x&state="+url.QueryEscape(env.backend.parState)+"&iss=https://evil.example", cookie)
		if cb.Code != http.StatusFound {
			t.Fatalf("iss mismatch = %d, want 302 (body=%s)", cb.Code, cb.Body.String())
		}
		loc, _ := url.Parse(cb.Header().Get("Location"))
		if loc.Path != "/login" || loc.Query().Get("oauth_error") != "atproto_identity_mismatch" {
			t.Errorf("Location = %q, want /login?oauth_error=atproto_identity_mismatch", cb.Header().Get("Location"))
		}
		if atprotoSessionCookie(cb) != nil {
			t.Error("iss mismatch must not mint a session")
		}
	})

	t.Run("provider denied", func(t *testing.T) {
		env := newATProtoLoginEnv(t, true)
		_, cookie := env.start(t, `{"handle":"alice.example","return_to":"/settings"}`)
		cb := env.callback(t, "?error=access_denied&error_description=nope", cookie)
		if cb.Code != http.StatusFound {
			t.Fatalf("denied = %d, want 302", cb.Code)
		}
		loc, _ := url.Parse(cb.Header().Get("Location"))
		if loc.Path != "/settings" || loc.Query().Get("oauth_error") != "access_denied" {
			t.Errorf("Location = %q, want /settings?oauth_error=access_denied", cb.Header().Get("Location"))
		}
	})
}

func TestATProtoClientMetadataServed(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/atproto/client-metadata.json", nil)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("client-metadata = %d, want 200", rec.Code)
	}
	var meta struct {
		ClientID                string   `json:"client_id"`
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		DPoPBoundAccessTokens   bool     `json:"dpop_bound_access_tokens"`
		Scope                   string   `json:"scope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ClientID != "https://vidra.test/api/v1/auth/atproto/client-metadata.json" {
		t.Errorf("client_id = %q", meta.ClientID)
	}
	if len(meta.RedirectURIs) != 1 || meta.RedirectURIs[0] != "https://vidra.test/api/v1/auth/atproto/callback" {
		t.Errorf("redirect_uris = %v", meta.RedirectURIs)
	}
	if meta.ClientName != branding.SoftwareName {
		t.Errorf("client_name = %q, want %q (the white-label toggle is off here)", meta.ClientName, branding.SoftwareName)
	}
	if meta.TokenEndpointAuthMethod != "none" || !meta.DPoPBoundAccessTokens || meta.Scope != "atproto" {
		t.Errorf("metadata = %+v", meta)
	}
}

// TestATProtoClientMetadataHonoursTheWhiteLabelToggle covers the one surface of
// this flow a human actually reads: the consent screen a third-party PDS
// (Bluesky) renders from client_name before handing over an account. By default
// it names the software. Once the operator white-labels the instance
// (branding_hide_software_name) it must name the INSTANCE instead — the screen
// has to name somebody, and the instance is who the user is authorising. The
// document is built in the handler, so the flip applies with no restart.
func TestATProtoClientMetadataHonoursTheWhiteLabelToggle(t *testing.T) {
	env := newATProtoLoginEnv(t, true)

	if got := env.clientName(t); got != branding.SoftwareName {
		t.Errorf("client_name = %q, want the software name %q", got, branding.SoftwareName)
	}

	// Hidden, with no instance_name override: the effective name is the config
	// default (INSTANCE_NAME), which is what an instance that never edited it has.
	env.setSetting(t, instancesettings.KeyBrandingHideSoftwareName, "true")
	if got, want := env.clientName(t), env.cfg.InstanceName; got != want {
		t.Errorf("client_name with the software name hidden = %q, want the config instance name %q", got, want)
	}

	// Hidden, with the DB overlay set: the overlay wins.
	env.setSetting(t, instancesettings.KeyInstanceName, "ExampleTube")
	if got := env.clientName(t); got != "ExampleTube" {
		t.Errorf("client_name = %q, want the overridden instance name ExampleTube", got)
	}

	// And turning the toggle back off restores the software name, so this is a
	// switch and not a one-way door.
	env.setSetting(t, instancesettings.KeyBrandingHideSoftwareName, "false")
	if got := env.clientName(t); got != branding.SoftwareName {
		t.Errorf("client_name after un-hiding = %q, want %q", got, branding.SoftwareName)
	}
}

func TestInstanceExposesATProtoLogin(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("instance = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"atproto_login":true`) {
		t.Errorf("instance should report atproto_login true: %s", rec.Body.String())
	}
	// Default (no login service) serializes false.
	srv := New(testConfig(), nil, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if !strings.Contains(rec.Body.String(), `"atproto_login":false`) {
		t.Errorf("default atproto_login should be false: %s", rec.Body.String())
	}
}

// atprotoSessionCookie extracts a LIVE vidra_refresh cookie from a response.
func atprotoSessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == refreshCookieName && ck.Value != "" && ck.MaxAge >= 0 {
			return ck
		}
	}
	return nil
}

// meViaRefresh exchanges the session cookie for an access token and loads /me.
// accessToken exchanges the cookie-mode refresh cookie for a bearer token, the
// same hop the SPA makes after the callback redirect.
func (e *atprotoLoginEnv) accessToken(t *testing.T, session *http.Cookie) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatal(err)
	}
	return ar.Token
}

func (e *atprotoLoginEnv) meViaRefresh(t *testing.T, session *http.Cookie) userView {
	t.Helper()
	token := e.accessToken(t, session)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me = %d, body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// An ATProto account is passwordless: the DID is its only credential. The
// identity routes must therefore (a) exist on an instance with no OIDC provider
// configured, (b) show the linked handle, and (c) refuse to remove the account's
// last sign-in method.
func TestATProtoIdentityListingAndUnlinkGuard(t *testing.T) {
	env := newATProtoLoginEnv(t, true)

	_, cookie := env.start(t, `{"handle":"alice.example","return_to":"/"}`)
	cb := env.callback(t, "?code=the-code&state="+url.QueryEscape(env.backend.parState)+"&iss="+url.QueryEscape(env.backend.srv.URL), cookie)
	session := atprotoSessionCookie(cb)
	if session == nil {
		t.Fatal("callback must set a session cookie")
	}
	token := env.accessToken(t, session)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/oauth-identities", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list identities = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Identities []struct {
			Provider string  `json:"provider"`
			Handle   *string `json:"handle"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Identities) != 1 || list.Identities[0].Provider != "atproto" {
		t.Fatalf("identities = %+v, want one atproto identity", list.Identities)
	}
	if list.Identities[0].Handle == nil || *list.Identities[0].Handle != "alice.example" {
		t.Errorf("identity handle = %v, want alice.example", list.Identities[0].Handle)
	}

	// The DID is the account's only credential — unlinking it would strand the
	// user, so it is refused with the set-a-password-first remedy.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/atproto", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unlink last method = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// Shipped order of checks, pinned so a refactor cannot quietly invert it:
	// the last-credential guard runs BEFORE the "is this provider even linked"
	// lookup, so a passwordless account asking to unlink a provider it never
	// linked also gets the 422 remedy rather than a 404. Nothing is removed
	// either way; the answer is merely less specific than it could be.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/google", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unlink unlinked provider on a passwordless account = %d, want 422", rec.Code)
	}
}

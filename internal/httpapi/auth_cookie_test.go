package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
)

// These lock in the cookie-mode session contract: {"cookie_mode":true} (or an
// existing vidra_refresh cookie) makes register/login/refresh carry the rotating
// refresh token in an httpOnly cookie and omit it from the JSON body; logout and
// logout-all clear the cookie; clients that never opt in see no cookie at all.

// postJSONCookies posts a JSON body with the given cookies attached.
func postJSONCookies(srv *Server, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// refreshCookieFrom extracts the vidra_refresh Set-Cookie from a response, or
// nil when the response did not set one.
func refreshCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == refreshCookieName {
			return ck
		}
	}
	return nil
}

func TestLoginCookieModeSetsRefreshCookie(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	ck := refreshCookieFrom(rec)
	if ck == nil {
		t.Fatal("cookie-mode login did not set the vidra_refresh cookie")
	}
	if ck.Value == "" {
		t.Error("refresh cookie has no value")
	}
	if !ck.HttpOnly {
		t.Error("refresh cookie must be httpOnly")
	}
	if ck.Path != refreshCookiePath {
		t.Errorf("cookie Path = %q, want %q (scoped to the auth group)", ck.Path, refreshCookiePath)
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", ck.SameSite)
	}
	if want := int((720 * time.Hour).Seconds()); ck.MaxAge != want {
		t.Errorf("cookie Max-Age = %d, want the refresh TTL %d", ck.MaxAge, want)
	}
	if ck.Secure {
		t.Error("cookie Secure = true on a plain-http test instance, want false")
	}

	// The body omits the raw refresh token: the cookie is the sole carrier.
	var body authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.RefreshToken != "" {
		t.Error("cookie-mode body still carries refresh_token")
	}
	if body.Token == "" || body.User.Username != "ada" {
		t.Errorf("access token/user missing from cookie-mode response: %+v", body)
	}
}

func TestRegisterCookieModeSetsRefreshCookie(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	ck := refreshCookieFrom(rec)
	if ck == nil || ck.Value == "" || !ck.HttpOnly {
		t.Fatalf("cookie-mode register did not set an httpOnly vidra_refresh cookie: %+v", ck)
	}
	if strings.Contains(rec.Body.String(), "refresh_token") {
		t.Error("cookie-mode register body still carries refresh_token")
	}
}

func TestLoginWithoutCookieModeSetsNoCookie(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", rec.Code)
	}
	if ck := refreshCookieFrom(rec); ck != nil {
		t.Errorf("login without cookie_mode set a vidra_refresh cookie: %+v", ck)
	}
	var body authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.RefreshToken == "" {
		t.Error("non-cookie-mode body must still carry refresh_token")
	}
}

func TestRefreshFromCookieRotatesAndResets(t *testing.T) {
	srv := authServer(t)
	reg := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
	first := refreshCookieFrom(reg)
	if first == nil {
		t.Fatal("register did not set the refresh cookie")
	}

	// No body token, no cookie_mode flag: the existing cookie alone drives the
	// refresh and keeps the session in cookie mode.
	rec := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh-from-cookie status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rotated := refreshCookieFrom(rec)
	if rotated == nil {
		t.Fatal("cookie-mode refresh did not re-set the vidra_refresh cookie")
	}
	if rotated.Value == "" || rotated.Value == first.Value {
		t.Errorf("cookie not rotated: old=%q new=%q", first.Value, rotated.Value)
	}
	if strings.Contains(rec.Body.String(), "refresh_token") {
		t.Error("cookie-mode refresh body still carries refresh_token")
	}

	// Reuse of the rotated cookie is rejected (reuse detection unchanged) and
	// the dead cookie is cleared alongside the 401.
	reuse := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, first)
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reused cookie status = %d, want 401", reuse.Code)
	}
	cleared := refreshCookieFrom(reuse)
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("invalid cookie was not cleared: %+v", cleared)
	}
}

func TestLogoutClearsCookieAndRevokes(t *testing.T) {
	srv := authServer(t)
	reg := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
	ck := refreshCookieFrom(reg)
	if ck == nil {
		t.Fatal("register did not set the refresh cookie")
	}

	out := postJSONCookies(srv, "/api/v1/auth/logout", `{}`, ck)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204; body=%s", out.Code, out.Body.String())
	}
	cleared := refreshCookieFrom(out)
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout did not clear the refresh cookie: %+v", cleared)
	}

	// The cookie's session is revoked: presenting it again cannot refresh.
	rec := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, ck)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh after cookie logout = %d, want 401", rec.Code)
	}
}

func TestLogoutAllClearsCookie(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	out := postWithAuth(srv, "/api/v1/auth/logout-all", reg.Token)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout-all status = %d, want 204", out.Code)
	}
	cleared := refreshCookieFrom(out)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("logout-all did not clear the refresh cookie: %+v", cleared)
	}
}

func TestRefreshWithoutBodyOrCookieIs422(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/refresh", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 when neither body nor cookie carries a token", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) == 0 || er.Error.Fields[0].Field != "refresh_token" {
		t.Errorf("expected a refresh_token field error, got %+v", er.Error)
	}
}

// cookieSecureServer builds an auth server whose config claims the given public
// base URL / environment, to exercise Secure-flag derivation.
func cookieSecureServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	cfg := testConfig()
	mutate(cfg)
	return New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
}

func TestRefreshCookieSecureDerivation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   bool
	}{
		{"https public base url", func(c *config.Config) { c.PublicBaseURL = "https://videos.example" }, true},
		{"http public base url", func(c *config.Config) { c.PublicBaseURL = "http://localhost:8080" }, false},
		{"production fail-secure", func(c *config.Config) { c.Environment = "production" }, true},
		{"plain dev", func(*config.Config) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := cookieSecureServer(t, tc.mutate)
			rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("register status = %d, want 201", rec.Code)
			}
			ck := refreshCookieFrom(rec)
			if ck == nil {
				t.Fatal("no refresh cookie set")
			}
			if ck.Secure != tc.want {
				t.Errorf("cookie Secure = %v, want %v", ck.Secure, tc.want)
			}
		})
	}
}

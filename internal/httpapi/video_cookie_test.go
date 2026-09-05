package httpapi

import (
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func videoCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "vidra_video_access" {
			return ck
		}
	}
	return nil
}

func TestCookieModePrivateVideoReads(t *testing.T) {
	srv := videoServer(t)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	other := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createPublishedVideo(t, srv, owner, "ada", `{"title":"Secret","privacy":"private"}`)
	login := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret","cookie_mode":true}`)
	ck := videoCookieFrom(login)
	if ck == nil {
		t.Fatal("cookie-mode login must supply native video credentials")
	}
	if !ck.HttpOnly || ck.Path != "/api/v1/videos/" || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != int(srv.authTTL.Seconds()) {
		t.Fatal("unexpected video cookie attributes")
	}
	var body authResponse
	if err := json.Unmarshal(login.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if ck.Value != body.Token {
		t.Fatal("video cookie must use the current short-lived access token")
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(ck.Value, claims); err != nil {
		t.Fatal(err)
	}
	// Match videoServer's test issuer and prove the re-signing key is valid
	// before changing expiry; otherwise this would only test a bad signature.
	signingKey := []byte("test-secret-test-secret-test-secret-0")
	valid, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.authsvc.Parse(valid); err != nil {
		t.Fatal("control token is not valid")
	}
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, token, header string
		want                              int
	}{
		{"owner original", "GET", "/api/v1/videos/" + id + "/original", ck.Value, "", 200},
		{"owner detail", "GET", "/api/v1/videos/" + id, ck.Value, "", 200},
		{"anonymous", "GET", "/api/v1/videos/" + id + "/original", "", "", 404},
		{"foreign cookie", "GET", "/api/v1/videos/" + id + "/original", other, "", 404},
		{"expired cookie", "GET", "/api/v1/videos/" + id + "/original", expired, "", 404},
		{"malformed cookie", "GET", "/api/v1/videos/" + id + "/original", "invalid", "", 404},
		{"explicit foreign bearer", "GET", "/api/v1/videos/" + id + "/original", ck.Value, "Bearer " + other, 404},
		{"explicit invalid bearer", "GET", "/api/v1/videos/" + id + "/original", ck.Value, "Bearer invalid", 404},
		{"no session mint authority", "POST", "/api/v1/videos/" + id + "/playback-session", ck.Value, "", 404},
		{"no mutation authority", "DELETE", "/api/v1/videos/" + id, ck.Value, "", 401},
		{"no account authority", "GET", "/api/v1/auth/me", ck.Value, "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: "vidra_video_access", Value: tc.token})
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d", rec.Code, tc.want)
			}
			if tc.name == "owner original" && rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("private bytes must not be cached")
			}
		})
	}
	refresh := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, refreshCookieFrom(login))
	if refreshed := videoCookieFrom(refresh); refreshed == nil || refreshed.Value == "" {
		t.Fatal("refresh must renew native media credentials")
	}
	logout := postJSONCookies(srv, "/api/v1/auth/logout", `{}`, refreshCookieFrom(refresh))
	if cleared := videoCookieFrom(logout); cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatal("logout must clear native media credentials")
	}
}

func TestVideoCookieDoesNotCredentialPublicPlayback(t *testing.T) {
	srv := videoServer(t)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, owner, "ada", `{"title":"Public","privacy":"public"}`)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/original", nil)
	req.AddCookie(&http.Cookie{Name: "vidra_video_access", Value: owner})
	c := echo.New().NewContext(req, httptest.NewRecorder())
	if _, err := srv.videoReadBase(c, uuid.MustParse(id)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := principalFromContext(c); ok || credentialedMediaRequest(c) {
		t.Fatal("public playback must remain anonymous and CDN eligible")
	}
}

package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

func TestHostMetaPointsAtWebFingerFromPublicBaseURL(t *testing.T) {
	srv := fedServer(fedTestConfig())
	// A spoofed Host must not leak into the document.
	req := httptest.NewRequest(http.MethodGet, "/.well-known/host-meta", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xrd+xml") {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	want := `template="https://videos.example/.well-known/webfinger?resource={uri}"`
	if !strings.Contains(body, `rel="lrdd"`) || !strings.Contains(body, want) {
		t.Errorf("body = %s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Error("document was built from the request Host")
	}

	rec = get(t, srv, "/.well-known/host-meta.json")
	var doc struct {
		Links []map[string]string `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Links) != 1 {
		t.Fatalf("json = %s (%v)", rec.Body.String(), err)
	}
	if doc.Links[0]["rel"] != "lrdd" || doc.Links[0]["template"] != "https://videos.example/.well-known/webfinger?resource={uri}" {
		t.Errorf("link = %v", doc.Links[0])
	}
}

func TestSecurityTxtUsesConfiguredContactOnly(t *testing.T) {
	// No contact configured: nothing honest to publish -> 404, not a fake one.
	if rec := get(t, fedServer(fedTestConfig()), "/.well-known/security.txt"); rec.Code != http.StatusNotFound {
		t.Fatalf("no contact: status = %d, want 404", rec.Code)
	}

	cfg := fedTestConfig()
	cfg.InstanceContactEmail = "ops@videos.example"
	rec := get(t, fedServer(cfg), "/.well-known/security.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		k, v, _ := strings.Cut(line, ": ")
		fields[k] = v
	}
	if fields["Contact"] != "mailto:ops@videos.example" {
		t.Errorf("Contact = %q", fields["Contact"])
	}
	if fields["Canonical"] != "https://videos.example/.well-known/security.txt" {
		t.Errorf("Canonical = %q", fields["Canonical"])
	}
	exp, err := time.Parse(time.RFC3339, fields["Expires"])
	if err != nil {
		t.Fatalf("Expires %q: %v", fields["Expires"], err)
	}
	if d := time.Until(exp); d < 300*24*time.Hour || d > 366*24*time.Hour {
		t.Errorf("Expires is %v away, want about a year", d)
	}
}

func TestChangePasswordRedirectsToSecuritySettings(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/.well-known/change-password")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://videos.example/settings/security" {
		t.Errorf("Location = %q", loc)
	}
}

func TestWebFingerAdvertisesSubscribeTemplate(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/.well-known/webfinger?resource=acct:ada@videos.example")
	var jrd federation.JRD
	if err := json.Unmarshal(rec.Body.Bytes(), &jrd); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range jrd.Links {
		if l.Rel == "http://ostatus.org/schema/1.0/subscribe" {
			found = true
			if l.Template != "https://videos.example/remote-interaction?uri={uri}" || l.Href != "" {
				t.Errorf("subscribe link = %+v", l)
			}
		}
	}
	if !found {
		t.Fatalf("no subscribe link in %s", rec.Body.String())
	}
}

// --- GET /api/v1/remote-interaction/resolve ---------------------------------

type jrdTransport struct {
	calls *[]string
	jrd   string
}

func (rt jrdTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	*rt.calls = append(*rt.calls, req.URL.String())
	if req.URL.Host != "home.example" {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}, Request: req}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(rt.jrd)), Header: http.Header{}, Request: req}, nil
}

func interactionServer(t *testing.T, jrd string, calls *[]string) *Server {
	t.Helper()
	cfg := fedTestConfig()
	svc := federation.NewService(newFedRepoFor(cfg),
		federation.WithBaseURL(cfg.PublicBaseURL),
		federation.WithFetchClient(&http.Client{Transport: jrdTransport{calls: calls, jrd: jrd}}))
	return New(cfg, nil, nil, WithFederationService(svc))
}

const interactionPath = "/api/v1/remote-interaction/resolve"

func TestResolveRemoteInteractionFillsHomeServerTemplate(t *testing.T) {
	var calls []string
	srv := interactionServer(t, `{"subject":"acct:bob@home.example","links":[
		{"rel":"self","type":"application/activity+json","href":"https://home.example/users/bob"},
		{"rel":"http://ostatus.org/schema/1.0/subscribe","template":"https://home.example/authorize_interaction?uri={uri}"}]}`, &calls)

	target := "https://videos.example/video-channels/films"
	rec := get(t, srv, interactionPath+"?handle=%40bob%40home.example&target="+target)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct{ URL string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	want := "https://home.example/authorize_interaction?uri=https%3A%2F%2Fvideos.example%2Fvideo-channels%2Ffilms"
	if out.URL != want {
		t.Errorf("url = %q, want %q", out.URL, want)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "https://home.example/.well-known/webfinger?resource=acct%3Abob%40home.example") {
		t.Errorf("fetches = %v", calls)
	}
}

func TestResolveRemoteInteractionRejectsMalformedInput(t *testing.T) {
	var calls []string
	srv := interactionServer(t, `{}`, &calls)
	for name, q := range map[string]string{
		"no handle":        "target=https://videos.example/video-channels/films",
		"no at sign":       "handle=bob&target=https://videos.example/video-channels/films",
		"path in host":     "handle=bob@home.example/evil&target=https://videos.example/video-channels/films",
		"userinfo trick":   "handle=bob@evil.example@home.example&target=https://videos.example/video-channels/films",
		"foreign target":   "handle=bob@home.example&target=https://elsewhere.example/video-channels/films",
		"missing target":   "handle=bob@home.example",
		"prefix lookalike": "handle=bob@home.example&target=https://videos.example.evil/x",
	} {
		if rec := get(t, srv, interactionPath+"?"+q); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, rec.Code)
		}
	}
	if len(calls) != 0 {
		t.Errorf("malformed input still caused fetches: %v", calls)
	}
}

func TestResolveRemoteInteractionRefusesPrivateAddresses(t *testing.T) {
	var calls []string
	srv := interactionServer(t, `{}`, &calls)
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.1", "169.254.169.254", "[::1]", "127.0.0.1:8080"} {
		rec := get(t, srv, interactionPath+"?handle=bob@"+host+"&target=https://videos.example/video-channels/films")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", host, rec.Code)
		}
	}
	if len(calls) != 0 {
		t.Errorf("a private address reached the transport: %v", calls)
	}
}

func TestResolveRemoteInteractionTemplateProblems(t *testing.T) {
	target := "&target=https://videos.example/video-channels/films"
	for name, jrd := range map[string]string{
		"no subscribe link": `{"links":[{"rel":"self","type":"application/activity+json","href":"https://home.example/u/bob"}]}`,
		"plain http":        `{"links":[{"rel":"http://ostatus.org/schema/1.0/subscribe","template":"http://home.example/i?uri={uri}"}]}`,
		"javascript scheme": `{"links":[{"rel":"http://ostatus.org/schema/1.0/subscribe","template":"javascript:alert({uri})"}]}`,
		"no placeholder":    `{"links":[{"rel":"http://ostatus.org/schema/1.0/subscribe","template":"https://home.example/i"}]}`,
	} {
		var calls []string
		rec := get(t, interactionServer(t, jrd, &calls), interactionPath+"?handle=bob@home.example"+target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
	}
	// An unreachable server is a 502, not a 5xx stack trace.
	var calls []string
	rec := get(t, interactionServer(t, `{}`, &calls), interactionPath+"?handle=bob@down.example"+target)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("unreachable: status = %d, want 502", rec.Code)
	}
}

func TestResolveRemoteInteractionIsRateLimited(t *testing.T) {
	cfg := fedTestConfig()
	var calls []string
	svc := federation.NewService(newFedRepoFor(cfg), federation.WithBaseURL(cfg.PublicBaseURL),
		federation.WithFetchClient(&http.Client{Transport: jrdTransport{calls: &calls, jrd: `{}`}}))
	srv := New(cfg, nil, nil, WithFederationService(svc),
		WithSearchResolveRateLimiter(ratelimit.NewLimiter(ratelimit.NewMemoryCounter(), 2, time.Minute)))
	url := interactionPath + "?handle=bob@home.example&target=https://videos.example/video-channels/films"
	for i := 0; i < 2; i++ {
		if rec := get(t, srv, url); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited too early", i+1)
		}
	}
	rec := get(t, srv, url)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third = %d (Retry-After %q), want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestResolveRemoteInteractionNeedsFederation(t *testing.T) {
	cfg := fedTestConfig()
	cfg.FederationEnabled = false
	var calls []string
	svc := federation.NewService(newFedRepoFor(cfg), federation.WithBaseURL(cfg.PublicBaseURL),
		federation.WithFetchClient(&http.Client{Transport: jrdTransport{calls: &calls, jrd: `{}`}}))
	srv := New(cfg, nil, nil, WithFederationService(svc))
	rec := get(t, srv, interactionPath+"?handle=bob@home.example&target=https://videos.example/video-channels/films")
	if rec.Code != http.StatusServiceUnavailable || len(calls) != 0 {
		t.Errorf("status = %d calls = %v, want 503 and no fetch", rec.Code, calls)
	}
}

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func adProxyEchoUpstream(hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"code":"Q:` + r.URL.RawQuery + `"}}`))
	}))
}

func adProxyDoGet(h http.Handler, path, ua, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path+query, nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestParseAdProxyProviders(t *testing.T) {
	ps, err := parseAdProxyProviders(`[{"path":"/x.js","upstream":"https://api.example.com/aab","params":{"key":"K"},"zone_param":"zoneId","zone_desktop":"111","zone_mobile":"222","json_code_path":"result.code"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("want 1 provider, got %d", len(ps))
	}
	if ps[0].ContentType != adProxyDefaultContentType {
		t.Errorf("default content type not applied: %q", ps[0].ContentType)
	}
	if ps[0].UpstreamUserAgent != adProxyDefaultUserAgent {
		t.Errorf("default UA not applied")
	}
	if ps[0].ttl() != adProxyDefaultTTL {
		t.Errorf("default ttl: %v", ps[0].ttl())
	}
	bad := map[string]string{
		"empty list":        `[]`,
		"relative path":     `[{"path":"x.js","upstream":"https://a.com/p"}]`,
		"reserved path":     `[{"path":"/healthz","upstream":"https://a.com/p"}]`,
		"api path":          `[{"path":"/api/v1/x","upstream":"https://a.com/p"}]`,
		"bad scheme":        `[{"path":"/x.js","upstream":"ftp://a.com/p"}]`,
		"relative upstream": `[{"path":"/x.js","upstream":"/local"}]`,
		"duplicate":         `[{"path":"/x.js","upstream":"https://a.com/p"},{"path":"/x.js","upstream":"https://b.com/p"}]`,
		"unknown field":     `[{"path":"/x.js","upstream":"https://a.com/p","bogus":1}]`,
		"not json":          `{`,
	}
	for name, in := range bad {
		if _, err := parseAdProxyProviders(in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestAdProxyZoneAndJSON(t *testing.T) {
	var hits int32
	up := adProxyEchoUpstream(&hits)
	defer up.Close()
	p := adProxyProvider{Path: "/x.js", Upstream: up.URL, Params: map[string]string{"key": "K"}, ZoneParam: "zoneId", ZoneDesktop: "111", ZoneMobile: "222", JSONCodePath: "result.code", CacheTTLSeconds: 300}
	h := newAdProxy(nil).handler(p)
	d := adProxyDoGet(h, "/x.js", "Mozilla/5.0 (Windows NT 10.0)", "")
	if d.Code != 200 {
		t.Fatalf("code=%d", d.Code)
	}
	if ct := d.Header().Get("Content-Type"); ct != adProxyDefaultContentType {
		t.Errorf("content-type=%q", ct)
	}
	if b := d.Body.String(); !strings.Contains(b, "zoneId=111") || !strings.Contains(b, "key=K") {
		t.Errorf("desktop/static wrong: %q", b)
	}
	m := adProxyDoGet(h, "/x.js", "Mozilla/5.0 (iPhone)", "")
	if b := m.Body.String(); !strings.Contains(b, "zoneId=222") {
		t.Errorf("mobile zone not used: %q", b)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("hits=%d want 2", got)
	}
}

func TestAdProxyPassthroughOverride(t *testing.T) {
	up := adProxyEchoUpstream(nil)
	defer up.Close()
	p := adProxyProvider{Path: "/x.js", Upstream: up.URL, Params: map[string]string{"key": "SECRET"}, Passthrough: true, JSONCodePath: "result.code"}
	h := newAdProxy(nil).handler(p)
	b := adProxyDoGet(h, "/x.js", "d", "?q=hello&key=attacker").Body.String()
	if !strings.Contains(b, "q=hello") {
		t.Errorf("passthrough missing: %q", b)
	}
	if !strings.Contains(b, "key=SECRET") {
		t.Errorf("static override missing: %q", b)
	}
	if strings.Contains(b, "attacker") {
		t.Errorf("client overrode static param: %q", b)
	}
}

func TestAdProxyCaching(t *testing.T) {
	var hits int32
	up := adProxyEchoUpstream(&hits)
	defer up.Close()
	p := adProxyProvider{Path: "/x.js", Upstream: up.URL, JSONCodePath: "result.code", CacheTTLSeconds: 300}
	h := newAdProxy(nil).handler(p)
	adProxyDoGet(h, "/x.js", "d", "")
	adProxyDoGet(h, "/x.js", "d", "")
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits=%d want 1 (cached)", got)
	}
}

func TestAdProxyRawMode(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("/*raw-tag*/"))
	}))
	defer up.Close()
	p := adProxyProvider{Path: "/r.js", Upstream: up.URL}
	h := newAdProxy(nil).handler(p)
	rec := adProxyDoGet(h, "/r.js", "d", "")
	if rec.Body.String() != "/*raw-tag*/" {
		t.Errorf("raw body=%q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != adProxyDefaultContentType {
		t.Errorf("ct=%q", ct)
	}
}

func TestAdProxyErrorFallback(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	p := adProxyProvider{Path: "/x.js", Upstream: up.URL}
	ap := newAdProxy(nil)
	h := ap.handler(p)
	rec := adProxyDoGet(h, "/x.js", "d", "")
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("want empty 200, got code=%d body=%q", rec.Code, rec.Body.String())
	}
	ap.store("/x.js\x00", adProxyEntry{body: []byte("STALE"), contentType: adProxyDefaultContentType, expires: time.Now().Add(-time.Hour)})
	rec = adProxyDoGet(h, "/x.js", "d", "")
	if rec.Code != 200 || rec.Body.String() != "STALE" {
		t.Errorf("stale-on-error not served: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

package cdn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustNew(t *testing.T, cfg Config) *Provider {
	t.Helper()
	p, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New(%+v) = %v", cfg.BaseURL, err)
	}
	if p == nil {
		t.Fatalf("New(%q) returned no provider", cfg.BaseURL)
	}
	return p
}

// TestNewWithoutABaseURLIsNotAnError: "no CDN" is the default every install has,
// not a misconfiguration. It has to be a nil provider rather than a provider
// that answers nothing, so cmd/api's `if base != ""` and this agree.
func TestNewWithoutABaseURLIsNotAnError(t *testing.T) {
	for _, base := range []string{"", "   "} {
		p, err := New(Config{BaseURL: base}, nil)
		if err != nil || p != nil {
			t.Fatalf("New(%q) = (%v, %v), want (nil, nil)", base, p, err)
		}
	}
}

// TestNewRejectsUnusableBases. Each of these produces an edge URL that is
// wrong in a way no request-time check can catch, because the 404 comes from
// somebody else's server.
func TestNewRejectsUnusableBases(t *testing.T) {
	for _, base := range []string{
		"cdn.example.com",             // no scheme: not a URL at all
		"/media",                      // origin-relative: nothing to redirect to
		"ftp://cdn.example.com",       // not fetchable by a browser
		"https://",                    // no host
		"https://cdn.example.com?a=b", // query: the key is appended to the PATH
		"https://cdn.example.com#frag",
	} {
		if _, err := New(Config{BaseURL: base}, nil); err == nil {
			t.Errorf("New(%q) accepted an unusable base URL", base)
		}
	}
}

// TestEdgeURLIsBasePlusTheAPIMediaPath pins the whole addressing contract: the
// edge URL is the operator's base plus THIS API's own media route path, which
// is why the CDN's origin has to be the api rather than the object store.
func TestEdgeURLIsBasePlusTheAPIMediaPath(t *testing.T) {
	const marker = "__vidra_edge=1"
	cases := []struct{ base, mediaPath, want string }{
		{"https://cdn.example.com", "/api/v1/videos/a/original",
			"https://cdn.example.com/api/v1/videos/a/original?" + marker},
		// A trailing slash on the base must not double up.
		{"https://cdn.example.com/", "/api/v1/videos/a/thumbnail",
			"https://cdn.example.com/api/v1/videos/a/thumbnail?" + marker},
		// A path prefix on the base survives.
		{"https://cdn.example.com/media", "/api/v1/videos/a/thumbnail",
			"https://cdn.example.com/media/api/v1/videos/a/thumbnail?" + marker},
		// THE GENERATION TAG TRAVELS. This is the defect the key-addressed model
		// had: cdn.EdgeURL was base + "/" + key with no query at all, so a
		// re-transcode's new ?v= never reached the edge and could not version
		// anything there.
		{"https://cdn.example.com", "/api/v1/videos/a/hls/cmaf/chunk-0-00001.m4s?v=dl9u7z2ka8zk",
			"https://cdn.example.com/api/v1/videos/a/hls/cmaf/chunk-0-00001.m4s?v=dl9u7z2ka8zk&" + marker},
		// So does a route's own variant selector: ?audio=false is a different
		// resource and therefore a different cache entry.
		{"https://cdn.example.com", "/api/v1/videos/a/download/hls/720?audio=false",
			"https://cdn.example.com/api/v1/videos/a/download/hls/720?audio=false&" + marker},
		// Percent-escaping in the path SURVIVES UNTOUCHED rather than being
		// escaped again: the caller's path is already a request URI.
		{"https://cdn.example.com", "/api/v1/channels/a%20b/avatar",
			"https://cdn.example.com/api/v1/channels/a%20b/avatar?" + marker},
	}
	for _, tc := range cases {
		p := mustNew(t, Config{BaseURL: tc.base})
		got, ok, err := p.EdgeURL(context.Background(), tc.mediaPath)
		if err != nil || !ok {
			t.Fatalf("EdgeURL(%q, %q) = (%q, %v, %v)", tc.base, tc.mediaPath, got, ok, err)
		}
		if got != tc.want {
			t.Errorf("EdgeURL(%q, %q) = %q, want %q", tc.base, tc.mediaPath, got, tc.want)
		}
	}
}

// TestEdgeURLRejectsEscapingPaths. A "../" path would address something outside
// the base — on a shared CDN account, plausibly somebody else's. This validates
// independently of the caller, because this package's caller is a resolver
// whose whole job is handling paths from elsewhere.
func TestEdgeURLRejectsEscapingPaths(t *testing.T) {
	p := mustNew(t, Config{BaseURL: "https://cdn.example.com/media"})
	for _, mediaPath := range []string{
		"",
		"api/v1/videos/a/original", // not rooted
		"//evil.example/x",         // protocol-relative: leaves the base entirely
		"/../escape",               //
		"/a/../../escape",          //
		"/a/./b",                   //
		"/a/%2e%2e/escape",         // the origin decodes before it routes
		"/a//b",                    // an empty segment some origins collapse
		"/a/b#frag",                // a fragment cannot appear in a request URI
		"/with\x00null",
		"/with\nnewline",
	} {
		got, ok, err := p.EdgeURL(context.Background(), mediaPath)
		if err == nil || ok {
			t.Errorf("EdgeURL(%q) = (%q, %v, %v), want ErrInvalidMediaPath", mediaPath, got, ok, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidMediaPath) {
			t.Errorf("EdgeURL(%q) err = %v, want ErrInvalidMediaPath", mediaPath, err)
		}
	}
}

// TestEdgeURLRefusesAPathThatAlreadyCameFromTheEdge. Minting an edge URL for a
// request the edge itself made is a redirect loop, so it is refused here as
// well as prevented in the resolver: two independent fences on the one failure
// mode this topology introduces.
func TestEdgeURLRefusesAPathThatAlreadyCameFromTheEdge(t *testing.T) {
	p := mustNew(t, Config{BaseURL: "https://cdn.example.com"})
	for _, mediaPath := range []string{
		"/api/v1/videos/a/original?__vidra_edge=1",
		"/api/v1/videos/a/original?v=abc&__vidra_edge=1",
		"/api/v1/videos/a/original?__vidra_edge=0",
	} {
		if _, ok, err := p.EdgeURL(context.Background(), mediaPath); ok || !errors.Is(err, ErrInvalidMediaPath) {
			t.Errorf("EdgeURL(%q) = (ok=%v, err=%v), want ErrInvalidMediaPath", mediaPath, ok, err)
		}
	}
}

// TestEdgeURLMakesNoNetworkCall. Asking the CDN whether it holds an object
// would put a synchronous third-party round trip on every media request in
// exchange for an answer that is stale on arrival.
func TestEdgeURLMakesNoNetworkCall(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := mustNew(t, Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	for range 5 {
		if _, _, err := p.EdgeURL(context.Background(), "/api/v1/videos/a/original"); err != nil {
			t.Fatalf("EdgeURL: %v", err)
		}
	}
	if hits != 0 {
		t.Errorf("EdgeURL made %d requests, want 0", hits)
	}
}

// purgeRecorder captures what one purge request actually looked like on the
// wire — the assertion that matters for a vendor-neutral template.
type purgeRecorder struct {
	method string
	path   string
	query  string
	header http.Header
	status int
	hits   int
}

func (r *purgeRecorder) serve(w http.ResponseWriter, req *http.Request) {
	r.hits++
	r.method = req.Method
	// EscapedPath, not Path: a purge has to name the URL byte-for-byte as the
	// edge was asked for it, so a decoded "%20" would hide a real mismatch.
	r.path = req.URL.EscapedPath()
	r.query = req.URL.RawQuery
	r.header = req.Header.Clone()
	if r.status == 0 {
		r.status = http.StatusOK
	}
	w.WriteHeader(r.status)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// TestPurgeTemplatePlaceholders proves the three placeholders between them
// spell the shapes real purge APIs come in, with no per-vendor code anywhere.
func TestPurgeTemplatePlaceholders(t *testing.T) {
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	defer srv.Close()

	const mediaPath = "/api/v1/videos/a/hls/240p/seg_00000.ts?v=abc"
	const suffix = "/api/v1/videos/a/hls/240p/seg_00000.ts?v=abc&__vidra_edge=1"
	edge := "https://cdn.example.com" + suffix

	cases := []struct {
		name       string
		base       string
		template   string
		method     string
		wantMethod string
		wantPath   string
		wantQuery  string
	}{
		{
			// Varnish / the nginx purge module / most single-URL vendor APIs:
			// the asset's OWN URL, with the PURGE method — which is why the
			// whole template can be the two words "{url}" and no method at all.
			name: "{url} against the asset's own URL",
			base: srv.URL, template: "{url}",
			wantMethod: DefaultPurgeMethod,
			wantPath:   "/api/v1/videos/a/hls/240p/seg_00000.ts",
			wantQuery:  "v=abc&__vidra_edge=1",
		},
		{
			// A "?url=" API: the edge URL has to be query-encoded or the
			// caller's own query string eats it.
			name:       "{url_encoded} as a query value",
			template:   srv.URL + "/purge?url={url_encoded}",
			method:     "POST",
			wantMethod: "POST",
			wantPath:   "/purge",
			wantQuery:  "url=" + url.QueryEscape(edge),
		},
		{
			// A zone/path API: the edge URL's path and query, no leading
			// slash. With an api origin that suffix carries a query, so a
			// template of this shape splices it into the caller's own URL —
			// which is exactly what such an API needs to name the entry.
			name:       "{key} in a path segment",
			template:   srv.URL + "/zones/7/purge/{key}",
			method:     "DELETE",
			wantMethod: "DELETE",
			wantPath:   "/zones/7/purge/api/v1/videos/a/hls/240p/seg_00000.ts",
			wantQuery:  "v=abc&__vidra_edge=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			*rec = purgeRecorder{}
			base := tc.base
			if base == "" {
				base = "https://cdn.example.com"
			}
			p := mustNew(t, Config{
				BaseURL:     base,
				PurgeURL:    tc.template,
				PurgeMethod: tc.method,
				HTTPClient:  srv.Client(),
			})
			if err := p.Purge(context.Background(), mediaPath); err != nil {
				t.Fatalf("Purge = %v", err)
			}
			if rec.hits != 1 {
				t.Fatalf("server saw %d requests, want 1", rec.hits)
			}
			if rec.method != tc.wantMethod {
				t.Errorf("method = %q, want %q", rec.method, tc.wantMethod)
			}
			if rec.path != tc.wantPath {
				t.Errorf("path = %q, want %q", rec.path, tc.wantPath)
			}
			if tc.wantQuery != "" && rec.query != tc.wantQuery {
				t.Errorf("query = %q, want %q", rec.query, tc.wantQuery)
			}
			// The point of {url_encoded}: the edge URL's own "?" and "&" must
			// not survive raw into the caller's query string, or the purge API
			// reads them as its own parameters. With an api origin the edge URL
			// always has a query, so this stopped being a corner case.
			if strings.Contains(tc.template, "{url_encoded}") && strings.Count(rec.query, "&") != 0 {
				t.Errorf("query = %q; the encoded edge URL leaked its separators", rec.query)
			}
		})
	}
}

// TestPurgeSendsTheCredentialAsAHeader. The token rides in ONE header, verbatim,
// so a Bearer API and a bare API-key header are the same code path.
func TestPurgeSendsTheCredentialAsAHeader(t *testing.T) {
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	defer srv.Close()

	// A named vendor header with a bare key.
	p := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/p/{key}",
		PurgeHeader: "X-Purge-Key", PurgeToken: "s3cret", HTTPClient: srv.Client(),
	})
	if err := p.Purge(context.Background(), "/api/v1/videos/a/original"); err != nil {
		t.Fatalf("Purge = %v", err)
	}
	if got := rec.header.Get("X-Purge-Key"); got != "s3cret" {
		t.Errorf("X-Purge-Key = %q, want the token", got)
	}
	// The marker is the only thing this package puts in a URL. The CREDENTIAL
	// is header-only, which is what this assertion is really about.
	if strings.Contains(rec.query, "s3cret") || strings.Contains(rec.path, "s3cret") {
		t.Errorf("request = %q?%q; the credential must not be put in the URL by this package", rec.path, rec.query)
	}

	// No header named: Authorization, so `Bearer …` needs no extra knob.
	*rec = purgeRecorder{}
	p2 := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/p/{key}",
		PurgeToken: "Bearer abc123", HTTPClient: srv.Client(),
	})
	if err := p2.Purge(context.Background(), "/api/v1/videos/a/original"); err != nil {
		t.Fatalf("Purge = %v", err)
	}
	if got := rec.header.Get(DefaultPurgeAuthHeader); got != "Bearer abc123" {
		t.Errorf("%s = %q, want the token", DefaultPurgeAuthHeader, got)
	}

	// No token: no auth header at all, rather than an empty one.
	*rec = purgeRecorder{}
	p3 := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/p/{key}",
		HTTPClient: srv.Client(),
	})
	if err := p3.Purge(context.Background(), "/api/v1/videos/a/original"); err != nil {
		t.Fatalf("Purge = %v", err)
	}
	if _, ok := rec.header[http.CanonicalHeaderKey(DefaultPurgeAuthHeader)]; ok {
		t.Error("an empty Authorization header was sent; want none")
	}
}

// TestPurgeStatusHandling. 404 is success: the nginx purge module (and APIs
// modelled on it) answers 404 for a URL it holds no entry for, and "there was
// never a stale copy" satisfies the postcondition exactly.
func TestPurgeStatusHandling(t *testing.T) {
	cases := []struct {
		status  int
		wantErr bool
	}{
		{http.StatusOK, false},
		{http.StatusNoContent, false},
		{http.StatusAccepted, false},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
	}
	for _, tc := range cases {
		rec := &purgeRecorder{status: tc.status}
		srv := httptest.NewServer(http.HandlerFunc(rec.serve))
		p := mustNew(t, Config{
			BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/{key}",
			HTTPClient: srv.Client(),
		})
		err := p.Purge(context.Background(), "/api/v1/videos/a/original")
		if (err != nil) != tc.wantErr {
			t.Errorf("status %d: Purge = %v, wantErr=%v", tc.status, err, tc.wantErr)
		}
		srv.Close()
	}
}

// TestPurgeErrorsCarryNoCredentialAndNoBody. Some purge APIs want the
// credential in the query string, and net/http wraps every transport failure in
// a *url.Error carrying the full URL — so an unsanitised error would put an
// operator's CDN credential in the logs of a system whose entire premise is
// that it does not hold credentials in logs.
func TestPurgeErrorsCarryNoCredentialAndNoBody(t *testing.T) {
	const secret = "tok_SUPERSECRET"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"bad key ` + secret + `","detail":"leaky body"}`))
	}))
	defer srv.Close()

	p := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/purge?token=" + secret + "&url={url_encoded}",
		PurgeToken: secret, HTTPClient: srv.Client(),
	})
	err := p.Purge(context.Background(), "/api/v1/videos/a/original")
	if err == nil {
		t.Fatal("Purge = nil for a 403")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		t.Errorf("purge error leaks the credential: %q", msg)
	}
	if strings.Contains(msg, "leaky body") {
		t.Errorf("purge error echoes the response body: %q", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Errorf("purge error = %q, want the status code an operator can act on", msg)
	}

	// The same rule on a TRANSPORT failure, where the URL is inside *url.Error.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	p2 := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: deadURL + "/purge?token=" + secret,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	err = p2.Purge(context.Background(), "/api/v1/videos/a/original")
	if err == nil {
		t.Fatal("Purge = nil against a closed server")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("transport error leaks the credential: %q", err.Error())
	}
}

// TestPurgeWithoutAnEndpointIsAnError, not a nil. See ErrPurgeNotConfigured.
func TestPurgeWithoutAnEndpointIsAnError(t *testing.T) {
	p := mustNew(t, Config{BaseURL: "https://cdn.example.com"})
	if p.CanPurge() {
		t.Error("CanPurge = true with no purge URL")
	}
	if err := p.Purge(context.Background(), "/api/v1/videos/a/original"); !errors.Is(err, ErrPurgeNotConfigured) {
		t.Fatalf("Purge = %v, want ErrPurgeNotConfigured", err)
	}
}

// TestPurgeRejectsEscapingPaths: a purge template rendered with "../" would
// invalidate somebody else's object, and with "" would address the zone root.
func TestPurgeRejectsEscapingPaths(t *testing.T) {
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	defer srv.Close()
	p := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/{key}",
		HTTPClient: srv.Client(),
	})
	for _, mediaPath := range []string{"", "no-leading-slash", "/../escape", "/a/../../escape", "/x\x00y"} {
		if err := p.Purge(context.Background(), mediaPath); !errors.Is(err, ErrInvalidMediaPath) {
			t.Errorf("Purge(%q) = %v, want ErrInvalidMediaPath", mediaPath, err)
		}
	}
	if rec.hits != 0 {
		t.Errorf("server saw %d requests for invalid paths, want 0", rec.hits)
	}
}

// TestPurgeIsBounded: a purge is a best-effort side effect and must never hold
// the operation that triggered it open on an edge that stopped answering.
func TestPurgeIsBounded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(release); srv.Close() }()

	p := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: srv.URL + "/{key}",
		PurgeTimeout: 100 * time.Millisecond, HTTPClient: srv.Client(),
	})
	start := time.Now()
	err := p.Purge(context.Background(), "/api/v1/videos/a/original")
	if err == nil {
		t.Fatal("Purge = nil against a server that never answers")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Purge took %s; the timeout did not bound it", elapsed)
	}
}

// TestDefaultsAreApplied pins the three defaults an operator gets for free.
func TestDefaultsAreApplied(t *testing.T) {
	p := mustNew(t, Config{BaseURL: "https://cdn.example.com", PurgeURL: "https://api.example/{url}"})
	if p.purgeMethod != DefaultPurgeMethod {
		t.Errorf("method default = %q, want %q", p.purgeMethod, DefaultPurgeMethod)
	}
	if p.purgeHeader != DefaultPurgeAuthHeader {
		t.Errorf("header default = %q, want %q", p.purgeHeader, DefaultPurgeAuthHeader)
	}
	if p.purgeTimeout != DefaultPurgeTimeout {
		t.Errorf("timeout default = %s, want %s", p.purgeTimeout, DefaultPurgeTimeout)
	}
	if DefaultPurgeTimeout != 10*time.Second {
		t.Errorf("DefaultPurgeTimeout = %s, want 10s", DefaultPurgeTimeout)
	}
}

// TestDescribeNeverCarriesTheToken. Describe is written into the boot log.
func TestDescribeNeverCarriesTheToken(t *testing.T) {
	const secret = "tok_SUPERSECRET"
	p := mustNew(t, Config{
		BaseURL: "https://cdn.example.com", PurgeURL: "https://api.example/p?token=" + secret,
		PurgeToken: secret,
	})
	d := p.Describe()
	if strings.Contains(d, secret) {
		t.Fatalf("Describe = %q, leaks the credential", d)
	}
	if !strings.Contains(d, "cdn.example.com") {
		t.Errorf("Describe = %q, want the edge base an operator can recognise", d)
	}
	// And the no-purge case says so, since that is the thing worth reading at boot.
	if d2 := mustNew(t, Config{BaseURL: "https://cdn.example.com"}).Describe(); !strings.Contains(d2, "no purge") {
		t.Errorf("Describe without purge = %q, want it to say so", d2)
	}
}

// TestNilProviderIsSafe: cmd/api guards on the base URL, but a nil *Provider
// reaching either method must degrade rather than panic — the resolver's whole
// contract is that an optional source never breaks a media request.
func TestNilProviderIsSafe(t *testing.T) {
	var p *Provider
	if u, ok, err := p.EdgeURL(context.Background(), "/api/v1/videos/a/original"); u != "" || ok || err != nil {
		t.Errorf("nil EdgeURL = (%q, %v, %v), want the empty no-source answer", u, ok, err)
	}
	if err := p.Purge(context.Background(), "/api/v1/videos/a/original"); err != nil {
		t.Errorf("nil Purge = %v, want nil", err)
	}
	if p.CanPurge() {
		t.Error("nil CanPurge = true")
	}
	if p.Describe() != "none" {
		t.Errorf("nil Describe = %q, want %q", p.Describe(), "none")
	}
}

// TestPurgeTargetsExactlyTheURLEdgeURLHandsOut is the invariant the whole
// invalidation path rests on, and it is a test rather than a comment because
// the two are built by different call sites minutes apart in wall-clock time.
//
// An edge holds its entry under the URL it was ASKED for. A purge that
// differed from that URL by one query parameter would be answered 404 by the
// edge — which this package treats as success, on purpose — and the operator
// would read "purged" in the log while the stale copy sat exactly where it was.
// That is strictly worse than a failure, so the two constructions are pinned to
// each other here.
func TestPurgeTargetsExactlyTheURLEdgeURLHandsOut(t *testing.T) {
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	defer srv.Close()

	for _, mediaPath := range []string{
		"/api/v1/videos/a/original",
		"/api/v1/videos/a/hls/cmaf/chunk-0-00001.m4s?v=dl9u7z2ka8zk",
		"/api/v1/videos/a/download/hls/720?audio=false",
		"/api/v1/channels/a%20b/avatar",
	} {
		p := mustNew(t, Config{BaseURL: srv.URL, PurgeURL: "{url}", HTTPClient: srv.Client()})
		want, ok, err := p.EdgeURL(context.Background(), mediaPath)
		if err != nil || !ok {
			t.Fatalf("EdgeURL(%q) = (%q, %v, %v)", mediaPath, want, ok, err)
		}
		*rec = purgeRecorder{}
		if err := p.Purge(context.Background(), mediaPath); err != nil {
			t.Fatalf("Purge(%q) = %v", mediaPath, err)
		}
		got := srv.URL + rec.path
		if rec.query != "" {
			got += "?" + rec.query
		}
		if got != want {
			t.Errorf("purge asked the edge to invalidate %q, but viewers were sent to %q", got, want)
		}
	}
}

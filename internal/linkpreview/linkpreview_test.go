package linkpreview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// privFetcher permits loopback so tests can hit httptest servers (matching
// HTTP_IMPORT_ALLOW_PRIVATE_URLS in dev/test).
func privFetcher() *Fetcher {
	return NewFetcher(urlsafety.Guard{AllowPrivate: true}, 0)
}

func TestFirstURL(t *testing.T) {
	cases := map[string]string{
		"check this https://example.com/page out": "https://example.com/page",
		"no url here":                                "",
		"trailing http://foo.test/path.":             "http://foo.test/path",
		"(wrapped https://foo.test/x)":               "https://foo.test/x",
		"ftp://nope.test only":                       "",
		"multiple https://a.test and https://b.test": "https://a.test",
	}
	for in, want := range cases {
		if got := FirstURL(in); got != want {
			t.Errorf("FirstURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseOpenGraph(t *testing.T) {
	html := `<html><head>
	<title>Fallback Title</title>
	<meta property="og:title" content="OG Title">
	<meta property="og:description" content="A description">
	<meta property="og:image" content="https://img.test/x.png">
	</head><body>ignored</body></html>`
	p := parse(strings.NewReader(html))
	if p.Title != "OG Title" || p.Description != "A description" || p.Image != "https://img.test/x.png" {
		t.Fatalf("parse = %+v", p)
	}
}

func TestParseTitleFallback(t *testing.T) {
	p := parse(strings.NewReader(`<html><head><title>Just A Title</title></head><body>x</body></html>`))
	if p.Title != "Just A Title" || p.Description != "" || p.Image != "" {
		t.Fatalf("parse = %+v", p)
	}
}

func TestFetchParsesOG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="Hello"></head></html>`))
	}))
	defer srv.Close()
	// AllowPrivate is required because httptest binds to loopback.
	f := privFetcher()
	title, _, _, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if title != "Hello" {
		t.Errorf("title = %q, want Hello", title)
	}
}

func TestFetchRejectsNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not":"html"}`))
	}))
	defer srv.Close()
	f := privFetcher()
	if _, _, _, err := f.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected non-html error")
	}
}

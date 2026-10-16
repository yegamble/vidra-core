package blocklistsub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

func TestFetcherRefusesLoopbackDirectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("a.example\n")) }))
	defer srv.Close()
	_, err := NewHTTPFetcher(false).Fetch(context.Background(), srv.URL, "")
	if !errors.Is(err, ErrBlockedTarget) {
		t.Fatalf("err = %v, want ErrBlockedTarget", err)
	}
}

func TestFetcherRefusesNonHTTPAndCredentials(t *testing.T) {
	f := NewHTTPFetcher(false)
	for _, u := range []string{"file:///etc/passwd", "ftp://x.example/l", "https://u:p@x.example/l", "http://169.254.169.254/latest"} {
		if _, err := f.Fetch(context.Background(), u, ""); !errors.Is(err, ErrBlockedTarget) {
			t.Errorf("%s: err = %v, want ErrBlockedTarget", u, err)
		}
	}
}

// roundTripFunc lets a test answer for a public-looking host without any
// network, while every other request still reaches the guarded transport.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A public list host that answers 302 -> an internal address must be refused:
// the redirect hop goes through the guarded transport.
func TestFetcherRefusesRedirectToInternalHost(t *testing.T) {
	internalHit := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHit = true
		_, _ = w.Write([]byte("secret.example\n"))
	}))
	defer internal.Close()
	urlsafety.SetTransportWrapper(func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "lists.example.test" {
				return &http.Response{
					StatusCode: http.StatusFound, Proto: "HTTP/1.1", Request: r,
					Header: http.Header{"Location": {internal.URL + "/list"}},
					Body:   http.NoBody,
				}, nil
			}
			return next.RoundTrip(r)
		})
	})
	t.Cleanup(func() { urlsafety.SetTransportWrapper(nil) })

	_, err := NewHTTPFetcher(false).Fetch(context.Background(), "https://lists.example.test/blocklist.csv", "")
	if !errors.Is(err, ErrBlockedTarget) {
		t.Fatalf("err = %v, want ErrBlockedTarget", err)
	}
	if internalHit {
		t.Fatal("the internal host was contacted")
	}
}

func TestFetcherConditionalGetAndCaps(t *testing.T) {
	var gotINM string
	big := strings.Repeat("a", MaxBodyBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		switch r.URL.Path {
		case "/304":
			w.WriteHeader(http.StatusNotModified)
		case "/big":
			_, _ = w.Write([]byte(big))
		case "/500":
			w.WriteHeader(500)
		default:
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte("a.example\n"))
		}
	}))
	defer srv.Close()
	f := NewHTTPFetcher(true) // loopback test server
	ctx := context.Background()
	if r, err := f.Fetch(ctx, srv.URL+"/ok", ""); err != nil || r.ETag != `"v1"` || string(r.Body) != "a.example\n" {
		t.Fatalf("ok: %+v %v", r, err)
	}
	if r, err := f.Fetch(ctx, srv.URL+"/304", `"v1"`); err != nil || !r.NotModified || gotINM != `"v1"` {
		t.Fatalf("304: %+v %v inm=%q", r, err, gotINM)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/big", ""); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big: %v", err)
	}
	var se StatusError
	if _, err := f.Fetch(ctx, srv.URL+"/500", ""); !errors.As(err, &se) || se.Code != 500 {
		t.Fatalf("500: %v", err)
	}
}

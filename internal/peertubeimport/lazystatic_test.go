package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The shared /lazy-static/ fetcher: the one way avatars, banners, video posters
// and storyboards get off a PeerTube host. Every gate below is here because the
// families that use it are named by rows in somebody else's database, and the
// bytes come back from a live production instance.

// Tiny real image headers. Only the first bytes matter — the gate under test is
// http.DetectContentType, which classifies on the magic number.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64))
	jpegBytes = []byte("\xff\xd8\xff\xe0" + strings.Repeat("\x00", 64))
	webpBytes = []byte("RIFF\x00\x00\x00\x00WEBPVP8 " + strings.Repeat("\x00", 64))
	gifBytes  = []byte("GIF89a" + strings.Repeat("\x00", 64))
	// What /static/avatars/<name> answers with on a real instance: the
	// single-page app's shell, at 200, instead of a 404.
	spaHTML = []byte("<!DOCTYPE html><html><head><title>PeerTube</title></head><body></body></html>")
)

// imageServer serves one canned response and records every path it was asked
// for. paths() is mutex-guarded so the assertions are safe under -race.
func imageServer(t *testing.T, status int, contentType string, body []byte) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		} else {
			// Present-but-nil suppresses net/http's own sniffing, so "" really
			// means "the server declared no type".
			w.Header()["Content-Type"] = nil
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func TestLazyStaticFetchAcceptsRealImages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        []byte
		wantExt     string
	}{
		{"png", "image/png", pngBytes, ".png"},
		{"jpeg", "image/jpeg", jpegBytes, ".jpg"},
		{"webp", "image/webp", webpBytes, ".webp"},
		// The extension comes from the BYTES, not the source filename or the
		// declared type, so a PNG named .jpg is still stored as a PNG.
		{"bytes beat the declared type", "image/jpeg", pngBytes, ".png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := imageServer(t, http.StatusOK, tc.contentType, tc.body)
			body, ext, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.jpg")
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if ext != tc.wantExt {
				t.Fatalf("ext = %q, want %q", ext, tc.wantExt)
			}
			if !bytes.Equal(body, tc.body) {
				t.Fatalf("body was not carried verbatim")
			}
		})
	}
}

// The whole point of the content-type gate: /static/avatars/<name> answers 200
// with the single-page app's HTML. Storing that would give every account a
// 62 KB HTML "avatar".
func TestLazyStaticFetchRejectsTheSPAFallback(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "text/html; charset=utf-8", spaHTML)
	_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v, want errNotAnImage", err)
	}
}

// A response that LIES about its type must not get through either: the declared
// header is a cheap first gate, the sniffed bytes are the authoritative one.
func TestLazyStaticFetchRejectsHTMLDeclaredAsAnImage(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/png", spaHTML)
	_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v, want errNotAnImage", err)
	}
}

func TestLazyStaticFetchRejectsUnsupportedImageTypes(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/gif", gifBytes)
	_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.gif")
	if !errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v, want errNotAnImage (a GIF is not something Vidra's own upload accepts)", err)
	}
}

// A server that declares nothing must not cost the operator their avatars: the
// header gate is a shortcut, the sniff is the ruling.
func TestLazyStaticFetchFallsThroughToTheSniffWithNoDeclaredType(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "", pngBytes)
	_, ext, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
	if err != nil || ext != ".png" {
		t.Fatalf("fetch = %q, %v; want .png with no error", ext, err)
	}
	// …but the bytes still have to be an image.
	srv2, _ := imageServer(t, http.StatusOK, "", spaHTML)
	if _, _, err := newLazyStaticFetcher(srv2.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png"); !errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v, want errNotAnImage", err)
	}
}

func TestLazyStaticFetchRejectsAnEmptyBody(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/png", nil)
	_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v, want errNotAnImage", err)
	}
}

// A 404 or a 500 is a TRANSIENT fact, not a statement about the file's type, so
// it must NOT come back as errNotAnImage — the caller records those
// terminal and would never look again.
func TestLazyStaticFetchMissingIsRetryableNotTerminal(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusTooManyRequests} {
		srv, _ := imageServer(t, status, "text/plain", []byte("nope"))
		_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
		if err == nil {
			t.Fatalf("status %d: want an error", status)
		}
		if errors.Is(err, errNotAnImage) {
			t.Fatalf("status %d: classified as unsupported; a bad status is retryable", status)
		}
	}
}

// An oversize image is a FACT ABOUT THE SOURCE — it will be exactly as big on
// every future run — so it must come back as its own terminal sentinel and not
// as a generic error. Classified generically it produced rows that were retried
// on every single run and failed identically every time.
func TestLazyStaticFetchBoundsTheBody(t *testing.T) {
	huge := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("x"), maxLazyStaticBytes)...)
	srv, _ := imageServer(t, http.StatusOK, "image/png", huge)
	_, _, err := newLazyStaticFetcher(srv.URL, lazyStaticAvatars).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("err = %v, want errImageTooLarge", err)
	}
	if errors.Is(err, errNotAnImage) {
		t.Fatalf("err = %v: too-big and not-an-image are different facts and get different notes", err)
	}
}

// The two terminal sentinels must stay distinct from the retryable failures, or
// the caller's classification collapses.
func TestLazyStaticTerminalSentinelsAreDistinct(t *testing.T) {
	if errors.Is(errImageTooLarge, errNotAnImage) ||
		errors.Is(errNotAnImage, errImageTooLarge) {
		t.Fatal("the size cap and the content-type gate must not match each other")
	}
}

// The filename comes out of the source database, so it is basenamed before it
// reaches a URL: a row cannot walk out of the avatars prefix.
func TestLazyStaticFetchBasenamesTheSourceFilename(t *testing.T) {
	srv, paths := imageServer(t, http.StatusOK, "image/png", pngBytes)
	f := newLazyStaticFetcher(srv.URL, lazyStaticAvatars)
	if _, _, err := f.fetch(context.Background(), "../../../etc/passwd"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := paths()[0]; got != lazyStaticAvatars+"passwd" {
		t.Fatalf("requested %q, want %q", got, lazyStaticAvatars+"passwd")
	}
	for _, empty := range []string{"", "   ", "/", "."} {
		if _, _, err := f.fetch(context.Background(), empty); !errors.Is(err, errNotAnImage) {
			t.Fatalf("filename %q: err = %v, want errNotAnImage", empty, err)
		}
	}
}

func TestLazyStaticFetchTrimsATrailingSlashFromTheOrigin(t *testing.T) {
	srv, paths := imageServer(t, http.StatusOK, "image/png", pngBytes)
	if _, _, err := newLazyStaticFetcher(srv.URL+"/", lazyStaticAvatars).fetch(context.Background(), "a.png"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := paths()[0]; got != lazyStaticAvatars+"a.png" {
		t.Fatalf("requested %q, want %q (a doubled slash would 404)", got, lazyStaticAvatars+"a.png")
	}
}

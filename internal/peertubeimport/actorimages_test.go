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

	"github.com/vidra/vidra-core/internal/profileimage"
)

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

func TestMapActorImageKind(t *testing.T) {
	if kind, ok := mapActorImageKind(1); !ok || kind != profileimage.KindAvatar {
		t.Fatalf("type 1 = %q,%v; want avatar,true", kind, ok)
	}
	if kind, ok := mapActorImageKind(2); !ok || kind != profileimage.KindBanner {
		t.Fatalf("type 2 = %q,%v; want banner,true", kind, ok)
	}
	for _, unknown := range []int{0, 3, -1, 99} {
		if kind, ok := mapActorImageKind(unknown); ok {
			t.Fatalf("type %d mapped to %q; unknown types must be unsupported, never guessed into a slot", unknown, kind)
		}
	}
}

func TestDeriveSourceOrigin(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{"account url", []string{"https://tube.example/accounts/alice"}, "https://tube.example"},
		{"channel url", []string{"https://tube.example/video-channels/news"}, "https://tube.example"},
		{"port is part of the origin", []string{"http://tube.example:9000/accounts/a"}, "http://tube.example:9000"},
		{
			"one odd row cannot steer the run",
			[]string{
				"https://evil.example/accounts/x",
				"https://tube.example/accounts/a",
				"https://tube.example/accounts/b",
				"https://tube.example/video-channels/c",
			},
			"https://tube.example",
		},
		{"ties keep the first seen", []string{"https://a.example/accounts/x", "https://b.example/accounts/y"}, "https://a.example"},
		{"unusable rows are ignored", []string{"", "   ", "not a url", "ftp://tube.example/x", "/accounts/relative"}, ""},
		{"nothing at all", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveSourceOrigin(tc.urls); got != tc.want {
				t.Fatalf("deriveSourceOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageExtForSniffedType(t *testing.T) {
	for ct, want := range map[string]string{
		"image/jpeg":    ".jpg",
		"image/png":     ".png",
		"image/webp":    ".webp",
		"IMAGE/PNG":     ".png",
		"image/gif":     "", // Vidra's own upload path does not accept it either
		"image/svg+xml": "",
		"text/html":     "",
		"":              "",
	} {
		if got := imageExtForSniffedType(ct); got != want {
			t.Fatalf("imageExtForSniffedType(%q) = %q, want %q", ct, got, want)
		}
	}
}

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

func TestActorImageFetchAcceptsRealImages(t *testing.T) {
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
			body, ext, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.jpg")
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
func TestActorImageFetchRejectsTheSPAFallback(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "text/html; charset=utf-8", spaHTML)
	_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want errActorImageNotAnImage", err)
	}
}

// A response that LIES about its type must not get through either: the declared
// header is a cheap first gate, the sniffed bytes are the authoritative one.
func TestActorImageFetchRejectsHTMLDeclaredAsAnImage(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/png", spaHTML)
	_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want errActorImageNotAnImage", err)
	}
}

func TestActorImageFetchRejectsUnsupportedImageTypes(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/gif", gifBytes)
	_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.gif")
	if !errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want errActorImageNotAnImage (a GIF is not something Vidra's own upload accepts)", err)
	}
}

// A server that declares nothing must not cost the operator their avatars: the
// header gate is a shortcut, the sniff is the ruling.
func TestActorImageFetchFallsThroughToTheSniffWithNoDeclaredType(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "", pngBytes)
	_, ext, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
	if err != nil || ext != ".png" {
		t.Fatalf("fetch = %q, %v; want .png with no error", ext, err)
	}
	// …but the bytes still have to be an image.
	srv2, _ := imageServer(t, http.StatusOK, "", spaHTML)
	if _, _, err := newActorImageFetcher(srv2.URL).fetch(context.Background(), "abc.png"); !errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want errActorImageNotAnImage", err)
	}
}

func TestActorImageFetchRejectsAnEmptyBody(t *testing.T) {
	srv, _ := imageServer(t, http.StatusOK, "image/png", nil)
	_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
	if !errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want errActorImageNotAnImage", err)
	}
}

// A 404 or a 500 is a TRANSIENT fact, not a statement about the file's type, so
// it must NOT come back as errActorImageNotAnImage — the caller records those
// terminal and would never look again.
func TestActorImageFetchMissingIsRetryableNotTerminal(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusTooManyRequests} {
		srv, _ := imageServer(t, status, "text/plain", []byte("nope"))
		_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
		if err == nil {
			t.Fatalf("status %d: want an error", status)
		}
		if errors.Is(err, errActorImageNotAnImage) {
			t.Fatalf("status %d: classified as unsupported; a bad status is retryable", status)
		}
	}
}

func TestActorImageFetchBoundsTheBody(t *testing.T) {
	huge := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("x"), maxActorImageBytes)...)
	srv, _ := imageServer(t, http.StatusOK, "image/png", huge)
	_, _, err := newActorImageFetcher(srv.URL).fetch(context.Background(), "abc.png")
	if err == nil || errors.Is(err, errActorImageNotAnImage) {
		t.Fatalf("err = %v, want a size-cap error", err)
	}
}

// The filename comes out of the source database, so it is basenamed before it
// reaches a URL: a row cannot walk out of the avatars prefix.
func TestActorImageFetchBasenamesTheSourceFilename(t *testing.T) {
	srv, paths := imageServer(t, http.StatusOK, "image/png", pngBytes)
	f := newActorImageFetcher(srv.URL)
	if _, _, err := f.fetch(context.Background(), "../../../etc/passwd"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := paths()[0]; got != actorImageLazyStatic+"passwd" {
		t.Fatalf("requested %q, want %q", got, actorImageLazyStatic+"passwd")
	}
	for _, empty := range []string{"", "   ", "/", "."} {
		if _, _, err := f.fetch(context.Background(), empty); !errors.Is(err, errActorImageNotAnImage) {
			t.Fatalf("filename %q: err = %v, want errActorImageNotAnImage", empty, err)
		}
	}
}

func TestActorImageFetchTrimsATrailingSlashFromTheOrigin(t *testing.T) {
	srv, paths := imageServer(t, http.StatusOK, "image/png", pngBytes)
	if _, _, err := newActorImageFetcher(srv.URL+"/").fetch(context.Background(), "a.png"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := paths()[0]; got != actorImageLazyStatic+"a.png" {
		t.Fatalf("requested %q, want %q (a doubled slash would 404)", got, actorImageLazyStatic+"a.png")
	}
}

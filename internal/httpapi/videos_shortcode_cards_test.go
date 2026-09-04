package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// cardsFrom issues a GET and returns the card list from either response shape
// the discovery surfaces use (feed carries sort/scope, lists do not).
func cardsFrom(t *testing.T, srv *Server, path string) []videoView {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Videos []videoView `json:"videos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return body.Videos
}

// Every LOCAL discovery surface must carry the short code, and it must be the
// SAME code the detail view reports — otherwise a card would link to a
// different video, or to nothing.
//
// This is the property stage 5 depends on: the frontend builds /v/{code} links
// from cards, and a card without a code silently falls back to the long URL.
func TestLocalCardsCarryTheSameShortCodeAsDetail(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"findable","privacy":"public"}`)
	want := shortCodeOf(t, srv, id, tok)

	surfaces := []struct{ name, path string }{
		{"public feed", "/api/v1/videos"},
		{"search", "/api/v1/videos/search?q=findable"},
		{"channel videos", "/api/v1/channels/ada/videos"},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			cards := cardsFrom(t, srv, s.path)
			if len(cards) == 0 {
				t.Fatalf("%s returned no cards", s.name)
			}
			for _, c := range cards {
				if c.ID != id {
					continue
				}
				if c.ShortCode == "" {
					t.Fatalf("%s card carries no short_code", s.name)
				}
				if c.ShortCode != want {
					t.Fatalf("%s card short_code = %q, detail says %q", s.name, c.ShortCode, want)
				}
				if !video.ValidShortCode(c.ShortCode) {
					t.Fatalf("%s card short_code %q is not a valid code", s.name, c.ShortCode)
				}
				return
			}
			t.Fatalf("%s did not return the seeded video %s", s.name, id)
		})
	}
}

// A REMOTE federated card has no local short code and must omit the field
// entirely — emitting an empty string would let a client build /v/ and 404.
// The UNION queries select ”::text for these rows; omitempty does the rest.
func TestRemoteCardsOmitShortCode(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	stream := "https://tube.remote.example/stream.m3u8"
	repo.remoteSearch = append(repo.remoteSearch, sqlcgen.SearchPublicVideosRow{
		ID: uuid.New(), Remote: true, Title: "Remote premiere", Description: "from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		ChannelHandle: "movies@tube.remote.example", ChannelDisplayName: "movies",
		Domain: "tube.remote.example", WatchUrl: "https://tube.remote.example/videos/watch/abc",
		StreamUrl: &stream,
	})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=premiere", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Assert on the RAW JSON: a struct decode cannot tell "" from absent, and
	// absent is the contract.
	var raw struct {
		Videos []map[string]any `json:"videos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawRemote bool
	for _, c := range raw.Videos {
		if c["remote"] != true {
			continue
		}
		sawRemote = true
		if _, present := c["short_code"]; present {
			t.Fatalf("remote card emitted short_code: %v", c["short_code"])
		}
	}
	if !sawRemote {
		t.Fatal("no remote card in the search response; the fixture did not reach the assertion")
	}
}

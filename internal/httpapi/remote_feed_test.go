package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Remote cards on the discovery surfaces (remote-content §3-4): the
// subscriptions feed, the public feed with scope=all, and search carry
// remote:true entries with origin domain + watch/stream URLs and no local-only
// fields.

// seedStream is the stream URL the seeded remote card advertises.
var seedStream = "https://tube.remote.example/hls/abc.m3u8"

// decodeCards unmarshals a feed/search body's videos into raw maps so tests
// can assert which JSON keys are present.
func decodeCards(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var envelope struct {
		Videos []map[string]any `json:"videos"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal feed: %v (body=%s)", err, body)
	}
	return envelope.Videos
}

// findRemoteCard returns the remote:true card, or nil.
func findRemoteCard(cards []map[string]any) map[string]any {
	for _, c := range cards {
		if c["remote"] == true {
			return c
		}
	}
	return nil
}

// assertRemoteCardShape checks the remote-card contract: remote fields
// present, local-only fields absent.
func assertRemoteCardShape(t *testing.T, card map[string]any) {
	t.Helper()
	if card["domain"] != "tube.remote.example" {
		t.Errorf("domain = %v", card["domain"])
	}
	if card["watch_url"] != "https://tube.remote.example/videos/watch/abc" {
		t.Errorf("watch_url = %v", card["watch_url"])
	}
	if card["stream_url"] != seedStream {
		t.Errorf("stream_url = %v", card["stream_url"])
	}
	if card["channel_handle"] != "movies@tube.remote.example" {
		t.Errorf("channel_handle = %v", card["channel_handle"])
	}
	for _, key := range []string{"channel_id", "privacy", "state"} {
		if _, ok := card[key]; ok {
			t.Errorf("remote card must omit local-only %q (got %v)", key, card[key])
		}
	}
}

func TestSubscriptionFeedCarriesRemoteCards(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	repo.remoteSubs = append(repo.remoteSubs, sqlcgen.ListSubscriptionVideosRow{
		ID: uuid.New(), Remote: true, Title: "Remote premiere", Description: "from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(), HasThumbnail: true,
		ChannelHandle: "movies@tube.remote.example", ChannelDisplayName: "movies",
		Domain: "tube.remote.example", WatchUrl: "https://tube.remote.example/videos/watch/abc",
		StreamUrl: &seedStream,
	})

	rec := getWithAuth(srv, "/api/v1/me/subscriptions/videos", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscriptions = %d; body=%s", rec.Code, rec.Body.String())
	}
	card := findRemoteCard(decodeCards(t, rec.Body.Bytes()))
	if card == nil {
		t.Fatal("no remote card in the subscriptions feed")
	}
	assertRemoteCardShape(t, card)
}

func TestPublicFeedScopeAllIncludesRemote(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	repo.remoteFeed = append(repo.remoteFeed, sqlcgen.ListPublicVideosSortedRow{
		ID: uuid.New(), Remote: true, Title: "Remote premiere", Description: "from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		ChannelHandle: "movies@tube.remote.example", ChannelDisplayName: "movies",
		Domain: "tube.remote.example", WatchUrl: "https://tube.remote.example/videos/watch/abc",
		StreamUrl: &seedStream,
	})

	// Default scope is local: no remote cards, and the response says so.
	rec := getPath(srv, "/api/v1/videos")
	var envelope struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	if envelope.Scope != "local" {
		t.Errorf("default scope = %q, want local", envelope.Scope)
	}
	if findRemoteCard(decodeCards(t, rec.Body.Bytes())) != nil {
		t.Error("scope=local feed leaked a remote card")
	}

	// Unknown scope values fall back to local (like sort).
	rec = getPath(srv, "/api/v1/videos?scope=bogus")
	if findRemoteCard(decodeCards(t, rec.Body.Bytes())) != nil {
		t.Error("scope=bogus feed leaked a remote card")
	}

	// scope=all mixes the remote card in.
	rec = getPath(srv, "/api/v1/videos?scope=all")
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	if envelope.Scope != "all" {
		t.Errorf("scope = %q, want all", envelope.Scope)
	}
	card := findRemoteCard(decodeCards(t, rec.Body.Bytes()))
	if card == nil {
		t.Fatal("scope=all feed has no remote card")
	}
	assertRemoteCardShape(t, card)

	// Local taxonomy filters exclude remote videos (they carry none).
	rec = getPath(srv, "/api/v1/videos?scope=all&tag=cats")
	if findRemoteCard(decodeCards(t, rec.Body.Bytes())) != nil {
		t.Error("a tag-filtered feed leaked a remote card")
	}
}

func TestSearchIncludesRemoteMatches(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	repo.remoteSearch = append(repo.remoteSearch, sqlcgen.SearchPublicVideosRow{
		ID: uuid.New(), Remote: true, Title: "Remote premiere", Description: "from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		ChannelHandle: "movies@tube.remote.example", ChannelDisplayName: "movies",
		Domain: "tube.remote.example", WatchUrl: "https://tube.remote.example/videos/watch/abc",
		StreamUrl: &seedStream,
	})

	rec := getPath(srv, "/api/v1/videos/search?q=premiere")
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d; body=%s", rec.Code, rec.Body.String())
	}
	card := findRemoteCard(decodeCards(t, rec.Body.Bytes()))
	if card == nil {
		t.Fatal("no remote card in search results")
	}
	assertRemoteCardShape(t, card)

	// A non-matching query keeps it out.
	rec = getPath(srv, "/api/v1/videos/search?q=zzznotfound")
	if findRemoteCard(decodeCards(t, rec.Body.Bytes())) != nil {
		t.Error("non-matching search leaked the remote card")
	}
}

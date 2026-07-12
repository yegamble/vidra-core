package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- query classification (table-driven, config-parity W13) -----------------

func TestClassifySearchQuery(t *testing.T) {
	cases := []struct {
		q    string
		want SearchQueryKind
	}{
		// URI-shaped.
		{"https://tube.example/videos/abc", SearchQueryURI},
		{"http://tube.example/accounts/marta", SearchQueryURI},
		{"  https://tube.example/videos/abc  ", SearchQueryURI},
		{"https://tube.example:8443/v/1", SearchQueryURI},
		// Handle-shaped.
		{"movies@tube.example", SearchQueryHandle},
		{"@movies@tube.example", SearchQueryHandle},
		{"movies@tube.example:8080", SearchQueryHandle},
		{"marta@sub.tube.example", SearchQueryHandle},
		// Plain text (never resolved).
		{"", SearchQueryNone},
		{"go concurrency patterns", SearchQueryNone},
		{"cats", SearchQueryNone},
		{"a@b", SearchQueryNone},          // bare single-label host: ordinary text
		{"@only.domain", SearchQueryNone}, // no name part
		{"movies@", SearchQueryNone},
		{"@movies", SearchQueryNone},
		{"a@@b.example", SearchQueryNone},
		{"name with space@tube.example", SearchQueryNone},
		{"movies@tube.example/path", SearchQueryNone},
		{"ftp://tube.example/videos/abc", SearchQueryNone},
		{"https://", SearchQueryNone},
		{"https:// tube.example/x", SearchQueryNone},
	}
	for _, tc := range cases {
		if got := ClassifySearchQuery(tc.q); got != tc.want {
			t.Errorf("ClassifySearchQuery(%q) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

// --- resolution harness ------------------------------------------------------

// jsonTransport serves canned JSON documents by exact URL (a search-flavoured
// sibling of the contract suite's fixtureTransport) and counts requests so
// tests can assert that gates/blocklists stop resolution BEFORE any fetch.
type jsonTransport struct {
	docs  map[string]string
	calls *int
}

func (jt jsonTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if jt.calls != nil {
		*jt.calls++
	}
	body, ok := jt.docs[req.URL.String()]
	status := http.StatusOK
	if !ok {
		status, body = http.StatusNotFound, "{}"
	}
	h := http.Header{}
	h.Set("Content-Type", "application/activity+json")
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h, Request: req}, nil
}

const (
	searchOrigin   = "https://tube.example"
	searchVideoURL = searchOrigin + "/videos/abc"
	searchOwnerURL = searchOrigin + "/accounts/marta"
	searchGroupURL = searchOrigin + "/video-channels/movies"
)

func searchDocs() map[string]string {
	return map[string]string{
		searchVideoURL: `{
			"id": "` + searchVideoURL + `",
			"type": "Video",
			"name": "Dawn crossing",
			"content": "a remote premiere",
			"attributedTo": "` + searchOwnerURL + `",
			"duration": "PT62S",
			"url": [
				{"mediaType": "text/html", "href": "` + searchOrigin + `/w/abc"},
				{"mediaType": "application/x-mpegURL", "href": "` + searchOrigin + `/hls/abc.m3u8"}
			]
		}`,
		searchOwnerURL: `{
			"id": "` + searchOwnerURL + `",
			"type": "Person",
			"preferredUsername": "marta",
			"inbox": "` + searchOrigin + `/accounts/marta/inbox",
			"publicKey": {"publicKeyPem": "PEM"}
		}`,
		searchGroupURL: `{
			"id": "` + searchGroupURL + `",
			"type": "Group",
			"preferredUsername": "movies",
			"inbox": "` + searchOrigin + `/video-channels/movies/inbox",
			"publicKey": {"publicKeyPem": "PEM"}
		}`,
		"https://tube.example/.well-known/webfinger?resource=" + url.QueryEscape("acct:movies@tube.example"): `{
			"links": [{"rel": "self", "type": "application/activity+json", "href": "` + searchGroupURL + `"}]
		}`,
	}
}

func searchService(docs map[string]string, calls *int) (*Service, fakeRepo) {
	repo := newFollowRepo(uuid.New())
	repo.blockedDomains = map[string]bool{}
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithFetchClient(&http.Client{Transport: jsonTransport{docs: docs, calls: calls}}),
	)
	return svc, repo
}

// --- resolution behaviour ----------------------------------------------------

func TestResolveSearchTargetVideoURI(t *testing.T) {
	svc, repo := searchService(searchDocs(), nil)

	res, err := svc.ResolveSearchTarget(context.Background(), searchVideoURL)
	if err != nil {
		t.Fatalf("ResolveSearchTarget: %v", err)
	}
	if res.VideoID == uuid.Nil || res.Actor != nil {
		t.Fatalf("resolution = %+v, want a video id only", res)
	}
	rv, ok := repo.remoteVideos[searchVideoURL]
	if !ok {
		t.Fatalf("resolved video was not stored: %+v", repo.remoteVideos)
	}
	if rv.id != res.VideoID {
		t.Errorf("returned id %s != stored id %s", res.VideoID, rv.id)
	}
	if rv.params.RemoteActorUrl != searchOwnerURL {
		t.Errorf("owner = %q, want %q", rv.params.RemoteActorUrl, searchOwnerURL)
	}
	if rv.params.Title != "Dawn crossing" {
		t.Errorf("title = %q", rv.params.Title)
	}
	if rv.params.StreamUrl == nil || *rv.params.StreamUrl != searchOrigin+"/hls/abc.m3u8" {
		t.Errorf("stream_url = %v, want the HLS link", rv.params.StreamUrl)
	}
	if _, ok := repo.remoteActors[searchOwnerURL]; !ok {
		t.Error("owner actor was not resolved+cached before the video upsert (FK target)")
	}
}

func TestResolveSearchTargetHandle(t *testing.T) {
	svc, repo := searchService(searchDocs(), nil)

	for _, q := range []string{"movies@tube.example", "@movies@tube.example"} {
		res, err := svc.ResolveSearchTarget(context.Background(), q)
		if err != nil {
			t.Fatalf("ResolveSearchTarget(%q): %v", q, err)
		}
		if res.Actor == nil || res.VideoID != uuid.Nil {
			t.Fatalf("resolution = %+v, want an actor only", res)
		}
		if res.Actor.Kind != SearchResultChannel {
			t.Errorf("kind = %q, want channel (Group actor)", res.Actor.Kind)
		}
		if res.Actor.Handle != "movies@tube.example" || res.Actor.Domain != "tube.example" || res.Actor.ActorURL != searchGroupURL {
			t.Errorf("actor = %+v", res.Actor)
		}
	}
	if _, ok := repo.remoteActors[searchGroupURL]; !ok {
		t.Error("resolved channel actor was not cached")
	}
}

func TestResolveSearchTargetActorURIKinds(t *testing.T) {
	svc, _ := searchService(searchDocs(), nil)

	// A Person actor URL resolves as an account result.
	res, err := svc.ResolveSearchTarget(context.Background(), searchOwnerURL)
	if err != nil {
		t.Fatalf("ResolveSearchTarget(person): %v", err)
	}
	if res.Actor == nil || res.Actor.Kind != SearchResultAccount || res.Actor.Handle != "marta@tube.example" {
		t.Fatalf("person resolution = %+v, want an account actor", res.Actor)
	}

	// A Group actor URL resolves as a channel result.
	res, err = svc.ResolveSearchTarget(context.Background(), searchGroupURL)
	if err != nil {
		t.Fatalf("ResolveSearchTarget(group): %v", err)
	}
	if res.Actor == nil || res.Actor.Kind != SearchResultChannel {
		t.Fatalf("group resolution = %+v, want a channel actor", res.Actor)
	}
}

func TestResolveSearchTargetAuthority(t *testing.T) {
	// A video document whose canonical id / owner lives on a DIFFERENT origin
	// than the queried URL is refused (the same-origin rules the inbox applies).
	docs := searchDocs()
	docs[searchOrigin+"/videos/foreign-id"] = `{
		"id": "https://elsewhere.example/videos/foreign",
		"type": "Video", "name": "spoof", "attributedTo": "` + searchOwnerURL + `"
	}`
	docs[searchOrigin+"/videos/foreign-owner"] = `{
		"id": "` + searchOrigin + `/videos/foreign-owner",
		"type": "Video", "name": "spoof", "attributedTo": "https://elsewhere.example/accounts/eve"
	}`
	svc, repo := searchService(docs, nil)

	for _, q := range []string{searchOrigin + "/videos/foreign-id", searchOrigin + "/videos/foreign-owner"} {
		if _, err := svc.ResolveSearchTarget(context.Background(), q); !errors.Is(err, ErrRemoteUnresolvable) {
			t.Errorf("ResolveSearchTarget(%q) err = %v, want ErrRemoteUnresolvable", q, err)
		}
	}
	if len(repo.remoteVideos) != 0 {
		t.Errorf("foreign-attributed objects were stored: %+v", repo.remoteVideos)
	}
}

func TestResolveSearchTargetLocalAndBlocked(t *testing.T) {
	calls := 0
	svc, repo := searchService(searchDocs(), &calls)
	repo.blockedDomains["blocked.example"] = true

	// Local targets are refused without any fetch (local search covers them).
	for _, q := range []string{"https://videos.example/videos/xyz", "ada@videos.example"} {
		if _, err := svc.ResolveSearchTarget(context.Background(), q); !errors.Is(err, ErrLocalFollowTarget) {
			t.Errorf("ResolveSearchTarget(%q) err = %v, want ErrLocalFollowTarget", q, err)
		}
	}
	// Blocked domains are refused BEFORE any outbound fetch.
	for _, q := range []string{"https://blocked.example/videos/1", "user@blocked.example"} {
		if _, err := svc.ResolveSearchTarget(context.Background(), q); !errors.Is(err, ErrRemoteUnresolvable) {
			t.Errorf("ResolveSearchTarget(%q) err = %v, want ErrRemoteUnresolvable", q, err)
		}
	}
	// Plain text is never a target.
	if _, err := svc.ResolveSearchTarget(context.Background(), "go concurrency"); !errors.Is(err, ErrNotSearchTarget) {
		t.Errorf("plain text err = %v, want ErrNotSearchTarget", err)
	}
	if calls != 0 {
		t.Errorf("outbound fetches = %d, want 0 (refused before fetching)", calls)
	}
}

func TestResolveSearchTargetSSRFGuard(t *testing.T) {
	// No injected fetch client and no AllowPrivateFetch: resolution builds the
	// SSRF-guarded client, and the pre-flight URL validation refuses non-public
	// literal addresses outright — no dial is ever attempted.
	repo := newFollowRepo(uuid.New())
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	for _, q := range []string{
		"http://127.0.0.1/videos/abc",
		"http://169.254.169.254/latest/meta-data",
		"https://10.0.0.8/videos/abc",
	} {
		if _, err := svc.ResolveSearchTarget(context.Background(), q); err == nil {
			t.Errorf("ResolveSearchTarget(%q) = nil error, want the SSRF guard to refuse", q)
		}
	}
}

func TestResolveSearchTargetTimeout(t *testing.T) {
	// A hung origin: the transport parks until the request context is done, so
	// resolution must return promptly once the caller's deadline expires.
	hang := http.Client{Transport: hangTransport{}}
	repo := newFollowRepo(uuid.New())
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFetchClient(&hang))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.ResolveSearchTarget(ctx, searchVideoURL)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("resolution took %v, want prompt return after the 50ms deadline", elapsed)
	}
}

// hangTransport blocks every request until its context is cancelled.
type hangTransport struct{}

func (hangTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestNoRawHTTPFetchInFederation is the W13 spec's static assertion: every
// outbound fetch in this package goes through the SSRF-guarded client
// (urlsafety.Guard / the injected fetchClient) — never a raw http.Get/Post
// with the default transport.
func TestNoRawHTTPFetchInFederation(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, forbidden := range []string{"http.Get(", "http.Post(", "http.DefaultClient"} {
			if strings.Contains(string(src), forbidden) {
				t.Errorf("%s uses %s — outbound fetches must go through the SSRF-guarded client", name, forbidden)
			}
		}
	}
}

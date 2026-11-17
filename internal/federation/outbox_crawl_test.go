package federation

// The origin here is a PeerTube as the crawl sees one. Its actor, activity and
// video documents are the inbound corpus; the outbox and its pages are shaped
// as the interop lab's PeerTube v8.3.2 served them: an OrderedCollection
// naming its first page, then pages of Announce activities whose object is a
// video URL.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// fakeOrigin serves documents by exact URL and records every request.
type fakeOrigin struct {
	docs map[string][]byte
	seen *[]string
}

func (f fakeOrigin) RoundTrip(req *http.Request) (*http.Response, error) {
	*f.seen = append(*f.seen, req.URL.String())
	body, ok := f.docs[req.URL.String()]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func requestsTo(seen []string, prefix string) (n int) {
	for _, u := range seen {
		if strings.HasPrefix(u, prefix) {
			n++
		}
	}
	return n
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// peertubeOrigin is the fixture PeerTube with one public video, listed on page
// 1 of the outbox of `actor`: its channel, or the instance actor.
func peertubeOrigin(t *testing.T, actor string) map[string][]byte {
	t.Helper()
	outbox := actor + "/outbox"
	return map[string][]byte{
		ctPTVideo:   readFixture(t, "inbound/peertube_video_object.json"),
		ctPTChannel: readFixture(t, "inbound/peertube_actor_group.json"),
		ctPTAccount: readFixture(t, "inbound/peertube_actor_person.json"),
		ctPTInstance: mustJSON(t, map[string]any{
			"id": ctPTInstance, "type": "Application", "preferredUsername": "peertube", "inbox": ctPTInstance + "/inbox",
			"outbox": ctPTInstance + "/outbox", "publicKey": map[string]any{"publicKeyPem": "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----"},
		}),
		outbox: mustJSON(t, map[string]any{"id": outbox, "type": "OrderedCollection", "totalItems": 1, "first": outbox + "?page=1"}),
		outbox + "?page=1": mustJSON(t, map[string]any{
			"id": outbox + "?page=1", "type": "OrderedCollectionPage", "partOf": outbox,
			"orderedItems": []json.RawMessage{asSentBy(t, "inbound/peertube_announce_video.json", actor)},
		}),
	}
}

// newOutboxCrawl is a vidra that follows each of `followed` and reaches the
// origin through docs.
func newOutboxCrawl(docs map[string][]byte, followed ...string) (*Service, fakeRepo, *[]string) {
	repo, seen := newContractRepo(), &[]string{}
	return NewService(repo, WithBaseURL(contractBase), WithFollowEdgeChecker(edgesFor(followed...)),
		WithFetchClient(&http.Client{Transport: fakeOrigin{docs: docs, seen: seen}})), repo, seen
}

// The video the origin published before the follow is stored through the same
// ingest a delivery goes through: filed under its channel, whose document
// names the account that owns it. A second crawl stores nothing and does not
// fetch the video again.
func TestCrawlOutboxBackfillsAFollowedActor(t *testing.T) {
	for name, actor := range map[string]string{"a channel": ctPTChannel, "an instance": ctPTInstance} {
		t.Run(name, func(t *testing.T) {
			svc, repo, seen := newOutboxCrawl(peertubeOrigin(t, actor), actor)
			if stored, err := svc.CrawlOutbox(context.Background(), actor); err != nil || stored != 1 {
				t.Fatalf("CrawlOutbox = %d, %v; want 1 stored", stored, err)
			}
			assertFiledUnderChannel(t, repo)

			fetched := requestsTo(*seen, ctPTVideo)
			if stored, err := svc.CrawlOutbox(context.Background(), actor); err != nil || stored != 0 {
				t.Errorf("second CrawlOutbox = %d, %v; want nothing stored", stored, err)
			}
			if again := requestsTo(*seen, ctPTVideo) - fetched; again != 0 || len(repo.remoteVideos) != 1 {
				t.Errorf("second crawl fetched the held video %d more time(s) and left %d rows", again, len(repo.remoteVideos))
			}
		})
	}
}

// Nothing is read from an actor nobody here follows, from a blocked instance,
// or from an address the SSRF guard refuses.
func TestCrawlOutboxReadsOnlyWhatWasAskedFor(t *testing.T) {
	const private = "https://10.0.0.5/video-channels/kayak_diaries"
	for _, tc := range []struct {
		name, actor string
		followed    []string
		blocked     bool
		wantErr     bool
	}{
		{name: "not followed", actor: ctPTChannel},
		{name: "blocked instance", actor: ctPTChannel, followed: []string{ctPTChannel}, blocked: true},
		{name: "private address", actor: private, followed: []string{private}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, seen := newOutboxCrawl(peertubeOrigin(t, ctPTChannel), tc.followed...)
			repo.blockedDomains["peertube.example"] = tc.blocked
			stored, err := svc.CrawlOutbox(context.Background(), tc.actor)
			if stored != 0 || (err != nil) != tc.wantErr {
				t.Fatalf("CrawlOutbox = %d, %v; want 0, error %v", stored, err, tc.wantErr)
			}
			if len(*seen) != 0 || len(repo.remoteVideos) != 0 {
				t.Errorf("requests %v, rows %d; want none", *seen, len(repo.remoteVideos))
			}
		})
	}
}

// Everything the crawl reads and stores lives on the followed actor's host,
// and only what that actor itself did is taken.
func TestCrawlOutboxStaysOnTheActorsHost(t *testing.T) {
	const outbox, elsewhere = ctPTChannel + "/outbox", "https://elsewhere.example"
	page := func(next, actor, object string) []byte {
		p := map[string]any{"type": "OrderedCollectionPage", "orderedItems": []any{}}
		if object != "" {
			p["orderedItems"] = []any{map[string]any{"id": object + "/announces/1", "type": "Announce", "actor": actor, "object": object}}
		}
		if next != "" {
			p["next"] = next
		}
		return mustJSON(t, p)
	}
	for name, mutate := range map[string]func(docs map[string][]byte){
		"an object on another host":    func(d map[string][]byte) { d[outbox+"?page=1"] = page("", ctPTChannel, elsewhere+"/videos/watch/1") },
		"a next page on another host":  func(d map[string][]byte) { d[outbox+"?page=1"] = page(elsewhere+"/outbox?page=2", "", "") },
		"an activity by another actor": func(d map[string][]byte) { d[outbox+"?page=1"] = page("", ctPTAccount, ctPTVideo) },
		"an outbox on another host": func(d map[string][]byte) {
			d[ctPTChannel] = bytes.ReplaceAll(d[ctPTChannel], []byte(`"`+outbox+`"`), []byte(`"`+elsewhere+`/outbox"`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			docs := peertubeOrigin(t, ctPTChannel)
			mutate(docs)
			svc, repo, seen := newOutboxCrawl(docs, ctPTChannel)
			if stored, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil || stored != 0 {
				t.Fatalf("CrawlOutbox = %d, %v; want nothing stored", stored, err)
			}
			if n := requestsTo(*seen, elsewhere) + requestsTo(*seen, ctPTVideo); n != 0 || len(repo.remoteVideos) != 0 {
				t.Errorf("requests %v, rows %d; want nothing fetched but the actor's own outbox", *seen, len(repo.remoteVideos))
			}
			if requestsTo(*seen, ctPTChannel) == 0 {
				t.Error("the crawl read nothing at all, so the case proved nothing")
			}
		})
	}
}

// An outbox whose every page names another is read for a fixed number of pages.
func TestCrawlOutboxIsBounded(t *testing.T) {
	const outbox = ctPTChannel + "/outbox"
	docs := peertubeOrigin(t, ctPTChannel)
	for n := 1; n <= 4*outboxCrawlMaxPages; n++ {
		docs[outbox+"?page="+strconv.Itoa(n)] = mustJSON(t, map[string]any{"orderedItems": []any{}, "next": outbox + "?page=" + strconv.Itoa(n+1)})
	}
	svc, _, seen := newOutboxCrawl(docs, ctPTChannel)
	if _, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil {
		t.Fatalf("CrawlOutbox: %v", err)
	}
	if got := requestsTo(*seen, outbox); got != 1+outboxCrawlMaxPages {
		t.Errorf("read the outbox and %d pages, want the outbox and %d", got-1, outboxCrawlMaxPages)
	}
}

package federation

// A remote server pushes only what it publishes after a follow is accepted.
// What it published before reaches a follower only if the follower reads the
// followed actor's outbox, which PeerTube does straight after the Accept and
// vidra did not: the interop lab staged a public video on PeerTube before a
// channel follow and before an instance follow, and neither ever arrived
// (meta docs/evidence/peertube-interop-2026-11-17-gaps.md).
//
// The origin here is a PeerTube as the crawl sees one. Its actor, activity and
// video documents are the inbound corpus; the outbox and its pages are shaped
// as the lab's PeerTube v8.3.2 served them: an OrderedCollection naming its
// first page, then pages of Announce activities whose object is a video URL.

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
		status, body = http.StatusNotFound, []byte("no document for "+req.URL.String())
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)),
		Header: http.Header{"Content-Type": {"application/activity+json"}}, Request: req,
	}, nil
}

func requestsTo(seen []string, prefix string) int {
	n := 0
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

// peertubeOrigin is the fixture PeerTube with one public video, listed in the
// outbox of `actor` (its channel, or the instance actor) on page 1.
func peertubeOrigin(t *testing.T, actor string) map[string][]byte {
	t.Helper()
	outbox := actor + "/outbox"
	docs := map[string][]byte{
		ctPTVideo:   readFixture(t, "inbound/peertube_video_object.json"),
		ctPTChannel: readFixture(t, "inbound/peertube_actor_group.json"),
		ctPTAccount: readFixture(t, "inbound/peertube_actor_person.json"),
		outbox:      mustJSON(t, map[string]any{"id": outbox, "type": "OrderedCollection", "totalItems": 1, "first": outbox + "?page=1"}),
		outbox + "?page=1": mustJSON(t, map[string]any{
			"id": outbox + "?page=1", "type": "OrderedCollectionPage", "partOf": outbox, "totalItems": 1,
			"orderedItems": []json.RawMessage{asSentBy(t, "inbound/peertube_announce_video.json", actor)},
		}),
	}
	if actor == ctPTInstance {
		docs[ctPTInstance] = mustJSON(t, map[string]any{
			"id": ctPTInstance, "type": "Application", "preferredUsername": "peertube",
			"inbox": ctPTInstance + "/inbox", "outbox": outbox,
			"publicKey": map[string]any{"publicKeyPem": "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----"},
		})
	}
	return docs
}

// newOutboxCrawl is a vidra that follows each of `followed` and reaches the
// origin through docs.
func newOutboxCrawl(docs map[string][]byte, followed ...string) (*Service, fakeRepo, *[]string) {
	repo := newContractRepo()
	seen := &[]string{}
	svc := NewService(repo,
		WithBaseURL(contractBase),
		WithFollowEdgeChecker(edgesFor(followed...)),
		WithFetchClient(&http.Client{Transport: fakeOrigin{docs: docs, seen: seen}}),
	)
	return svc, repo, seen
}

// The video the origin published before the follow is stored, through the same
// ingest a delivery goes through: filed under its channel, whose document
// names the account that owns it. A second crawl stores nothing and does not
// fetch the video again.
func TestCrawlOutboxBackfillsAFollowedActor(t *testing.T) {
	for _, tc := range []struct{ name, actor string }{
		{"a channel", ctPTChannel},
		{"an instance", ctPTInstance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, seen := newOutboxCrawl(peertubeOrigin(t, tc.actor), tc.actor)
			stored, err := svc.CrawlOutbox(context.Background(), tc.actor)
			if err != nil || stored != 1 {
				t.Fatalf("CrawlOutbox = %d, %v; want 1 stored", stored, err)
			}
			assertFiledUnderChannel(t, repo)

			fetched := requestsTo(*seen, ctPTVideo)
			stored, err = svc.CrawlOutbox(context.Background(), tc.actor)
			if err != nil || stored != 0 {
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
	t.Run("not followed", func(t *testing.T) {
		svc, repo, seen := newOutboxCrawl(peertubeOrigin(t, ctPTChannel))
		if stored, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil || stored != 0 {
			t.Fatalf("CrawlOutbox = %d, %v", stored, err)
		}
		if len(*seen) != 0 || len(repo.remoteVideos) != 0 {
			t.Errorf("requests %v, rows %d; want none", *seen, len(repo.remoteVideos))
		}
	})
	t.Run("blocked instance", func(t *testing.T) {
		svc, repo, seen := newOutboxCrawl(peertubeOrigin(t, ctPTChannel), ctPTChannel)
		repo.blockedDomains["peertube.example"] = true
		if stored, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil || stored != 0 {
			t.Fatalf("CrawlOutbox = %d, %v", stored, err)
		}
		if len(*seen) != 0 || len(repo.remoteVideos) != 0 {
			t.Errorf("requests %v, rows %d; want none", *seen, len(repo.remoteVideos))
		}
	})
	t.Run("private address", func(t *testing.T) {
		const private = "https://10.0.0.5/video-channels/kayak_diaries"
		svc, _, seen := newOutboxCrawl(peertubeOrigin(t, ctPTChannel), private)
		if _, err := svc.CrawlOutbox(context.Background(), private); err == nil {
			t.Error("CrawlOutbox of a private address returned no error")
		}
		if len(*seen) != 0 {
			t.Errorf("requests %v; want none", *seen)
		}
	})
}

// Everything the crawl reads and stores lives on the followed actor's host,
// and only what that actor itself did is taken.
func TestCrawlOutboxStaysOnTheActorsHost(t *testing.T) {
	const outbox = ctPTChannel + "/outbox"
	elsewhere := "https://elsewhere.example"
	announce := func(actor, object string) json.RawMessage {
		return mustJSON(t, map[string]any{"id": object + "/announces/1", "type": "Announce", "actor": actor, "object": object})
	}
	page := func(next string, items ...json.RawMessage) []byte {
		p := map[string]any{"id": outbox + "?page=1", "type": "OrderedCollectionPage", "orderedItems": items}
		if next != "" {
			p["next"] = next
		}
		return mustJSON(t, p)
	}
	for _, tc := range []struct {
		name   string
		mutate func(docs map[string][]byte)
	}{
		{"an object on another host", func(docs map[string][]byte) {
			docs[outbox+"?page=1"] = page("", announce(ctPTChannel, elsewhere+"/videos/watch/1"))
		}},
		{"a next page on another host", func(docs map[string][]byte) {
			docs[outbox+"?page=1"] = page(elsewhere + "/outbox?page=2")
		}},
		{"an outbox on another host", func(docs map[string][]byte) {
			docs[ctPTChannel] = bytes.ReplaceAll(docs[ctPTChannel], []byte(`"`+outbox+`"`), []byte(`"`+elsewhere+`/outbox"`))
		}},
		{"an activity by another actor", func(docs map[string][]byte) {
			docs[outbox+"?page=1"] = page("", announce(ctPTAccount, ctPTVideo))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := peertubeOrigin(t, ctPTChannel)
			tc.mutate(docs)
			svc, repo, seen := newOutboxCrawl(docs, ctPTChannel)
			if stored, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil || stored != 0 {
				t.Fatalf("CrawlOutbox = %d, %v; want nothing stored", stored, err)
			}
			if n := requestsTo(*seen, elsewhere) + requestsTo(*seen, ctPTVideo); n != 0 || len(repo.remoteVideos) != 0 {
				t.Errorf("requests %v, rows %d; want nothing fetched off the actor's own activities", *seen, len(repo.remoteVideos))
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
		docs[outbox+"?page="+strconv.Itoa(n)] = mustJSON(t, map[string]any{
			"type": "OrderedCollectionPage", "orderedItems": []any{}, "next": outbox + "?page=" + strconv.Itoa(n+1),
		})
	}
	svc, _, seen := newOutboxCrawl(docs, ctPTChannel)
	if _, err := svc.CrawlOutbox(context.Background(), ctPTChannel); err != nil {
		t.Fatalf("CrawlOutbox: %v", err)
	}
	if got := requestsTo(*seen, outbox); got != 1+outboxCrawlMaxPages {
		t.Errorf("read the outbox and %d pages, want the outbox and %d", got-1, outboxCrawlMaxPages)
	}
}

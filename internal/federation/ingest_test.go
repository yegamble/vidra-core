package federation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeEdges is an in-memory FollowEdgeChecker.
type fakeEdges struct{ has map[string]bool }

func (f fakeEdges) HasAcceptedFollow(_ context.Context, actor string) (bool, error) {
	return f.has[actor], nil
}

func edgesFor(actors ...string) fakeEdges {
	m := map[string]bool{}
	for _, a := range actors {
		m[a] = true
	}
	return fakeEdges{has: m}
}

func newIngestRepo() fakeRepo {
	r := newInboxRepo()
	r.remoteVideos = map[string]*fakeRemoteVideo{}
	r.remoteActors = map[string]sqlcgen.RemoteActor{}
	r.blockedDomains = map[string]bool{}
	return r
}

const (
	remoteChan  = "https://remote.example/video-channels/movies"
	remoteVideo = "https://remote.example/videos/watch/abc"
)

// createActivity renders a Create{Video} with the given activity id, actor,
// object id, attribution, and title.
func createActivity(actID, actor, objectID, attributedTo, title string) string {
	return `{"id":"` + actID + `","type":"Create","actor":"` + actor + `","object":{` +
		`"id":"` + objectID + `","type":"Video","name":` + quote(title) + `,` +
		`"content":"a description","attributedTo":"` + attributedTo + `",` +
		`"published":"2026-06-30T12:00:00Z","duration":"PT90S",` +
		`"url":[{"type":"Link","mediaType":"text/html","href":"` + objectID + `"},` +
		`{"type":"Link","mediaType":"application/x-mpegURL","href":"https://remote.example/hls/abc.m3u8"},` +
		`{"type":"Link","mediaType":"video/mp4","href":"https://remote.example/dl/abc.mp4"}]}}`
}

func TestHandleInboxCreateVideoStores(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))

	longTitle := strings.Repeat("t", 600)
	body := createActivity("https://remote.example/act/c1", remoteChan, remoteVideo, remoteChan, longTitle)
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	rv, ok := repo.remoteVideos[remoteVideo]
	if !ok {
		t.Fatalf("remote video not stored: %+v", repo.remoteVideos)
	}
	p := rv.params
	if p.RemoteActorUrl != remoteChan {
		t.Errorf("remote_actor_url = %q, want %q", p.RemoteActorUrl, remoteChan)
	}
	if len([]rune(p.Title)) != maxRemoteTitleLen {
		t.Errorf("title length = %d, want truncated to %d", len([]rune(p.Title)), maxRemoteTitleLen)
	}
	if p.Description != "a description" {
		t.Errorf("description = %q", p.Description)
	}
	if p.DurationSeconds == nil || *p.DurationSeconds != 90 {
		t.Errorf("duration = %v, want 90", p.DurationSeconds)
	}
	if !p.PublishedAt.Valid || !p.PublishedAt.Time.Equal(time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("published_at = %+v", p.PublishedAt)
	}
	if p.WatchUrl != remoteVideo {
		t.Errorf("watch_url = %q, want %q", p.WatchUrl, remoteVideo)
	}
	if p.StreamUrl == nil || *p.StreamUrl != "https://remote.example/hls/abc.m3u8" {
		t.Errorf("stream_url = %v, want the HLS link preferred", p.StreamUrl)
	}
	if !repo.processed["https://remote.example/act/c1"] {
		t.Error("activity not marked processed")
	}
}

func TestHandleInboxCreateVideoNoFollowEdgeIgnored(t *testing.T) {
	repo := newIngestRepo()
	// No edge checker wired at all — the next-slice table doesn't exist yet.
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	body := createActivity("https://remote.example/act/c2", remoteChan, remoteVideo, remoteChan, "Clip")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("a video was ingested without an accepted local follow edge")
	}
	if !repo.processed["https://remote.example/act/c2"] {
		t.Error("accept-and-ignore should still mark the activity processed")
	}
}

func TestHandleInboxCreateVideoActorMismatch(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	body := createActivity("https://remote.example/act/c3", remoteChan, remoteVideo, remoteChan, "Clip")
	// Signed by eve, activity claims the channel actor.
	err := svc.HandleInbox(context.Background(), "https://remote.example/accounts/eve", []byte(body))
	if !errors.Is(err, ErrActorMismatch) {
		t.Errorf("err = %v, want ErrActorMismatch", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("video stored despite actor mismatch")
	}
}

func TestHandleInboxCreateVideoForeignAttributionIgnored(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	// Object attributed to someone other than the signer → accept-and-ignore.
	body := createActivity("https://remote.example/act/c4", remoteChan, remoteVideo,
		"https://remote.example/accounts/other", "Clip")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("video stored despite foreign attribution")
	}
}

func TestHandleInboxCreateVideoCrossOriginObjectIgnored(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	// Object id lives on a different origin than the signer → accept-and-ignore.
	body := createActivity("https://remote.example/act/c5", remoteChan,
		"https://elsewhere.example/videos/watch/x", remoteChan, "Clip")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("video stored despite cross-origin object id")
	}
}

func TestHandleInboxCreateVideoIdempotentUpsert(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))

	first := createActivity("https://remote.example/act/c6", remoteChan, remoteVideo, remoteChan, "Old title")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(first)); err != nil {
		t.Fatalf("first HandleInbox: %v", err)
	}
	id1 := repo.remoteVideos[remoteVideo].id

	// A re-delivery with the SAME activity id is deduped entirely.
	dup := createActivity("https://remote.example/act/c6", remoteChan, remoteVideo, remoteChan, "Dup title")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(dup)); err != nil {
		t.Fatalf("dup HandleInbox: %v", err)
	}
	if got := repo.remoteVideos[remoteVideo].params.Title; got != "Old title" {
		t.Errorf("deduped activity mutated the row: title = %q", got)
	}

	// A NEW activity for the same object upserts in place (same row id).
	second := createActivity("https://remote.example/act/c7", remoteChan, remoteVideo, remoteChan, "New title")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(second)); err != nil {
		t.Fatalf("second HandleInbox: %v", err)
	}
	if len(repo.remoteVideos) != 1 {
		t.Fatalf("videos = %d, want 1 (upsert by object_url)", len(repo.remoteVideos))
	}
	if repo.remoteVideos[remoteVideo].id != id1 {
		t.Error("upsert minted a new row id for the same object_url")
	}
	if got := repo.remoteVideos[remoteVideo].params.Title; got != "New title" {
		t.Errorf("title = %q, want %q", got, "New title")
	}
}

// objectOrigin serves an AP Video object document (and counts hits) the way a
// remote origin would for an Announce'd object URL.
func objectOrigin(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		origin := "http://" + r.Host
		w.Header().Set("Content-Type", "application/activity+json")
		_, _ = w.Write([]byte(`{
			"id": "` + origin + `/videos/watch/1",
			"type": "Video",
			"name": "Announced clip",
			"content": "from the origin",
			"attributedTo": "` + origin + `/video-channels/movies",
			"duration": "PT2M3S",
			"url": [{"type":"Link","mediaType":"video/mp4","href":"` + origin + `/dl/1.mp4"}]
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestHandleInboxAnnounceFetchesObject(t *testing.T) {
	origin, hits := objectOrigin(t)
	signer := origin.URL + "/video-channels/movies"
	repo := newIngestRepo()
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithFollowEdgeChecker(edgesFor(signer)),
		WithAllowPrivateFetch(true), // the guard must permit the loopback origin
	)

	announce := `{"id":"` + origin.URL + `/act/a1","type":"Announce","actor":"` + signer +
		`","object":"` + origin.URL + `/videos/watch/1"}`
	if err := svc.HandleInbox(context.Background(), signer, []byte(announce)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if *hits != 1 {
		t.Fatalf("origin hits = %d, want 1 (object fetched)", *hits)
	}
	rv, ok := repo.remoteVideos[origin.URL+"/videos/watch/1"]
	if !ok {
		t.Fatalf("announced video not stored: %+v", repo.remoteVideos)
	}
	if rv.params.Title != "Announced clip" {
		t.Errorf("title = %q", rv.params.Title)
	}
	if rv.params.RemoteActorUrl != signer {
		t.Errorf("remote_actor_url = %q, want the attributed channel %q", rv.params.RemoteActorUrl, signer)
	}
	if rv.params.DurationSeconds == nil || *rv.params.DurationSeconds != 123 {
		t.Errorf("duration = %v, want 123 (PT2M3S)", rv.params.DurationSeconds)
	}
	if rv.params.StreamUrl == nil || !strings.HasSuffix(*rv.params.StreamUrl, "/dl/1.mp4") {
		t.Errorf("stream_url = %v", rv.params.StreamUrl)
	}
}

func TestHandleInboxAnnounceForeignObjectIgnored(t *testing.T) {
	origin, hits := objectOrigin(t)
	signer := origin.URL + "/video-channels/movies"
	repo := newIngestRepo()
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithFollowEdgeChecker(edgesFor(signer)),
		WithAllowPrivateFetch(true),
	)
	// Announce of an object on ANOTHER origin → ignored, never fetched.
	announce := `{"id":"` + origin.URL + `/act/a2","type":"Announce","actor":"` + signer +
		`","object":"https://elsewhere.example/videos/watch/9"}`
	if err := svc.HandleInbox(context.Background(), signer, []byte(announce)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if *hits != 0 {
		t.Errorf("origin hits = %d, want 0 (no proxy-fetch of foreign objects)", *hits)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("foreign announce was stored")
	}
}

func TestHandleInboxAnnounceNoEdgeDoesNotFetch(t *testing.T) {
	origin, hits := objectOrigin(t)
	signer := origin.URL + "/video-channels/movies"
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	announce := `{"id":"` + origin.URL + `/act/a3","type":"Announce","actor":"` + signer +
		`","object":"` + origin.URL + `/videos/watch/1"}`
	if err := svc.HandleInbox(context.Background(), signer, []byte(announce)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if *hits != 0 {
		t.Errorf("origin hits = %d, want 0 (gate before fetch)", *hits)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("announce ingested without a follow edge")
	}
}

func TestHandleInboxBlockedDomainDropped(t *testing.T) {
	repo := newIngestRepo()
	repo.blockedDomains["remote.example"] = true
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	// A Follow that would otherwise be recorded is dropped silently (202, no
	// dispatch) and NOT marked processed, so an unblock lets a redelivery in.
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Error("activity from a blocked domain was dispatched")
	}
	if repo.processed[followID] {
		t.Error("dropped activity should not be marked processed")
	}
}

func TestCacheRemoteThumbnail(t *testing.T) {
	img := bytes.Repeat([]byte{0xFF}, 1024)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(img)
	}))
	t.Cleanup(origin.Close)

	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	repo := newIngestRepo()
	signer := origin.URL + "/video-channels/movies"
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithFollowEdgeChecker(edgesFor(signer)),
		WithAllowPrivateFetch(true),
		WithMediaStorage(blobs),
	)

	objectID := origin.URL + "/videos/watch/2"
	body := `{"id":"` + origin.URL + `/act/t1","type":"Create","actor":"` + signer + `","object":{` +
		`"id":"` + objectID + `","type":"Video","name":"With poster","attributedTo":"` + signer + `",` +
		`"icon":[{"type":"Image","mediaType":"image/jpeg","url":"` + origin.URL + `/poster.jpg"}]}}`
	if err := svc.HandleInbox(context.Background(), signer, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	rv := repo.remoteVideos[objectID]
	if rv == nil {
		t.Fatal("video not stored")
	}
	if rv.thumbnailKey == nil {
		t.Fatal("thumbnail key not recorded")
	}
	wantKey := "remote-thumbnails/" + rv.id.String() + ".jpg"
	if *rv.thumbnailKey != wantKey {
		t.Errorf("thumbnail key = %q, want %q", *rv.thumbnailKey, wantKey)
	}
	rc, err := blobs.Open(context.Background(), wantKey)
	if err != nil {
		t.Fatalf("cached thumbnail missing: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, img) {
		t.Errorf("cached bytes differ (len %d vs %d)", len(got), len(img))
	}
}

func TestCacheRemoteThumbnailOversizedSkipped(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(bytes.Repeat([]byte{0x00}, maxThumbnailBytes+1))
	}))
	t.Cleanup(origin.Close)

	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	repo := newIngestRepo()
	signer := origin.URL + "/video-channels/movies"
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithFollowEdgeChecker(edgesFor(signer)),
		WithAllowPrivateFetch(true),
		WithMediaStorage(blobs),
	)
	objectID := origin.URL + "/videos/watch/3"
	body := `{"id":"` + origin.URL + `/act/t2","type":"Create","actor":"` + signer + `","object":{` +
		`"id":"` + objectID + `","type":"Video","name":"Huge poster","attributedTo":"` + signer + `",` +
		`"icon":{"type":"Image","url":"` + origin.URL + `/poster.jpg"}}}`
	if err := svc.HandleInbox(context.Background(), signer, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v (thumbnail failures must be non-fatal)", err)
	}
	rv := repo.remoteVideos[objectID]
	if rv == nil {
		t.Fatal("video not stored (ingestion must survive a thumbnail failure)")
	}
	if rv.thumbnailKey != nil {
		t.Errorf("oversized thumbnail was cached: %q", *rv.thumbnailKey)
	}
}

func TestDrainCancelsBlockedDomainDeliveries(t *testing.T) {
	repo := newIngestRepo()
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	repo.blockedDomains["blocked.example"] = true
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	chID := uuid.New()
	if err := repo.EnqueueDelivery(context.Background(), sqlcgen.EnqueueDeliveryParams{
		InboxUrl:             "https://blocked.example/inbox",
		Payload:              []byte(`{}`),
		SigningChannelID:     pgtype.UUID{Bytes: chID, Valid: true},
		SigningChannelHandle: "films",
	}); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	delivered, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0", delivered)
	}
	for _, d := range repo.deliveries {
		if d.state != "failed" {
			t.Errorf("delivery state = %q, want failed (cancelled)", d.state)
		}
		if !strings.Contains(d.lastError, "cancelled") {
			t.Errorf("last_error = %q, want a cancelled marker", d.lastError)
		}
	}
}

func TestParseISODurationSeconds(t *testing.T) {
	i := func(n int32) *int32 { return &n }
	cases := []struct {
		in   string
		want *int32
	}{
		{"PT90S", i(90)},
		{"PT2M3S", i(123)},
		{"PT1H2M3S", i(3723)},
		{"pt45s", i(45)},
		{"", nil},
		{"PT", nil},
		{"P1D", nil},
		{"PT1.5S", nil},
		{"PT0S", nil},
		{"nonsense", nil},
	}
	for _, c := range cases {
		got := parseISODurationSeconds(c.in)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("parse(%q) = %d, want nil", c.in, *got)
		case c.want != nil && (got == nil || *got != *c.want):
			t.Errorf("parse(%q) = %v, want %d", c.in, got, *c.want)
		}
	}
}

func TestVideoLinks(t *testing.T) {
	// A plain string url is the watch page; no stream.
	watch, stream := videoLinks([]byte(`"https://x.example/watch/1"`))
	if watch != "https://x.example/watch/1" || stream != nil {
		t.Errorf("string url: watch=%q stream=%v", watch, stream)
	}
	// HLS preferred over mp4; html link is the watch page.
	watch, stream = videoLinks([]byte(`[
		{"mediaType":"video/mp4","href":"https://x.example/f.mp4"},
		{"mediaType":"application/x-mpegURL","href":"https://x.example/f.m3u8"},
		{"mediaType":"text/html","href":"https://x.example/watch/1"}]`))
	if watch != "https://x.example/watch/1" {
		t.Errorf("watch = %q", watch)
	}
	if stream == nil || *stream != "https://x.example/f.m3u8" {
		t.Errorf("stream = %v, want the HLS link", stream)
	}
	// mp4 only.
	_, stream = videoLinks([]byte(`[{"mediaType":"video/mp4","href":"https://x.example/f.mp4"}]`))
	if stream == nil || *stream != "https://x.example/f.mp4" {
		t.Errorf("stream = %v, want the mp4 link", stream)
	}
	// Nothing playable.
	watch, stream = videoLinks([]byte(`[{"mediaType":"text/html","href":"https://x.example/w"}]`))
	if watch != "https://x.example/w" || stream != nil {
		t.Errorf("html only: watch=%q stream=%v", watch, stream)
	}
}

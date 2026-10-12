package httpapi

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/video"
)

func TestPodcastFeed(t *testing.T) {
	srv, blobs, tcRepo, _, repo := videoServerFull(t, distributionCfg())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	mk := func(body string) string {
		id := createVideo(t, srv, tok, "ada", body)
		if rec := uploadVideoFile(srv, id, "c.mp4", "video/mp4", "tiny", tok); rec.Code != http.StatusCreated {
			t.Fatalf("upload = %d", rec.Code)
		}
		seedReadyHLS(t, tcRepo, blobs, id)
		return id
	}
	good := mk(`{"title":"Good <ep>","privacy":"public"}`)
	noDL := mk(`{"title":"NoDownload","privacy":"public"}`)
	priv := mk(`{"title":"Private","privacy":"private"}`)
	bare := createVideo(t, srv, tok, "ada", `{"title":"NoMedia","privacy":"public"}`)
	_ = uploadVideoFile(srv, bare, "c.mp4", "video/mp4", "tiny", tok)

	id := uuid.MustParse(noDL)
	row := repo.videos[id]
	row.DownloadEnabled = false
	repo.videos[id] = row

	rec := get(t, srv, "/feeds/podcast/videos.xml?channel=ada")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var doc podcastDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"`,
		`xmlns:podcast="https://podcastindex.org/namespace/1.0"`,
		"<podcast:locked>no</podcast:locked>", "<podcast:guid>", "<itunes:author>", "<itunes:image href=",
		`<enclosure url="https://videos.example/api/v1/videos/` + good + `/download/audio" length="14" type="audio/mp4">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed missing %q:\n%s", want, body)
		}
	}
	if len(doc.Channel.Items) != 1 || doc.Channel.Items[0].Title != "Good <ep>" {
		t.Fatalf("items = %+v, want only the public downloadable video", doc.Channel.Items)
	}
	for _, leak := range []string{noDL, priv, bare} {
		if strings.Contains(body, leak) {
			t.Errorf("feed leaked video %s", leak)
		}
	}
	if strings.Contains(body, "token=") || strings.Contains(body, "X-Amz") {
		t.Errorf("feed carries a credentialed URL:\n%s", body)
	}
	// One batched flag read for the whole page, not a point read per item.
	if repo.downloadLookups != 1 {
		t.Errorf("download-flag lookups per render = %d, want exactly 1 (N+1 fence)", repo.downloadLookups)
	}
	// Instance downloads off: the feed must empty out even though every
	// per-video flag is still on.
	setToggle(t, srv, tok, "downloads_enabled", false)
	off := get(t, srv, "/feeds/podcast/videos.xml?channel=ada")
	var offDoc podcastDocument
	if err := xml.Unmarshal(off.Body.Bytes(), &offDoc); err != nil || len(offDoc.Channel.Items) != 0 {
		t.Errorf("downloads_enabled=false: items = %d (err %v), want 0", len(offDoc.Channel.Items), err)
	}
	setToggle(t, srv, tok, "downloads_enabled", true)
	// Stable guid across renders.
	rec2 := get(t, srv, "/feeds/podcast/videos.xml?videoChannelName=ada")
	var doc2 podcastDocument
	if err := xml.Unmarshal(rec2.Body.Bytes(), &doc2); err != nil || doc2.Channel.GUID != doc.Channel.GUID {
		t.Errorf("podcast:guid unstable: %q vs %q (%v)", doc.Channel.GUID, doc2.Channel.GUID, err)
	}
	// The enclosure is actually fetchable anonymously.
	if r := get(t, srv, "/api/v1/videos/"+good+"/download/audio"); r.Code != http.StatusOK {
		t.Errorf("enclosure fetch = %d, want 200", r.Code)
	}
}

func TestPodcastFeedNeedsOneChannel(t *testing.T) {
	srv, _, _ := distributionServer(t)
	for _, p := range []string{"/feeds/podcast/videos.xml", "/feeds/podcast/videos.xml?channel=nobody", "/feeds/podcast/videos.xml?videoChannelId=1"} {
		if rec := get(t, srv, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

func TestPodcastFeedHidesSensitiveUnderHidePolicy(t *testing.T) {
	srv, blobs, tcRepo, _, repo := videoServerFull(t, distributionCfg())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Spicy","privacy":"public","is_sensitive":true}`)
	if rec := uploadVideoFile(srv, id, "c.mp4", "video/mp4", "tiny", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d", rec.Code)
	}
	seedReadyHLS(t, tcRepo, blobs, id)
	_ = repo
	count := func() int {
		var d podcastDocument
		_ = xml.Unmarshal(get(t, srv, "/feeds/podcast/videos.xml?channel=ada").Body.Bytes(), &d)
		return len(d.Channel.Items)
	}
	applySensitivePolicy(t, srv, "display")
	if count() != 1 {
		t.Fatal("display policy: flagged episode should be listed")
	}
	applySensitivePolicy(t, srv, "hide")
	if count() != 0 {
		t.Error("hide policy: flagged episode leaked into the podcast feed")
	}
}

// One episode whose storage probe errors is dropped; the show still renders.
func TestPodcastFeedToleratesPerItemStorageError(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFull(t, distributionCfg())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	mk := func(title string) string {
		id := createVideo(t, srv, tok, "ada", `{"title":"`+title+`","privacy":"public"}`)
		_ = uploadVideoFile(srv, id, "c.mp4", "video/mp4", "tiny", tok)
		seedReadyHLS(t, tcRepo, blobs, id)
		return id
	}
	mk("Fine")
	bad := mk("Broken")
	// A traversal key makes the local backend's Path() fail: an error, not "absent".
	bid := uuid.MustParse(bad)
	pl := tcRepo.playlists[bid]
	pl.MasterKey = "../../escape/master.m3u8"
	tcRepo.playlists[bid] = pl

	rec := get(t, srv, "/feeds/podcast/videos.xml?channel=ada")
	if rec.Code != http.StatusOK {
		t.Fatalf("one broken item turned the feed into %d", rec.Code)
	}
	var d podcastDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &d); err != nil || len(d.Channel.Items) != 1 || d.Channel.Items[0].Title != "Fine" {
		t.Errorf("items = %+v (err %v), want only Fine", d.Channel.Items, err)
	}
}

func TestPodcastFeedIsRateLimited(t *testing.T) {
	cfg := distributionCfg()
	srv := New(cfg, nil, nil,
		WithRateLimiter(ratelimit.NewLimiter(&fakeCounter{}, 1, time.Minute)),
		WithVideoService(video.NewService(nil, nil)),
		WithChannelService(channel.NewService(nil)),
	)
	codes := make([]int, 0, 2)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/podcast/videos.xml", nil))
		codes = append(codes, rec.Code)
	}
	if codes[1] != http.StatusTooManyRequests {
		t.Errorf("second request = %d (first %d), want 429", codes[1], codes[0])
	}
}

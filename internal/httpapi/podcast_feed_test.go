package httpapi

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
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

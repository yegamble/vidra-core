package httpapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// distributionCfg is testConfig with a public origin set, which is what mounts
// the Wave E distribution routes (the drift guard's testConfig leaves it empty).
func distributionCfg() *config.Config {
	cfg := testConfig()
	cfg.PublicBaseURL = "https://videos.example"
	return cfg
}

// distributionServer builds the full test server with a public origin plus a
// channel "ada", and returns the server, the video fake repo (for direct
// seeding), and the resolved channel.
func distributionServer(t *testing.T) (*Server, *videoFakeRepo, sqlcgen.Channel) {
	t.Helper()
	srv, _, _, _, repo := videoServerFull(t, distributionCfg())
	createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	ada, err := repo.channels.GetChannelByHandle(context.Background(), "ada")
	if err != nil {
		t.Fatalf("resolve channel ada: %v", err)
	}
	return srv, repo, ada
}

// seedVideo inserts a video straight into the fake repo so a test controls the
// title, privacy tier, state, and recency exactly. Newer videos take a smaller
// ageMinutes (the discovery queries order newest-first).
func seedVideo(repo *videoFakeRepo, ch sqlcgen.Channel, title, desc, privacy, state string, ageMinutes int) uuid.UUID {
	id := uuid.New()
	ts := time.Now().Add(-time.Duration(ageMinutes) * time.Minute).UTC()
	repo.videos[id] = sqlcgen.GetVideoByIDRow{
		ID:          id,
		ChannelID:   ch.ID,
		OwnerID:     ch.OwnerID,
		Title:       title,
		Description: desc,
		Privacy:     privacy,
		State:       state,
		CreatedAt:   ts,
		UpdatedAt:   ts,
	}
	return id
}

func seedThumbnail(repo *videoFakeRepo, id uuid.UUID) {
	repo.files[id] = append(repo.files[id], sqlcgen.VideoFile{
		VideoID: id, Kind: "thumbnail", CreatedAt: time.Now(),
	})
}

// ---------------------------------------------------------------------------
// E1 — RSS feed
// ---------------------------------------------------------------------------

func TestVideosFeedPublicPublishedLocalOnly(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	pub1 := seedVideo(repo, ada, "First Public", "desc one", "public", "published", 10)
	seedThumbnail(repo, pub1)
	pub2 := seedVideo(repo, ada, "Second Public", "desc two", "public", "published", 20)
	seedVideo(repo, ada, "Secret", "hidden", "private", "published", 5)
	seedVideo(repo, ada, "Locked", "hidden", "password", "published", 5)
	seedVideo(repo, ada, "Unlisted", "hidden", "unlisted", "published", 5)
	seedVideo(repo, ada, "Draft", "hidden", "public", "draft", 5)

	rec := get(t, srv, "/feeds/videos.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeRSS {
		t.Errorf("content-type = %q, want %q", ct, contentTypeRSS)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, s-maxage=300" {
		t.Errorf("cache-control = %q, want public, s-maxage=300", cc)
	}

	// Round-trip unmarshal proves the document is well-formed. Go's encoding/xml
	// cannot map its own prefixed output (xmlns:media, atom:link, media:thumbnail)
	// back onto these structs, so the namespaced surface is asserted against the
	// raw body instead — that is what real RSS/MRSS consumers actually read.
	var doc rssDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}
	body := rec.Body.String()
	if doc.Version != "2.0" {
		t.Errorf("rss version = %q, want 2.0", doc.Version)
	}
	if !strings.Contains(body, `xmlns:media="`+mediaRSSNamespace+`"`) ||
		!strings.Contains(body, `xmlns:atom="`+atomNamespace+`"`) {
		t.Errorf("missing MRSS/atom namespace declarations:\n%s", body)
	}
	if !strings.Contains(body, `<atom:link href="https://videos.example/feeds/videos.xml" rel="self" type="application/rss+xml">`) {
		t.Errorf("missing atom:link rel=self:\n%s", body)
	}
	if len(doc.Channel.Items) != 2 {
		t.Fatalf("item count = %d, want 2 (public+published only); items=%+v", len(doc.Channel.Items), doc.Channel.Items)
	}
	// Newest first.
	if doc.Channel.Items[0].Title != "First Public" || doc.Channel.Items[1].Title != "Second Public" {
		t.Errorf("titles/order = %q, %q", doc.Channel.Items[0].Title, doc.Channel.Items[1].Title)
	}
	// Excluded videos never leak into the raw body.
	for _, bad := range []string{"Secret", "Locked", "Unlisted", "Draft"} {
		if strings.Contains(body, bad) {
			t.Errorf("feed leaked a non-public video title %q", bad)
		}
	}
	it0 := doc.Channel.Items[0]
	wantLink := "https://videos.example/videos/" + pub1.String()
	if it0.Link != wantLink {
		t.Errorf("item link = %q, want %q", it0.Link, wantLink)
	}
	if it0.GUID.Value != wantLink || !it0.GUID.IsPermaLink {
		t.Errorf("item guid = %+v, want permalink %q", it0.GUID, wantLink)
	}
	// Exactly one item (pub1) carries a media:thumbnail; the poster-less video omits it.
	wantThumb := `<media:thumbnail url="https://videos.example/api/v1/videos/` + pub1.String() + `/thumbnail">`
	if !strings.Contains(body, wantThumb) {
		t.Errorf("missing media:thumbnail for pub1:\n%s", body)
	}
	if n := strings.Count(body, "<media:thumbnail"); n != 1 {
		t.Errorf("media:thumbnail count = %d, want 1 (only the video with a poster)", n)
	}
	_ = pub2
}

func TestVideosFeedEscapesAttackerControlledText(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	title := `Pwned <script>alert("xss")</script> & <b>bold</b>`
	desc := `break out ]]> < & " '`
	seedVideo(repo, ada, title, desc, "public", "published", 1)

	rec := get(t, srv, "/feeds/videos.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("raw <script> present — XML escaping failed:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped &lt;script&gt; in body:\n%s", body)
	}
	// Round-trip proves well-formedness and that escaping is lossless.
	var doc rssDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if len(doc.Channel.Items) != 1 || doc.Channel.Items[0].Title != title {
		t.Errorf("round-trip title = %q, want %q", doc.Channel.Items[0].Title, title)
	}
}

func TestVideosFeedChannelFilter(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	// Give ada a distinct display name so the feed title proves it uses the
	// display name, not the handle.
	ada.DisplayName = "Ada's Channel"
	repo.channels.byHandle["ada"] = ada
	createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	bob, err := repo.channels.GetChannelByHandle(context.Background(), "bob")
	if err != nil {
		t.Fatalf("resolve bob: %v", err)
	}
	seedVideo(repo, ada, "Ada Clip", "d", "public", "published", 10)
	seedVideo(repo, bob, "Bob Clip", "d", "public", "published", 10)

	rec := get(t, srv, "/feeds/videos.xml?channel=ada")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var doc rssDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	body := rec.Body.String()
	if want := "Ada's Channel — Vidra Test"; doc.Channel.Title != want {
		t.Errorf("channel feed title = %q, want %q", doc.Channel.Title, want)
	}
	if !strings.Contains(body, "<link>https://videos.example/channels/ada</link>") {
		t.Errorf("channel feed <link> not the channel URL:\n%s", body)
	}
	if !strings.Contains(body, `<atom:link href="https://videos.example/feeds/videos.xml?channel=ada" rel="self"`) {
		t.Errorf("self link not the channel-filtered feed URL:\n%s", body)
	}
	if len(doc.Channel.Items) != 1 || doc.Channel.Items[0].Title != "Ada Clip" {
		t.Fatalf("channel filter leaked other channels: %+v", doc.Channel.Items)
	}
	_ = bob
}

func TestVideosFeedUnknownChannelIs404(t *testing.T) {
	srv, _, _ := distributionServer(t)
	rec := get(t, srv, "/feeds/videos.xml?channel=nobody")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown channel", rec.Code)
	}
}

func TestVideosFeedEmptyIsStillValid(t *testing.T) {
	srv, _, _ := distributionServer(t)
	rec := get(t, srv, "/feeds/videos.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty feed status = %d, want 200 (fail-safe)", rec.Code)
	}
	var doc rssDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("empty feed not well-formed: %v", err)
	}
	if len(doc.Channel.Items) != 0 {
		t.Errorf("empty feed items = %d, want 0", len(doc.Channel.Items))
	}
}

// ---------------------------------------------------------------------------
// E2 — oEmbed
// ---------------------------------------------------------------------------

func TestOEmbedGatingAndValidation(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	pub := seedVideo(repo, ada, "Public Clip", "d", "public", "published", 5)
	unlisted := seedVideo(repo, ada, "Unlisted Clip", "d", "unlisted", "published", 5)
	private := seedVideo(repo, ada, "Private Clip", "d", "private", "published", 5)
	locked := seedVideo(repo, ada, "Locked Clip", "d", "password", "published", 5)
	draft := seedVideo(repo, ada, "Draft Clip", "d", "public", "draft", 5)
	missing := uuid.New()

	base := "https://videos.example"
	tests := []struct {
		name string
		url  string
		want int
	}{
		{"public watch url", base + "/videos/" + pub.String(), http.StatusOK},
		{"public embed url", base + "/embed/" + pub.String(), http.StatusOK},
		{"unlisted may embed", base + "/videos/" + unlisted.String(), http.StatusOK},
		{"private is 401", base + "/videos/" + private.String(), http.StatusUnauthorized},
		{"password is 401", base + "/videos/" + locked.String(), http.StatusUnauthorized},
		{"draft is 404", base + "/videos/" + draft.String(), http.StatusNotFound},
		{"unknown video is 404", base + "/videos/" + missing.String(), http.StatusNotFound},
		{"foreign host is 404", "https://evil.example/videos/" + pub.String(), http.StatusNotFound},
		{"lookalike host is 404", "https://videos.example.evil.com/videos/" + pub.String(), http.StatusNotFound},
		{"non-video path is 404", base + "/channels/ada", http.StatusNotFound},
		{"path traversal is 404", base + "/videos/../secret/" + pub.String(), http.StatusNotFound},
		{"non-uuid id is 404", base + "/videos/not-a-uuid", http.StatusNotFound},
		{"javascript scheme is 404", "javascript:alert(1)//videos.example/videos/" + pub.String(), http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, "/services/oembed?format=json&url="+url.QueryEscape(tc.url))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestOEmbedMissingURLIs400(t *testing.T) {
	srv, _, _ := distributionServer(t)
	rec := get(t, srv, "/services/oembed?format=json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing url", rec.Code)
	}
}

func TestOEmbedFormat(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	pub := seedVideo(repo, ada, "Clip", "d", "public", "published", 5)
	u := "https://videos.example/videos/" + pub.String()

	// format=xml → 501 (not implemented).
	if rec := get(t, srv, "/services/oembed?format=xml&url="+url.QueryEscape(u)); rec.Code != http.StatusNotImplemented {
		t.Errorf("format=xml status = %d, want 501", rec.Code)
	}
	// missing format defaults to json.
	if rec := get(t, srv, "/services/oembed?url="+url.QueryEscape(u)); rec.Code != http.StatusOK {
		t.Errorf("missing format status = %d, want 200 (json default)", rec.Code)
	}
}

func TestOEmbedResponseShape(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	ada.DisplayName = "Ada's Channel"
	repo.channels.byHandle["ada"] = ada
	pub := seedVideo(repo, ada, "Great Clip", "d", "public", "published", 5)
	seedThumbnail(repo, pub)

	rec := get(t, srv, "/services/oembed?url="+url.QueryEscape("https://videos.example/videos/"+pub.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var resp oembedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v; body=%s", err, rec.Body.String())
	}
	if resp.Version != "1.0" || resp.Type != "video" {
		t.Errorf("version/type = %q/%q, want 1.0/video", resp.Version, resp.Type)
	}
	if resp.Title != "Great Clip" {
		t.Errorf("title = %q", resp.Title)
	}
	if resp.AuthorName != "Ada's Channel" || resp.AuthorURL != "https://videos.example/channels/ada" {
		t.Errorf("author = %q / %q", resp.AuthorName, resp.AuthorURL)
	}
	if resp.ProviderName != "Vidra Test" || resp.ProviderURL != "https://videos.example" {
		t.Errorf("provider = %q / %q", resp.ProviderName, resp.ProviderURL)
	}
	if resp.Width != 640 || resp.Height != 360 {
		t.Errorf("dims = %dx%d, want 640x360", resp.Width, resp.Height)
	}
	if resp.ThumbnailURL != "https://videos.example/api/v1/videos/"+pub.String()+"/thumbnail" {
		t.Errorf("thumbnail_url = %q", resp.ThumbnailURL)
	}
	wantSrc := "https://videos.example/embed/" + pub.String()
	if !strings.Contains(resp.HTML, wantSrc) {
		t.Errorf("html missing embed src %q: %s", wantSrc, resp.HTML)
	}
	if !strings.Contains(resp.HTML, `width="640"`) || !strings.Contains(resp.HTML, `height="360"`) {
		t.Errorf("html missing dims: %s", resp.HTML)
	}
	// The iframe must not contain a raw unescaped title (built with %q on the URL only).
	if strings.Contains(resp.HTML, "<script") {
		t.Errorf("html contains a script tag: %s", resp.HTML)
	}
}

func TestOEmbedThumbnailOmittedWhenAbsent(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	pub := seedVideo(repo, ada, "No Poster", "d", "public", "published", 5)
	rec := get(t, srv, "/services/oembed?url="+url.QueryEscape("https://videos.example/videos/"+pub.String()))
	var resp oembedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if resp.ThumbnailURL != "" {
		t.Errorf("thumbnail_url = %q, want empty (no poster stored)", resp.ThumbnailURL)
	}
}

func TestOEmbedMaxDimensionClamp(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	pub := seedVideo(repo, ada, "Clip", "d", "public", "published", 5)
	u := "https://videos.example/videos/" + pub.String()

	tests := []struct {
		name         string
		query        string
		wantW, wantH int
	}{
		{"no clamp", "", 640, 360},
		{"maxwidth below default", "&maxwidth=320", 320, 180},
		{"maxheight below default", "&maxheight=90", 160, 90},
		{"both maxes", "&maxwidth=320&maxheight=90", 160, 90},
		{"maxwidth above default is ignored", "&maxwidth=9999", 640, 360},
		{"invalid maxwidth is ignored", "&maxwidth=abc", 640, 360},
		{"negative maxwidth is ignored", "&maxwidth=-5", 640, 360},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, "/services/oembed?url="+url.QueryEscape(u)+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			var resp oembedResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("bad json: %v", err)
			}
			if resp.Width != tc.wantW || resp.Height != tc.wantH {
				t.Errorf("dims = %dx%d, want %dx%d", resp.Width, resp.Height, tc.wantW, tc.wantH)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// E3 — sitemap
// ---------------------------------------------------------------------------

func TestSitemap(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	bob, err := repo.channels.GetChannelByHandle(context.Background(), "bob")
	if err != nil {
		t.Fatalf("resolve bob: %v", err)
	}
	v1 := seedVideo(repo, ada, "Ada One", "d", "public", "published", 10)
	v2 := seedVideo(repo, ada, "Ada Two", "d", "public", "published", 20)
	v3 := seedVideo(repo, bob, "Bob One", "d", "public", "published", 15)
	seedVideo(repo, ada, "Ada Private", "d", "private", "published", 5)
	seedVideo(repo, ada, "Ada Draft", "d", "public", "draft", 5)

	rec := get(t, srv, "/sitemap.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeSitemap {
		t.Errorf("content-type = %q, want %q", ct, contentTypeSitemap)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, s-maxage=3600" {
		t.Errorf("cache-control = %q, want public, s-maxage=3600", cc)
	}
	var set sitemapURLSet
	if err := xml.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("sitemap not well-formed: %v", err)
	}
	if set.NS != sitemapNamespace {
		t.Errorf("xmlns = %q, want %q", set.NS, sitemapNamespace)
	}
	locs := map[string]string{}
	for _, u := range set.URLs {
		locs[u.Loc] = u.LastMod
	}
	mustHave := []string{
		"https://videos.example/",
		"https://videos.example/trending",
		"https://videos.example/search",
		"https://videos.example/channels/ada",
		"https://videos.example/channels/bob",
		"https://videos.example/videos/" + v1.String(),
		"https://videos.example/videos/" + v2.String(),
		"https://videos.example/videos/" + v3.String(),
	}
	for _, want := range mustHave {
		if _, ok := locs[want]; !ok {
			t.Errorf("sitemap missing %q", want)
		}
	}
	// Private/draft videos never appear.
	for _, bad := range []string{"Ada Private", "Ada Draft"} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("sitemap leaked non-public video %q", bad)
		}
	}
	// Video URLs carry a lastmod; parse it to prove W3C/RFC3339 shape.
	lm := locs["https://videos.example/videos/"+v1.String()]
	if _, err := time.Parse(time.RFC3339, lm); err != nil {
		t.Errorf("video lastmod = %q not RFC3339: %v", lm, err)
	}
	// Static routes carry no lastmod.
	if locs["https://videos.example/trending"] != "" {
		t.Errorf("static route should have no lastmod, got %q", locs["https://videos.example/trending"])
	}
}

// ---------------------------------------------------------------------------
// Route exemption: without a public origin the routes are not mounted, which is
// exactly what keeps them out of the OpenAPI drift guard (TestOpenAPIContract).
// ---------------------------------------------------------------------------

func TestDistributionRoutesUnmountedWithoutPublicOrigin(t *testing.T) {
	srv := videoServer(t) // testConfig(): PublicBaseURL == ""
	for _, path := range []string{"/feeds/videos.xml", "/services/oembed", "/sitemap.xml"} {
		rec := get(t, srv, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 (route must not be registered without PUBLIC_BASE_URL)", path, rec.Code)
		}
	}
}

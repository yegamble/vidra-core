package httpapi

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/shortid"
	"github.com/vidra/vidra-core/internal/video"
)

// Distribution surfaces (audit Wave E). Three ROOT-mounted public endpoints —
// an RSS 2.0 video feed, an oEmbed provider, and an XML sitemap — that let other
// software discover and embed this instance's public content. They are NOT part
// of the REST OpenAPI contract and NOT in the frontend generated client; they
// live at the root like /healthz and the fediverse discovery routes, and mount
// only when a public origin is configured (see routes()).
//
// Every absolute URL is built from cfg.PublicBaseURL — the instance's canonical
// public origin, the same value atproto/federation/OAuth already use to build
// watch and actor URLs. The frontend routes these paths serve (verified in
// vidra-user): /videos/{id} (watch), /embed/{id} (embed), /channels/{handle},
// /trending, /search.

const (
	// mediaRSSNamespace is the Media RSS (MRSS) namespace for <media:thumbnail>.
	mediaRSSNamespace = "http://search.yahoo.com/mrss/"
	// atomNamespace carries the <atom:link rel="self"> discovery hint.
	atomNamespace = "http://www.w3.org/2005/Atom"
	// sitemapNamespace is the sitemaps.org 0.9 schema.
	sitemapNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

	contentTypeRSS     = "application/rss+xml; charset=utf-8"
	contentTypeSitemap = "application/xml; charset=utf-8"

	// feedItemLimit caps the RSS feed; sitemapVideoLimit caps the sitemap (v1 is
	// a single file — a sitemapindex is a Later item).
	feedItemLimit     = 50
	sitemapVideoLimit = 5000

	// rssDescriptionMax truncates item descriptions (runes) so a feed stays lean.
	rssDescriptionMax = 500

	// Default embed dimensions when a maxwidth/maxheight is not supplied. Vidra
	// stores no per-video pixel dimensions, so oEmbed advertises a 16:9 box.
	oembedDefaultWidth  = 640
	oembedDefaultHeight = 360
)

// webOrigin is the instance's public origin with no trailing slash (config
// trims it at load). It is non-empty whenever these routes are mounted.
func (s *Server) webOrigin() string { return s.cfg.PublicBaseURL }

func (s *Server) watchURL(id uuid.UUID) string { return s.webOrigin() + "/videos/" + id.String() }
func (s *Server) embedURL(id uuid.UUID) string { return s.webOrigin() + "/embed/" + id.String() }
func (s *Server) channelURL(handle string) string {
	return s.webOrigin() + "/channels/" + handle
}
func (s *Server) thumbnailURL(id uuid.UUID) string {
	return s.webOrigin() + "/api/v1/videos/" + id.String() + "/thumbnail"
}

// instanceName resolves the effective instance name (settings overlay → config
// default), the same seam the public /instance endpoint uses.
func (s *Server) instanceName() string {
	return s.settingString(instancesettings.KeyInstanceName, s.cfg.InstanceName)
}

// ---------------------------------------------------------------------------
// E1 — RSS 2.0 feed: GET /feeds/videos.xml[?channel={handle}]
// ---------------------------------------------------------------------------

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	MediaNS string     `xml:"xmlns:media,attr"`
	AtomNS  string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	AtomLink      atomLink  `xml:"atom:link"`
	Items         []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string          `xml:"title"`
	Link        string          `xml:"link"`
	GUID        rssGUID         `xml:"guid"`
	PubDate     string          `xml:"pubDate"`
	Description string          `xml:"description"`
	Thumbnail   *mediaThumbnail `xml:"media:thumbnail,omitempty"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink bool   `xml:"isPermaLink,attr"`
}

type mediaThumbnail struct {
	URL string `xml:"url,attr"`
}

// handleVideosFeed renders the public RSS 2.0 feed of the newest published
// public LOCAL videos. With ?channel={handle} it filters to one channel (unknown
// handle → 404). It NEVER surfaces private/password/unlisted/remote videos.
// Fail-safe: an empty feed beats an error page.
func (s *Server) handleVideosFeed(c echo.Context) error {
	ctx := c.Request().Context()
	origin := s.webOrigin()
	instance := s.instanceName()

	var (
		items    []video.FeedItem
		title    = instance
		link     = origin + "/"
		selfURL  = origin + "/feeds/videos.xml"
		descText = "Latest public videos on " + instance
		err      error
	)

	if handle := strings.TrimSpace(c.QueryParam("channel")); handle != "" {
		ch, cerr := s.channelsvc.GetByHandle(ctx, handle)
		if cerr != nil {
			if errors.Is(cerr, channel.ErrNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "channel not found")
			}
			return cerr
		}
		// Anonymous surface: honour the INSTANCE sensitive-content policy (0100).
		// Under "hide", flagged videos never leak into the public feed.
		// The feed cap is the query's LIMIT now, so the RSS route never pulls a
		// whole channel into memory to throw most of it away.
		items, _, err = s.videosvc.ListPublicByChannel(ctx, ch.ID, s.hideSensitiveVideos(), "", feedItemLimit, 0)
		if err != nil {
			return err
		}
		title = ch.DisplayName + " — " + instance
		link = s.channelURL(ch.Handle)
		selfURL = origin + "/feeds/videos.xml?channel=" + url.QueryEscape(ch.Handle)
		descText = "Latest public videos from " + ch.DisplayName + " on " + instance
	} else {
		// Anonymous surface: honour the INSTANCE sensitive-content policy (0100).
		items, _, err = s.videosvc.ListPublic(ctx, "recent", "local",
			video.FeedFilter{HideSensitive: s.hideSensitiveVideos()}, uuid.Nil, false, feedItemLimit, 0)
		if err != nil {
			return err
		}
	}

	doc := rssDocument{
		Version: "2.0",
		MediaNS: mediaRSSNamespace,
		AtomNS:  atomNamespace,
		Channel: rssChannel{
			Title:       title,
			Link:        link,
			Description: descText,
			AtomLink:    atomLink{Href: selfURL, Rel: "self", Type: "application/rss+xml"},
		},
	}
	if len(items) > 0 {
		doc.Channel.LastBuildDate = items[0].Video.CreatedAt.UTC().Format(time.RFC1123Z)
	}
	for _, it := range items {
		id := it.Video.ID
		item := rssItem{
			Title:       it.Video.Title,
			Link:        s.watchURL(id),
			GUID:        rssGUID{Value: s.watchURL(id), IsPermaLink: true},
			PubDate:     it.Video.CreatedAt.UTC().Format(time.RFC1123Z),
			Description: truncateRunes(it.Video.Description, rssDescriptionMax),
		}
		if it.HasThumbnail {
			item.Thumbnail = &mediaThumbnail{URL: s.thumbnailURL(id)}
		}
		doc.Channel.Items = append(doc.Channel.Items, item)
	}

	return writeXML(c, contentTypeRSS, "public, s-maxage=300", doc)
}

// ---------------------------------------------------------------------------
// E2 — oEmbed provider: GET /services/oembed?url=…&format=json[&maxwidth&maxheight]
// ---------------------------------------------------------------------------

type oembedResponse struct {
	Version      string `json:"version"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	AuthorName   string `json:"author_name,omitempty"`
	AuthorURL    string `json:"author_url,omitempty"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	HTML         string `json:"html"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// handleOEmbed implements the oEmbed 1.0 provider for public/unlisted local
// videos. It mirrors the embed-privacy visibility rules: public and unlisted
// videos MAY embed; private and password videos are 401; anything else (foreign
// URL, non-video path, unknown/unpublished video) is 404. XML format is not
// implemented (501); a missing format defaults to json.
func (s *Server) handleOEmbed(c echo.Context) error {
	if format := c.QueryParam("format"); format != "" && format != "json" {
		return echo.NewHTTPError(http.StatusNotImplemented, "only the json oEmbed format is supported")
	}
	raw := strings.TrimSpace(c.QueryParam("url"))
	if raw == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}
	id, ok := s.parseLocalVideoURL(c.Request().Context(), raw)
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "no video matches the url")
	}

	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "no video matches the url")
		}
		return err
	}
	if v.State != "published" {
		return echo.NewHTTPError(http.StatusNotFound, "no video matches the url")
	}
	switch v.Privacy {
	case "public", "unlisted":
		// embeddable
	case "private", video.PrivacyPassword:
		return echo.NewHTTPError(http.StatusUnauthorized, "video is not publicly embeddable")
	default:
		return echo.NewHTTPError(http.StatusNotFound, "no video matches the url")
	}

	width, height := oembedDimensions(c.QueryParam("maxwidth"), c.QueryParam("maxheight"))
	resp := oembedResponse{
		Version:      "1.0",
		Type:         "video",
		Title:        v.Title,
		AuthorName:   v.ChannelDisplayName,
		AuthorURL:    s.channelURL(v.ChannelHandle),
		ProviderName: s.instanceName(),
		ProviderURL:  s.webOrigin(),
		HTML: fmt.Sprintf(
			`<iframe src=%q width=%q height=%q frameborder="0" allow="autoplay; fullscreen; picture-in-picture" allowfullscreen></iframe>`,
			s.embedURL(id), strconv.Itoa(width), strconv.Itoa(height)),
		Width:  width,
		Height: height,
	}
	// thumbnail_url only when a poster actually exists (the endpoint 404s
	// otherwise). FileForView is a metadata lookup that mirrors the public
	// thumbnail visibility for an anonymous viewer.
	if _, terr := s.videosvc.FileForView(c.Request().Context(), id, uuid.Nil, false, "thumbnail"); terr == nil {
		resp.ThumbnailURL = s.thumbnailURL(id)
	}

	return c.JSON(http.StatusOK, resp)
}

// parseLocalVideoURL accepts an absolute URL and returns the video id when it is
// a watch (/videos/{id}), embed (/embed/{id}) or short share (/v/{sid}) URL on
// THIS instance's host. Any foreign host, unexpected scheme, extra path
// segments, or an id that does not parse in the form its prefix implies → false.
//
// The short form has to be accepted here even though the frontend 301s /v/{sid}
// to /videos/{id}: short links are what people actually paste, and a CMS or
// chat app unfurling one calls oEmbed with the URL verbatim — it never follows
// the redirect first. Without this branch every shared short link unfurls as a
// dead card while the same video's long URL unfurls fine.
//
// TWO encodings share the /v/ prefix and are told apart by LENGTH, which is
// unambiguous because their ranges do not overlap:
//
//	11 chars    the STORED short code (videos.short_code) — resolved in the DB
//	16-22 chars the DERIVED sid, a base58 re-encoding of the uuid (internal/shortid)
//
// The derived band is kept forever: those links were published and browsers
// hold their redirects permanently. Note this is why the function now needs a
// context — the stored code cannot be decoded, only looked up.
//
// Deliberately NOT handled here: /w/{shortUUID} and /videos/watch/{uuid}, the
// forms an imported PeerTube instance's old links use. They redirect to the
// canonical page, and an unfurler that follows the redirect reads the discovery
// tag there. Supporting them verbatim would mean a PeerTube base58 decoder in
// core — a DIFFERENT alphabet ordering from internal/shortid's, so it cannot
// share that code — for links that are rare after a cutover.
func (s *Server) parseLocalVideoURL(ctx context.Context, raw string) (uuid.UUID, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return uuid.Nil, false
	}
	origin, err := url.Parse(s.webOrigin())
	if err != nil || !strings.EqualFold(u.Host, origin.Host) {
		return uuid.Nil, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return uuid.Nil, false
	}
	switch parts[0] {
	case "videos", "embed":
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return uuid.Nil, false
		}
		return id, true
	case "v":
		if len(parts[1]) == video.ShortCodeLen {
			id, err := s.videosvc.IDByShortCode(ctx, parts[1])
			if err != nil {
				return uuid.Nil, false
			}
			return id, true
		}
		return shortid.ToUUID(parts[1])
	default:
		return uuid.Nil, false
	}
}

// oembedDimensions clamps the default 16:9 box to the supplied maxwidth/maxheight
// (invalid/absent values are ignored), preserving the aspect ratio.
func oembedDimensions(maxWidthParam, maxHeightParam string) (int, int) {
	w, h := oembedDefaultWidth, oembedDefaultHeight
	if mw := parsePositiveInt(maxWidthParam); mw > 0 && mw < w {
		w = mw
		h = w * oembedDefaultHeight / oembedDefaultWidth
	}
	if mh := parsePositiveInt(maxHeightParam); mh > 0 && mh < h {
		h = mh
		w = h * oembedDefaultWidth / oembedDefaultHeight
	}
	return w, h
}

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// E3 — sitemap: GET /sitemap.xml
// ---------------------------------------------------------------------------

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// handleSitemap renders a single-file sitemap: the instance root and the static
// discovery routes, each public channel that has recent public content, then the
// newest published public LOCAL videos (capped). lastmod is the row's updated_at.
func (s *Server) handleSitemap(c echo.Context) error {
	ctx := c.Request().Context()
	origin := s.webOrigin()

	// Anonymous surface: honour the INSTANCE sensitive-content policy (0100), so
	// flagged videos never leak into the sitemap under the "hide" policy.
	items, _, err := s.videosvc.ListPublic(ctx, "recent", "local",
		video.FeedFilter{HideSensitive: s.hideSensitiveVideos()}, uuid.Nil, false, sitemapVideoLimit, 0)
	if err != nil {
		return err
	}

	set := sitemapURLSet{NS: sitemapNamespace}
	set.URLs = append(set.URLs,
		sitemapURL{Loc: origin + "/"},
		sitemapURL{Loc: origin + "/trending"},
		sitemapURL{Loc: origin + "/search"},
	)

	// Public channels, derived from the videos in the window (a channel is worth
	// indexing when it has public content). First-seen order == most recent, so
	// the first sighting carries the freshest lastmod.
	seen := make(map[string]bool)
	for _, it := range items {
		handle := it.ChannelHandle
		if handle == "" || seen[handle] {
			continue
		}
		seen[handle] = true
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.channelURL(handle),
			LastMod: it.Video.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	for _, it := range items {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.watchURL(it.Video.ID),
			LastMod: it.Video.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	return writeXML(c, contentTypeSitemap, "public, s-maxage=3600", set)
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// writeXML marshals doc with encoding/xml (never string templating — titles and
// descriptions are attacker-controlled), prepends the XML declaration, and sets
// the content-type and cache-control headers.
func writeXML(c echo.Context, contentType, cacheControl string, doc any) error {
	body, err := xml.Marshal(doc)
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", cacheControl)
	return c.Blob(http.StatusOK, contentType, append([]byte(xml.Header), body...))
}

// truncateRunes trims whitespace and shortens s to at most n runes, appending an
// ellipsis when it had to cut.
func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

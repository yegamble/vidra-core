package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/media"
)

// Podcast feed: GET /feeds/podcast/videos.xml?channel={handle} (PeerTube's
// ?videoChannelName= is accepted too). PeerTube serves podcasters the same
// path, so a feed URL held in a podcast app survives a migration. Unlike the
// MRSS feed, every item carries an <enclosure> a podcast app can download.
//
// The enclosure is ONLY ever an official download asset (the same files
// /videos/{id}/download advertises): the audio-only M4A when transcoding made
// one, else the highest progressive HLS-remux MP4. It is gated by the SAME
// predicate the download route's anonymous path uses (publicDownloadOf: public
// + published + instance downloads_enabled + the video's download_enabled), so
// a video whose owner switched downloads off simply drops out of the podcast
// rather than leaking a file. Items with no enclosure are omitted — an item a
// podcast app cannot play is a broken episode, not a partial one.

const (
	itunesNamespace  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	podcastNamespace = "https://podcastindex.org/namespace/1.0"
	// podcastGUIDNamespace is the fixed UUIDv5 namespace the Podcasting 2.0
	// spec mandates for podcast:guid.
	podcastGUIDNamespace = "ead4c236-bf58-58c6-a2c6-a6b28d128cb6"
)

type podcastDocument struct {
	XMLName   struct{}       `xml:"rss"`
	Version   string         `xml:"version,attr"`
	ItunesNS  string         `xml:"xmlns:itunes,attr"`
	PodcastNS string         `xml:"xmlns:podcast,attr"`
	AtomNS    string         `xml:"xmlns:atom,attr"`
	Channel   podcastChannel `xml:"channel"`
}

type podcastChannel struct {
	Title         string        `xml:"title"`
	Link          string        `xml:"link"`
	Description   string        `xml:"description"`
	LastBuildDate string        `xml:"lastBuildDate,omitempty"`
	AtomLink      atomLink      `xml:"atom:link"`
	Author        string        `xml:"itunes:author"`
	Image         itunesImage   `xml:"itunes:image"`
	Locked        string        `xml:"podcast:locked"`
	GUID          string        `xml:"podcast:guid"`
	Items         []podcastItem `xml:"item"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type podcastItem struct {
	Title       string       `xml:"title"`
	Link        string       `xml:"link"`
	GUID        rssGUID      `xml:"guid"`
	PubDate     string       `xml:"pubDate"`
	Description string       `xml:"description"`
	Enclosure   rssEnclosure `xml:"enclosure"`
	Duration    string       `xml:"itunes:duration,omitempty"`
	Image       *itunesImage `xml:"itunes:image,omitempty"`
}

// podcastEnclosure picks the downloadable media file for a video, or ok=false.
func (s *Server) podcastEnclosure(c echo.Context, id uuid.UUID) (rssEnclosure, bool, error) {
	ctx := c.Request().Context()
	if s.transcodesvc == nil || s.media == nil {
		return rssEnclosure{}, false, nil
	}
	sp, ok := s.transcodesvc.Playlist(ctx, id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return rssEnclosure{}, false, nil
	}
	base := s.webOrigin() + "/api/v1/videos/" + id.String() + "/download"
	size, exists, err := s.downloadObjectSize(ctx, media.HLSAudioDownloadKey(sp.MasterKey))
	if err != nil {
		return rssEnclosure{}, false, err
	}
	if exists {
		return rssEnclosure{URL: base + "/audio", Length: size, Type: "audio/mp4"}, true, nil
	}
	var best int32
	var bestSize int64
	for _, r := range s.transcodesvc.Renditions(ctx, id) {
		if r.Height <= best {
			continue
		}
		sz, ok, err := s.downloadObjectSize(ctx, media.HLSDownloadKey(r.KeyPrefix, true))
		if err != nil {
			return rssEnclosure{}, false, err
		}
		if ok {
			best, bestSize = r.Height, sz
		}
	}
	if best == 0 {
		return rssEnclosure{}, false, nil
	}
	return rssEnclosure{URL: base + "/hls/" + strconv.Itoa(int(best)), Length: bestSize, Type: "video/mp4"}, true, nil
}

func (s *Server) handlePodcastFeed(c echo.Context) error {
	ctx := c.Request().Context()
	scope, err := s.resolveFeedScope(c)
	if err != nil {
		return err
	}
	// A podcast is ONE show: an instance-wide or multi-channel feed has no
	// coherent identity (podcast:guid, author, artwork).
	if scope == nil || len(scope.channels) != 1 {
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	}
	ch := scope.channels[0]
	origin := s.webOrigin()
	instance := s.instanceName()
	selfURL := origin + "/feeds/podcast/videos.xml?channel=" + url.QueryEscape(ch.Handle)
	avatar := origin + "/api/v1/channels/" + url.PathEscape(ch.Handle) + "/avatar"

	desc := strings.TrimSpace(ch.Description)
	if desc == "" {
		desc = "Videos from " + ch.DisplayName + " on " + instance
	}
	doc := podcastDocument{
		Version: "2.0", ItunesNS: itunesNamespace, PodcastNS: podcastNamespace, AtomNS: atomNamespace,
		Channel: podcastChannel{
			Title:       ch.DisplayName,
			Link:        s.channelURL(ch.Handle),
			Description: desc,
			AtomLink:    atomLink{Href: selfURL, Rel: "self", Type: "application/rss+xml"},
			Author:      ch.DisplayName,
			Image:       itunesImage{Href: avatar},
			Locked:      "no",
			// Keyed on the channel's immutable id (not its renameable handle)
			// so the guid survives a handle change.
			GUID: uuid.NewSHA1(uuid.MustParse(podcastGUIDNamespace), []byte(ch.ID.String())).String(),
		},
	}
	rows, _, err := s.videosvc.ListPublicByChannel(ctx, ch.ID, uuid.Nil, false, s.hideSensitiveVideos(), "", feedItemLimit, 0)
	if err != nil {
		return err
	}
	for _, it := range rows {
		v := it.Video
		// The discovery row does not carry download_enabled, so the per-video
		// gate is read from the full row (<=50 point lookups, cached 5 min).
		full, gerr := s.videosvc.GetByID(ctx, v.ID)
		if gerr != nil {
			continue
		}
		if !s.publicDownloadOf(full.Privacy, full.State, full.DownloadEnabled) {
			continue
		}
		enc, ok, eerr := s.podcastEnclosure(c, v.ID)
		if eerr != nil {
			return eerr
		}
		if !ok {
			continue
		}
		item := podcastItem{
			Title:       v.Title,
			Link:        s.canonicalWatchURL(v.ID, v.ShortCode),
			GUID:        rssGUID{Value: s.watchURL(v.ID), IsPermaLink: true},
			PubDate:     v.CreatedAt.UTC().Format(time.RFC1123Z),
			Description: truncateRunes(v.Description, rssDescriptionMax),
			Enclosure:   enc,
		}
		if it.DurationSeconds != nil && *it.DurationSeconds > 0 {
			item.Duration = strconv.Itoa(int(*it.DurationSeconds))
		}
		if it.HasThumbnail {
			item.Image = &itunesImage{Href: s.thumbnailURL(v.ID)}
		}
		if doc.Channel.LastBuildDate == "" {
			doc.Channel.LastBuildDate = item.PubDate
		}
		doc.Channel.Items = append(doc.Channel.Items, item)
	}
	return writeXML(c, contentTypeRSS, "public, s-maxage=300", doc)
}

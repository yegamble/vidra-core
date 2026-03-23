package video

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"
)

// subscriptionFeedRepo is the narrow interface needed for subscription feeds.
type subscriptionFeedRepo interface {
	ListSubscriptionVideos(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error)
}

// FeedHandlers provides RSS/Atom feed endpoints.
type FeedHandlers struct {
	videoRepo   usecase.VideoRepository
	commentRepo usecase.CommentRepository
	baseURL     string
	subRepo     subscriptionFeedRepo
}

// NewFeedHandlers creates a new FeedHandlers.
func NewFeedHandlers(videoRepo usecase.VideoRepository, commentRepo usecase.CommentRepository, baseURL string) *FeedHandlers {
	return &FeedHandlers{videoRepo: videoRepo, commentRepo: commentRepo, baseURL: baseURL}
}

// SetSubRepo sets the subscription feed repository.
func (h *FeedHandlers) SetSubRepo(repo subscriptionFeedRepo) {
	h.subRepo = repo
}

// --- Atom 1.0 types ---

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	XMLNS   string      `xml:"xmlns,attr"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Link    []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	Title   string     `xml:"title"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Link    atomLink   `xml:"link"`
	Summary string     `xml:"summary,omitempty"`
	Author  atomAuthor `xml:"author,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

// --- RSS 2.0 types ---

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func writeAtomFeed(w http.ResponseWriter, feed *atomFeed) {
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}

func writeRSSFeed(w http.ResponseWriter, feed *rssFeed) {
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}

// VideosFeed handles GET /feeds/videos.atom — returns Atom 1.0 feed of recent public videos.
func (h *FeedHandlers) VideosFeed(w http.ResponseWriter, r *http.Request) {
	req := &domain.VideoSearchRequest{
		Privacy: domain.PrivacyPublic,
		Sort:    "upload_date",
		Order:   "desc",
		Limit:   20,
	}
	if channelIDStr := r.URL.Query().Get("videoChannelId"); channelIDStr != "" {
		if id, err := uuid.Parse(channelIDStr); err == nil {
			req.ChannelID = &id
		}
	}
	if accountIDStr := r.URL.Query().Get("accountId"); accountIDStr != "" {
		if id, err := uuid.Parse(accountIDStr); err == nil {
			req.AccountID = &id
		}
	}
	videos, _, err := h.videoRepo.List(r.Context(), req)
	if err != nil {
		http.Error(w, "Failed to load videos", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	feed := &atomFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		Title:   "Recent Videos",
		ID:      h.baseURL + "/feeds/videos.atom",
		Updated: now,
		Link:    []atomLink{{Rel: "self", Href: h.baseURL + "/feeds/videos.atom"}},
	}

	for _, v := range videos {
		updated := v.UpdatedAt.UTC().Format(time.RFC3339)
		entry := atomEntry{
			Title:   v.Title,
			ID:      fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			Updated: updated,
			Link:    atomLink{Href: fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID)},
			Summary: v.Description,
		}
		feed.Entries = append(feed.Entries, entry)
	}

	writeAtomFeed(w, feed)
}

// VideosFeedRSS handles GET /feeds/videos.rss — returns RSS 2.0 feed of recent public videos.
func (h *FeedHandlers) VideosFeedRSS(w http.ResponseWriter, r *http.Request) {
	req := &domain.VideoSearchRequest{
		Privacy: domain.PrivacyPublic,
		Sort:    "upload_date",
		Order:   "desc",
		Limit:   20,
	}
	if channelIDStr := r.URL.Query().Get("videoChannelId"); channelIDStr != "" {
		if id, err := uuid.Parse(channelIDStr); err == nil {
			req.ChannelID = &id
		}
	}
	if accountIDStr := r.URL.Query().Get("accountId"); accountIDStr != "" {
		if id, err := uuid.Parse(accountIDStr); err == nil {
			req.AccountID = &id
		}
	}
	videos, _, err := h.videoRepo.List(r.Context(), req)
	if err != nil {
		http.Error(w, "Failed to load videos", http.StatusInternalServerError)
		return
	}

	feed := &rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       "Recent Videos",
			Link:        h.baseURL,
			Description: "Recent public videos",
		},
	}

	for _, v := range videos {
		item := rssItem{
			Title:       v.Title,
			Link:        fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			GUID:        fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			PubDate:     v.UploadDate.UTC().Format(time.RFC1123Z),
			Description: v.Description,
		}
		feed.Channel.Items = append(feed.Channel.Items, item)
	}

	writeRSSFeed(w, feed)
}

// SubscriptionFeedAtom handles GET /feeds/subscriptions.atom — returns Atom feed of subscribed channel videos.
func (h *FeedHandlers) SubscriptionFeedAtom(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	videos, _, err := h.subRepo.ListSubscriptionVideos(r.Context(), userID, 20, 0)
	if err != nil {
		http.Error(w, "Failed to load subscription feed", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	feed := &atomFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		Title:   "My Subscription Feed",
		ID:      h.baseURL + "/feeds/subscriptions.atom",
		Updated: now,
		Link:    []atomLink{{Rel: "self", Href: h.baseURL + "/feeds/subscriptions.atom"}},
	}
	for _, v := range videos {
		updated := v.UpdatedAt.UTC().Format(time.RFC3339)
		entry := atomEntry{
			Title:   v.Title,
			ID:      fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			Updated: updated,
			Link:    atomLink{Href: fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID)},
			Summary: v.Description,
		}
		feed.Entries = append(feed.Entries, entry)
	}
	writeAtomFeed(w, feed)
}

// SubscriptionFeedRSS handles GET /feeds/subscriptions.rss — returns RSS feed of subscribed channel videos.
func (h *FeedHandlers) SubscriptionFeedRSS(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	videos, _, err := h.subRepo.ListSubscriptionVideos(r.Context(), userID, 20, 0)
	if err != nil {
		http.Error(w, "Failed to load subscription feed", http.StatusInternalServerError)
		return
	}
	feed := &rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       "My Subscription Feed",
			Link:        h.baseURL,
			Description: "Videos from channels you subscribe to",
		},
	}
	for _, v := range videos {
		item := rssItem{
			Title:       v.Title,
			Link:        fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			GUID:        fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			PubDate:     v.CreatedAt.UTC().Format(time.RFC1123Z),
			Description: v.Description,
		}
		feed.Channel.Items = append(feed.Channel.Items, item)
	}
	writeRSSFeed(w, feed)
}

// --- Podcast RSS 2.0 types (iTunes namespace) ---

type podcastRSSFeed struct {
	XMLName xml.Name       `xml:"rss"`
	Version string         `xml:"version,attr"`
	XMLNS   string         `xml:"xmlns:itunes,attr"`
	Channel podcastChannel `xml:"channel"`
}

type podcastChannel struct {
	Title       string        `xml:"title"`
	Link        string        `xml:"link"`
	Description string        `xml:"description"`
	Language    string        `xml:"language"`
	Items       []podcastItem `xml:"item"`
}

type podcastItem struct {
	Title     string           `xml:"title"`
	Link      string           `xml:"link"`
	GUID      string           `xml:"guid"`
	PubDate   string           `xml:"pubDate"`
	Summary   string           `xml:"itunes:summary"`
	Duration  string           `xml:"itunes:duration"`
	Enclosure podcastEnclosure `xml:"enclosure"`
}

type podcastEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// PodcastFeed handles GET /feeds/podcast/videos.xml — returns podcast-compatible RSS 2.0.
func (h *FeedHandlers) PodcastFeed(w http.ResponseWriter, r *http.Request) {
	req := &domain.VideoSearchRequest{
		Privacy: domain.PrivacyPublic,
		Sort:    "upload_date",
		Order:   "desc",
		Limit:   20,
	}
	if channelIDStr := r.URL.Query().Get("videoChannelId"); channelIDStr != "" {
		if id, err := uuid.Parse(channelIDStr); err == nil {
			req.ChannelID = &id
		}
	}

	videos, _, err := h.videoRepo.List(r.Context(), req)
	if err != nil {
		http.Error(w, "Failed to load videos", http.StatusInternalServerError)
		return
	}

	feed := &podcastRSSFeed{
		Version: "2.0",
		XMLNS:   "http://www.itunes.com/dtds/podcast-1.0.dtd",
		Channel: podcastChannel{
			Title:       "Videos Podcast Feed",
			Link:        h.baseURL,
			Description: "Recent public videos as a podcast",
			Language:    "en",
		},
	}

	for _, v := range videos {
		mimeType := v.MimeType
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		videoURL := v.S3URLs["source"]
		if videoURL == "" {
			videoURL = fmt.Sprintf("%s/videos/%s/stream", h.baseURL, v.ID)
		}
		durationStr := fmt.Sprintf("%d:%02d", v.Duration/60, v.Duration%60)
		item := podcastItem{
			Title:    v.Title,
			Link:     fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			GUID:     fmt.Sprintf("%s/videos/%s", h.baseURL, v.ID),
			PubDate:  v.UploadDate.UTC().Format(time.RFC1123Z),
			Summary:  v.Description,
			Duration: durationStr,
			Enclosure: podcastEnclosure{
				URL:    videoURL,
				Length: v.FileSize,
				Type:   mimeType,
			},
		}
		feed.Channel.Items = append(feed.Channel.Items, item)
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}

// CommentsFeed handles GET /feeds/video-comments.atom — returns Atom feed of comments.
// If the "videoId" query param is provided, returns comments for that video.
// Otherwise returns an empty feed.
func (h *FeedHandlers) CommentsFeed(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC().Format(time.RFC3339)
	feed := &atomFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		Title:   "Video Comments",
		ID:      h.baseURL + "/feeds/video-comments.atom",
		Updated: now,
		Link:    []atomLink{{Rel: "self", Href: h.baseURL + "/feeds/video-comments.atom"}},
	}

	videoIDStr := r.URL.Query().Get("videoId")
	if videoIDStr != "" {
		videoID, err := uuid.Parse(videoIDStr)
		if err == nil {
			comments, err := h.commentRepo.ListByVideo(r.Context(), domain.CommentListOptions{
				VideoID: videoID,
				Limit:   20,
			})
			if err != nil {
				log.Printf("feed: failed to list comments for video %s: %v", videoIDStr, err)
			} else {
				for _, c := range comments {
					entry := atomEntry{
						Title:   fmt.Sprintf("Comment by %s", c.Username),
						ID:      fmt.Sprintf("%s/videos/%s#comment-%s", h.baseURL, c.VideoID, c.ID),
						Updated: c.CreatedAt.UTC().Format(time.RFC3339),
						Link:    atomLink{Href: fmt.Sprintf("%s/videos/%s", h.baseURL, c.VideoID)},
						Summary: c.Body,
						Author:  atomAuthor{Name: c.Username},
					}
					feed.Entries = append(feed.Entries, entry)
				}
			}
		}
	}

	writeAtomFeed(w, feed)
}

package video

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/usecase"
)

// FeedHandlers provides RSS/Atom feed endpoints.
type FeedHandlers struct {
	videoRepo   usecase.VideoRepository
	commentRepo usecase.CommentRepository
	baseURL     string
}

// NewFeedHandlers creates a new FeedHandlers.
func NewFeedHandlers(videoRepo usecase.VideoRepository, commentRepo usecase.CommentRepository, baseURL string) *FeedHandlers {
	return &FeedHandlers{videoRepo: videoRepo, commentRepo: commentRepo, baseURL: baseURL}
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
	videos, _, err := h.videoRepo.List(r.Context(), &domain.VideoSearchRequest{
		Privacy: domain.PrivacyPublic,
		Sort:    "upload_date",
		Order:   "desc",
		Limit:   20,
	})
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
	videos, _, err := h.videoRepo.List(r.Context(), &domain.VideoSearchRequest{
		Privacy: domain.PrivacyPublic,
		Sort:    "upload_date",
		Order:   "desc",
		Limit:   20,
	})
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
			if err == nil {
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

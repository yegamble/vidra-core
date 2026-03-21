package video

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/mock"
)

func TestPodcastFeed_ContentTypeAndStructure(t *testing.T) {
	mockVideo := &MockVideoRepoForFeed{}
	mockComment := &MockCommentRepoForFeed{}
	h := NewFeedHandlers(mockVideo, mockComment, "https://example.com")

	videos := []*domain.Video{
		{
			ID:          "vid-1",
			Title:       "Test Episode",
			Description: "A test episode",
			Duration:    300,
			UploadDate:  time.Now(),
			Privacy:     domain.PrivacyPublic,
			MimeType:    "video/mp4",
		},
	}
	mockVideo.On("List", mock.Anything, mock.Anything).Return(videos, int64(1), nil)

	req := httptest.NewRequest(http.MethodGet, "/feeds/podcast/videos.xml", nil)
	rr := httptest.NewRecorder()
	h.PodcastFeed(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "rss+xml") && !strings.Contains(ct, "xml") {
		t.Fatalf("unexpected Content-Type: %s", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "itunes") {
		t.Fatal("expected iTunes namespace in podcast feed")
	}
	if !strings.Contains(body, "Test Episode") {
		t.Fatal("expected video title in feed")
	}
	if !strings.Contains(body, "enclosure") {
		t.Fatal("expected enclosure tag in podcast feed")
	}
}

func TestPodcastFeed_EmptyVideos(t *testing.T) {
	mockVideo := &MockVideoRepoForFeed{}
	mockComment := &MockCommentRepoForFeed{}
	h := NewFeedHandlers(mockVideo, mockComment, "https://example.com")

	mockVideo.On("List", mock.Anything, mock.Anything).Return([]*domain.Video{}, int64(0), nil)

	req := httptest.NewRequest(http.MethodGet, "/feeds/podcast/videos.xml", nil)
	rr := httptest.NewRecorder()
	h.PodcastFeed(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

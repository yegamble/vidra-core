package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockSubRepoForFeed struct {
	videos []domain.Video
}

func (m *mockSubRepoForFeed) GetSubscriptionVideos(_ context.Context, _ uuid.UUID, _, _ int) ([]domain.Video, int, error) {
	return m.videos, len(m.videos), nil
}

func makeAuthReq(url, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

func newFeedHandlersWithSub(subRepo subscriptionFeedRepo) *FeedHandlers {
	mockVideo := &MockVideoRepoForFeed{}
	mockComment := &MockCommentRepoForFeed{}
	h := NewFeedHandlers(mockVideo, mockComment, "http://example.com")
	h.SetSubRepo(subRepo)
	return h
}

func TestSubscriptionFeedAtom_ReturnsAtomXML(t *testing.T) {
	published := time.Now()
	id := uuid.New().String()
	userID := uuid.New().String()
	sub := &mockSubRepoForFeed{
		videos: []domain.Video{
			{ID: id, Title: "Sub Video", CreatedAt: published},
		},
	}
	h := newFeedHandlersWithSub(sub)

	req := makeAuthReq("/feeds/subscriptions.atom", userID)
	w := httptest.NewRecorder()
	h.SubscriptionFeedAtom(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<feed"), "expected Atom feed")
	assert.True(t, strings.Contains(body, "Sub Video"), "expected video title")
}

func TestSubscriptionFeedRSS_ReturnsRSSXML(t *testing.T) {
	userID := uuid.New().String()
	sub := &mockSubRepoForFeed{
		videos: []domain.Video{
			{ID: uuid.New().String(), Title: "Sub Video RSS", CreatedAt: time.Now()},
		},
	}
	h := newFeedHandlersWithSub(sub)

	req := makeAuthReq("/feeds/subscriptions.rss", userID)
	w := httptest.NewRecorder()
	h.SubscriptionFeedRSS(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "<rss"), "expected RSS feed")
}

func TestSubscriptionFeedAtom_Unauthenticated(t *testing.T) {
	h := newFeedHandlersWithSub(&mockSubRepoForFeed{})

	req := httptest.NewRequest(http.MethodGet, "/feeds/subscriptions.atom", nil)
	w := httptest.NewRecorder()
	h.SubscriptionFeedAtom(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

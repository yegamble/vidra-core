package social

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const (
	socialUnitUserID  = "11111111-1111-1111-1111-111111111111"
	socialUnitVideoID = "22222222-2222-2222-2222-222222222222"
	socialUnitItemID  = "33333333-3333-3333-3333-333333333333"
	socialUnitJobID   = "44444444-4444-4444-4444-444444444444"
	socialUnitBadUUID = "not-a-uuid"
)

func withSocialRouteParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withSocialAuthUser(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserIDKey, socialUnitUserID))
}

func TestCommentHandlers_ValidationPaths(t *testing.T) {
	h := NewCommentHandlers(nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "create invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/bad/comments", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"videoId": socialUnitBadUUID})
			},
			call:   h.CreateComment,
			status: http.StatusBadRequest,
		},
		{
			name: "create unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/comments", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"videoId": socialUnitVideoID})
			},
			call:   h.CreateComment,
			status: http.StatusUnauthorized,
		},
		{
			name: "create invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/comments", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"videoId": socialUnitVideoID})
				return withSocialAuthUser(req)
			},
			call:   h.CreateComment,
			status: http.StatusBadRequest,
		},
		{
			name: "create invalid body length",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/comments", bytes.NewBufferString(`{"body":""}`))
				req = withSocialRouteParams(req, map[string]string{"videoId": socialUnitVideoID})
				return withSocialAuthUser(req)
			},
			call:   h.CreateComment,
			status: http.StatusBadRequest,
		},
		{
			name: "get comments invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/comments", nil)
				return withSocialRouteParams(req, map[string]string{"videoId": socialUnitBadUUID})
			},
			call:   h.GetComments,
			status: http.StatusBadRequest,
		},
		{
			name: "get comments invalid parent id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/comments?parentId=bad", nil)
				req = withSocialRouteParams(req, map[string]string{"videoId": socialUnitVideoID})
				return req
			},
			call:   h.GetComments,
			status: http.StatusBadRequest,
		},
		{
			name: "get comment invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/comments/bad", nil)
				return withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
			},
			call:   h.GetComment,
			status: http.StatusBadRequest,
		},
		{
			name: "update unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/id", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
			},
			call:   h.UpdateComment,
			status: http.StatusUnauthorized,
		},
		{
			name: "update invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/id", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.UpdateComment,
			status: http.StatusBadRequest,
		},
		{
			name: "delete unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/id", nil)
				return withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
			},
			call:   h.DeleteComment,
			status: http.StatusUnauthorized,
		},
		{
			name: "flag invalid reason",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/flag", bytes.NewBufferString(`{"reason":"invalid"}`))
				req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.FlagComment,
			status: http.StatusBadRequest,
		},
		{
			name: "unflag unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/id/flag", nil)
				return withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
			},
			call:   h.UnflagComment,
			status: http.StatusUnauthorized,
		},
		{
			name: "moderate invalid status",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/moderate", bytes.NewBufferString(`{"status":"unknown"}`))
				req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.ModerateComment,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestRatingHandlers_ValidationPaths(t *testing.T) {
	h := NewRatingHandlers(nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "set rating unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/rating", bytes.NewBufferString(`{"rating":1}`))
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
			},
			call:   h.SetRating,
			status: http.StatusUnauthorized,
		},
		{
			name: "set rating invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/rating", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
				return withSocialAuthUser(req)
			},
			call:   h.SetRating,
			status: http.StatusBadRequest,
		},
		{
			name: "set rating invalid value",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/rating", bytes.NewBufferString(`{"rating":7}`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
				return withSocialAuthUser(req)
			},
			call:   h.SetRating,
			status: http.StatusBadRequest,
		},
		{
			name: "get rating invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/rating", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
			},
			call:   h.GetRating,
			status: http.StatusBadRequest,
		},
		{
			name: "remove rating unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/id/rating", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
			},
			call:   h.RemoveRating,
			status: http.StatusUnauthorized,
		},
		{
			name: "remove rating invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/id/rating", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.RemoveRating,
			status: http.StatusBadRequest,
		},
		{
			name: "get user ratings unauthorized",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/v1/users/me/ratings", nil)
			},
			call:   h.GetUserRatings,
			status: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestPlaylistHandlers_ValidationPaths(t *testing.T) {
	h := NewPlaylistHandlers(nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "create playlist unauthorized",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString(`{}`))
			},
			call:   h.CreatePlaylist,
			status: http.StatusUnauthorized,
		},
		{
			name: "create playlist invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString(`{`))
				return withSocialAuthUser(req)
			},
			call:   h.CreatePlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "get playlist invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/bad", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
			},
			call:   h.GetPlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "update playlist unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/id", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"id": socialUnitItemID})
			},
			call:   h.UpdatePlaylist,
			status: http.StatusUnauthorized,
		},
		{
			name: "update playlist invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/id", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.UpdatePlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "delete playlist invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/id", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.DeletePlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "add video invalid playlist id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/id/items", bytes.NewBufferString(`{}`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.AddVideoToPlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "add video invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/id/items", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.AddVideoToPlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "remove video invalid item id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/id/items/item", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID, "itemId": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.RemoveVideoFromPlaylist,
			status: http.StatusBadRequest,
		},
		{
			name: "get playlist items invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/bad/items", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
			},
			call:   h.GetPlaylistItems,
			status: http.StatusBadRequest,
		},
		{
			name: "reorder item invalid body",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/id/items/item/reorder", bytes.NewBufferString(`{`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID, "itemId": socialUnitVideoID})
				return withSocialAuthUser(req)
			},
			call:   h.ReorderPlaylistItem,
			status: http.StatusBadRequest,
		},
		{
			name: "get watch later unauthorized",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/v1/users/me/watch-later", nil)
			},
			call:   h.GetWatchLater,
			status: http.StatusUnauthorized,
		},
		{
			name: "add watch later invalid id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/watch-later", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.AddToWatchLater,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestCaptionHandlers_ValidationPaths(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "create caption unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
			},
			call:   h.CreateCaption,
			status: http.StatusUnauthorized,
		},
		{
			name: "create caption invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.CreateCaption,
			status: http.StatusBadRequest,
		},
		{
			name: "get captions invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
			},
			call:   h.GetCaptions,
			status: http.StatusBadRequest,
		},
		{
			name: "get caption content invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/caption/content", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "captionId": socialUnitItemID})
			},
			call:   h.GetCaptionContent,
			status: http.StatusBadRequest,
		},
		{
			name: "get caption content invalid caption id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/caption/content", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitBadUUID})
			},
			call:   h.GetCaptionContent,
			status: http.StatusBadRequest,
		},
		{
			name: "update caption unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/captions/caption", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitItemID})
			},
			call:   h.UpdateCaption,
			status: http.StatusUnauthorized,
		},
		{
			name: "update caption invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/captions/caption", bytes.NewBufferString(`{}`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "captionId": socialUnitItemID})
				return withSocialAuthUser(req)
			},
			call:   h.UpdateCaption,
			status: http.StatusBadRequest,
		},
		{
			name: "delete caption invalid caption id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/id/captions/caption", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.DeleteCaption,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestSocialHandler_ValidationPaths(t *testing.T) {
	h := NewSocialHandler(nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "follow invalid body",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/social/follow", bytes.NewBufferString(`{`))
			},
			call:   h.Follow,
			status: http.StatusBadRequest,
		},
		{
			name: "unfollow missing follower did",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/social/follow/alice", nil)
				return withSocialRouteParams(req, map[string]string{"handle": "alice"})
			},
			call:   h.Unfollow,
			status: http.StatusBadRequest,
		},
		{
			name: "like invalid body",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/social/like", bytes.NewBufferString(`{`))
			},
			call:   h.Like,
			status: http.StatusBadRequest,
		},
		{
			name: "unlike invalid body",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodDelete, "/api/v1/social/like", bytes.NewBufferString(`{`))
			},
			call:   h.Unlike,
			status: http.StatusBadRequest,
		},
		{
			name: "create comment invalid body",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/social/comment", bytes.NewBufferString(`{`))
			},
			call:   h.CreateComment,
			status: http.StatusBadRequest,
		},
		{
			name: "apply label invalid body",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/social/moderation/label", bytes.NewBufferString(`{`))
			},
			call:   h.ApplyLabel,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestCaptionGenerationHandlers_ValidationPaths(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)

	cases := []struct {
		name   string
		req    func() *http.Request
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name: "generate captions unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions/generate", bytes.NewBufferString(`{}`))
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
			},
			call:   h.GenerateCaptions,
			status: http.StatusUnauthorized,
		},
		{
			name: "generate captions invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions/generate", bytes.NewBufferString(`{}`))
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.GenerateCaptions,
			status: http.StatusBadRequest,
		},
		{
			name: "get job unauthorized",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs/job", nil)
				return withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "jobId": socialUnitJobID})
			},
			call:   h.GetCaptionGenerationJob,
			status: http.StatusUnauthorized,
		},
		{
			name: "get job invalid job id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs/job", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "jobId": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.GetCaptionGenerationJob,
			status: http.StatusBadRequest,
		},
		{
			name: "list jobs invalid video id",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs", nil)
				req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
				return withSocialAuthUser(req)
			},
			call:   h.ListCaptionGenerationJobs,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w, tc.req())
			require.Equal(t, tc.status, w.Code)
		})
	}
}

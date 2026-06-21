package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ratingFakeRepo is an in-memory rating.Repository keyed by (user, video).
type ratingFakeRepo struct {
	ratings map[[2]uuid.UUID]string
}

func newRatingFakeRepo() *ratingFakeRepo {
	return &ratingFakeRepo{ratings: map[[2]uuid.UUID]string{}}
}

func (f *ratingFakeRepo) UpsertVideoRating(_ context.Context, a sqlcgen.UpsertVideoRatingParams) error {
	f.ratings[[2]uuid.UUID{a.UserID, a.VideoID}] = a.Rating
	return nil
}

func (f *ratingFakeRepo) DeleteVideoRating(_ context.Context, a sqlcgen.DeleteVideoRatingParams) error {
	delete(f.ratings, [2]uuid.UUID{a.UserID, a.VideoID})
	return nil
}

func (f *ratingFakeRepo) GetVideoRating(_ context.Context, a sqlcgen.GetVideoRatingParams) (string, error) {
	r, ok := f.ratings[[2]uuid.UUID{a.UserID, a.VideoID}]
	if !ok {
		return "", sqlNoRows{}
	}
	return r, nil
}

func (f *ratingFakeRepo) CountVideoRatings(_ context.Context, videoID uuid.UUID) (sqlcgen.CountVideoRatingsRow, error) {
	var row sqlcgen.CountVideoRatingsRow
	for k, v := range f.ratings {
		if k[1] != videoID {
			continue
		}
		switch v {
		case rating.Like:
			row.Likes++
		case rating.Dislike:
			row.Dislikes++
		}
	}
	return row, nil
}

type sqlNoRows struct{}

func (sqlNoRows) Error() string { return "no rows" }

func getRating(srv *Server, videoID, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/rating", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRatingSetChangeClear(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	parse := func(rec *httptest.ResponseRecorder) ratingView {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var v ratingView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return v
	}

	// Anonymous viewer: zero counts, no personal rating.
	anon := parse(getRating(srv, vid, ""))
	if anon.LikeCount != 0 || anon.MyRating != nil {
		t.Fatalf("anon rating = %+v", anon)
	}

	// Like.
	liked := parse(sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+vid+"/rating", `{"rating":"like"}`, tok))
	if liked.LikeCount != 1 || liked.DislikeCount != 0 || liked.MyRating == nil || *liked.MyRating != "like" {
		t.Fatalf("after like: %+v", liked)
	}

	// Change to dislike (replaces, not adds).
	disliked := parse(sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+vid+"/rating", `{"rating":"dislike"}`, tok))
	if disliked.LikeCount != 0 || disliked.DislikeCount != 1 || *disliked.MyRating != "dislike" {
		t.Fatalf("after change: %+v", disliked)
	}

	// Clear.
	cleared := parse(sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+vid+"/rating", "", tok))
	if cleared.LikeCount != 0 || cleared.DislikeCount != 0 || cleared.MyRating != nil {
		t.Fatalf("after clear: %+v", cleared)
	}
}

func TestRatingValidationAndAuth(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	// Invalid rating value → 422.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+vid+"/rating", `{"rating":"love"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid rating = %d, want 422", rec.Code)
	}
	// Rating requires auth.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+vid+"/rating", `{"rating":"like"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon rating = %d, want 401", rec.Code)
	}
}

func TestRatingOnNonPublicVideoIs404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createVideo(t, srv, tok, "ada", `{"title":"secret","privacy":"private"}`)

	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+vid+"/rating", `{"rating":"like"}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("rate non-public video = %d, want 404", rec.Code)
	}
	if rec := getRating(srv, vid, ""); rec.Code != http.StatusNotFound {
		t.Errorf("get non-public rating = %d, want 404", rec.Code)
	}
}

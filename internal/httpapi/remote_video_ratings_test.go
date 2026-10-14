package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// rvRatingRepo is fakeFedRepo plus one known remote video and a vote store.
type rvRatingRepo struct {
	fakeFedRepo
	videos remoteVideoFakeRepo
	votes  map[string]string
}

func (r rvRatingRepo) GetRemoteVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error) {
	return r.videos.GetRemoteVideoByID(ctx, id)
}

func (r rvRatingRepo) GetRemoteVideoUserRating(_ context.Context, a sqlcgen.GetRemoteVideoUserRatingParams) (string, error) {
	if v, ok := r.votes[a.UserID.String()+a.RemoteVideoID.String()]; ok {
		return v, nil
	}
	return "", pgx.ErrNoRows
}

func (r rvRatingRepo) UpsertRemoteVideoUserRating(_ context.Context, a sqlcgen.UpsertRemoteVideoUserRatingParams) error {
	r.votes[a.UserID.String()+a.RemoteVideoID.String()] = a.Rating
	return nil
}

func (r rvRatingRepo) DeleteRemoteVideoUserRating(_ context.Context, a sqlcgen.DeleteRemoteVideoUserRatingParams) (string, error) {
	k := a.UserID.String() + a.RemoteVideoID.String()
	v, ok := r.votes[k]
	if !ok {
		return "", pgx.ErrNoRows
	}
	delete(r.votes, k)
	return v, nil
}

func TestRemoteVideoRatingEndpoints(t *testing.T) {
	cfg := fedTestConfig()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	id := uuid.New()
	videos := remoteVideoFakeRepo{id: {ID: id, ObjectUrl: "https://remote.example/videos/watch/x", RemoteActorUrl: "https://remote.example/video-channels/movies"}}
	repo := rvRatingRepo{videos: videos, votes: map[string]string{}}
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))),
		WithRemoteVideoService(remotevideo.NewService(videos, nil)),
	)
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	path := "/api/v1/remote-videos/" + id.String() + "/rating"
	mine := func(t *testing.T, method, body string, want *string) {
		t.Helper()
		rec := sendJSONAuth(srv, method, path, body, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d; body=%s", method, rec.Code, rec.Body.String())
		}
		var v remoteVideoRatingView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		if (v.MyRating == nil) != (want == nil) || (want != nil && *v.MyRating != *want) {
			t.Fatalf("%s my_rating = %v, want %v", method, v.MyRating, want)
		}
	}
	like := "like"

	if rec := sendJSONAuth(srv, http.MethodPut, path, `{"rating":"like"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous PUT = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/remote-videos/"+uuid.NewString()+"/rating", `{"rating":"like"}`, tok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video PUT = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, path, `{"rating":"love"}`, tok); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("invalid rating PUT = %d, want 4xx", rec.Code)
	}
	mine(t, http.MethodGet, "", nil)
	mine(t, http.MethodPut, `{"rating":"like"}`, &like)
	mine(t, http.MethodGet, "", &like)
	mine(t, http.MethodDelete, "", nil)
	mine(t, http.MethodGet, "", nil)
}

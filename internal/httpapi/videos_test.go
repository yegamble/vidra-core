package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// videoFakeRepo is an in-memory video.Repository. It resolves a new video's
// owner from the shared channelFakeRepo so GetVideoByID can return owner_id.
type videoFakeRepo struct {
	channels *channelFakeRepo
	videos   map[uuid.UUID]sqlcgen.GetVideoByIDRow
}

func (f *videoFakeRepo) CreateVideo(_ context.Context, a sqlcgen.CreateVideoParams) (sqlcgen.Video, error) {
	var owner uuid.UUID
	for _, ch := range f.channels.byHandle {
		if ch.ID == a.ChannelID {
			owner = ch.OwnerID
		}
	}
	v := sqlcgen.Video{
		ID: uuid.New(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		OwnerID: owner,
	}
	return v, nil
}

func (f *videoFakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	return v, nil
}

func vidRowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (f *videoFakeRepo) ListVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, vidRowToVideo(r))
		}
	}
	return out, nil
}

func (f *videoFakeRepo) ListPublicVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" {
			out = append(out, vidRowToVideo(r))
		}
	}
	return out, nil
}

func (f *videoFakeRepo) UpdateVideo(_ context.Context, a sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	if a.Title != nil {
		r.Title = *a.Title
	}
	if a.Description != nil {
		r.Description = *a.Description
	}
	if a.Privacy != nil {
		r.Privacy = *a.Privacy
	}
	f.videos[a.ID] = r
	return vidRowToVideo(r), nil
}

func (f *videoFakeRepo) DeleteVideo(_ context.Context, id uuid.UUID) error {
	delete(f.videos, id)
	return nil
}

func videoServer(t *testing.T) *Server {
	t.Helper()
	chRepo := newChannelFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(channel.NewService(chRepo)),
		WithVideoService(video.NewService(&videoFakeRepo{channels: chRepo, videos: map[uuid.UUID]sqlcgen.GetVideoByIDRow{}})),
	)
}

// createChannelFor registers a user, creates a channel, and returns (token, handle).
func createChannelFor(t *testing.T, srv *Server, username, email, handle string) string {
	t.Helper()
	tok := registerAndToken(t, srv, `{"username":"`+username+`","email":"`+email+`","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"`+handle+`","display_name":"`+handle+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel %s = %d; body=%s", handle, rec.Code, rec.Body.String())
	}
	return tok
}

func TestCreateVideoRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	rec := postTo(srv, "/api/v1/channels/ada/videos", `{"title":"Hi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateVideoValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":""}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestCreateVideoNonOwnerForbidden(t *testing.T) {
	srv := videoServer(t)
	_ = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"Hi"}`, otherTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateVideoUnknownChannel404(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels/ghost/videos", `{"title":"Hi"}`, tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCreateVideoDefaultsPrivate(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"My Draft"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Privacy != "private" || v.State != "draft" {
		t.Errorf("unexpected video: %+v", v)
	}
}

// createVideo returns the created video's id.
func createVideo(t *testing.T, srv *Server, token, handle, body string) string {
	t.Helper()
	rec := postJSONAuth(srv, "/api/v1/channels/"+handle+"/videos", body, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create video = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v.ID
}

func getVideo(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestGetPublicVideoIsAnonymous(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Public","privacy":"public"}`)

	if rec := getVideo(srv, id, ""); rec.Code != http.StatusOK {
		t.Fatalf("anon get public = %d, want 200", rec.Code)
	}
}

func TestGetPrivateVideoOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret","privacy":"private"}`)

	if rec := getVideo(srv, id, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner get private = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Hidden as 404 (not 403) from anon and non-owners.
	if rec := getVideo(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("anon get private = %d, want 404", rec.Code)
	}
	if rec := getVideo(srv, id, otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner get private = %d, want 404", rec.Code)
	}
}

func TestGetVideoNotFoundAndMalformed(t *testing.T) {
	srv := videoServer(t)
	if rec := getVideo(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", rec.Code)
	}
	if rec := getVideo(srv, "not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id = %d, want 404", rec.Code)
	}
}

func TestUpdateVideoOwnerAndNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"old","privacy":"private"}`)

	// Owner update.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"title":"new","privacy":"public"}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Title != "new" || v.Privacy != "public" {
		t.Errorf("unexpected video: %+v", v)
	}
	// Non-owner -> 404 (existence not leaked).
	if bad := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"title":"hax"}`, otherTok); bad.Code != http.StatusNotFound {
		t.Fatalf("non-owner update = %d, want 404", bad.Code)
	}
	// Empty patch -> 422.
	if empty := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{}`, ownerTok); empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", empty.Code)
	}
}

func TestDeleteVideoOwnerAndNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"t","privacy":"public"}`)

	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+id, "", otherTok); bad.Code != http.StatusNotFound {
		t.Fatalf("non-owner delete = %d, want 404", bad.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+id, "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d, want 204", rec.Code)
	}
	if get := getVideo(srv, id, ownerTok); get.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", get.Code)
	}
}

func TestListChannelVideosOwnerVsPublic(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = createVideo(t, srv, ownerTok, "ada", `{"title":"pub","privacy":"public"}`)
	_ = createVideo(t, srv, ownerTok, "ada", `{"title":"priv","privacy":"private"}`)

	list := func(tok string) videoListResponse {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada/videos", nil)
		if tok != "" {
			req.Header.Set("authorization", "Bearer "+tok)
		}
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body videoListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	if owner := list(ownerTok); len(owner.Videos) != 2 {
		t.Errorf("owner list = %d, want 2", len(owner.Videos))
	}
	if anon := list(""); len(anon.Videos) != 1 || anon.Videos[0].Privacy != "public" {
		t.Errorf("anon list = %+v, want 1 public", anon.Videos)
	}
	if other := list(otherTok); len(other.Videos) != 1 {
		t.Errorf("non-owner list = %d, want 1 (public only)", len(other.Videos))
	}
}

func TestListChannelVideosUnknownChannel404(t *testing.T) {
	srv := videoServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ghost/videos", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

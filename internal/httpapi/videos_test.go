package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// videoFakeRepo is an in-memory video.Repository. It resolves a new video's
// owner from the shared channelFakeRepo so GetVideoByID can return owner_id.
type videoFakeRepo struct {
	channels *channelFakeRepo
	videos   map[uuid.UUID]sqlcgen.GetVideoByIDRow
	files    map[uuid.UUID][]sqlcgen.VideoFile
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
		if r.ChannelID == channelID && r.Privacy == "public" && r.State == "published" {
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

func (f *videoFakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	if f.files == nil {
		f.files = map[uuid.UUID][]sqlcgen.VideoFile{}
	}
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
		CreatedAt: time.Now(),
	}
	f.files[a.VideoID] = append(f.files[a.VideoID], vf)
	return vf, nil
}

func (f *videoFakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	kept := f.files[a.VideoID][:0]
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind != a.Kind {
			kept = append(kept, vf)
		}
	}
	f.files[a.VideoID] = kept
	return nil
}

func (f *videoFakeRepo) SetVideoState(_ context.Context, a sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	r.State = a.State
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return vidRowToVideo(r), nil
}

func (f *videoFakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.Video, error) {
	q := ""
	if a.Query != nil {
		q = strings.ToLower(*a.Query)
	}
	var all []sqlcgen.Video
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && strings.Contains(strings.ToLower(r.Title), q) {
			all = append(all, vidRowToVideo(r))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	lo := int(a.ResultOffset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func (f *videoFakeRepo) ListPublicVideos(_ context.Context, a sqlcgen.ListPublicVideosParams) ([]sqlcgen.Video, error) {
	var all []sqlcgen.Video
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" {
			all = append(all, vidRowToVideo(r))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	lo := int(a.Offset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(a.Limit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func videoServer(t *testing.T) *Server { return videoServerCfg(t, testConfig()) }

func videoServerCfg(t *testing.T, cfg *config.Config, opts ...video.Option) *Server {
	t.Helper()
	chRepo := newChannelFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	repo := &videoFakeRepo{
		channels: chRepo,
		videos:   map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		files:    map[uuid.UUID][]sqlcgen.VideoFile{},
	}
	return New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(channel.NewService(chRepo)),
		WithVideoService(video.NewService(repo, blobs, opts...)),
	)
}

// fakeProber lets handler tests drive the publish/fail outcome of an upload.
type fakeProber struct{ err error }

func (p fakeProber) Probe(_ context.Context, _ string) error { return p.err }

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

// createPublishedVideo creates a video and uploads a tiny original so it lands
// published (the default harness has no prober, so Process publishes directly).
// Only published videos appear on the public discovery surfaces.
func createPublishedVideo(t *testing.T, srv *Server, token, handle, body string) string {
	t.Helper()
	id := createVideo(t, srv, token, handle, body)
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	return id
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
	_ = createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"pub","privacy":"public"}`)
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

func TestPublicVideoFeed(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"p1","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"p2","privacy":"public"}`)
	_ = createVideo(t, srv, tok, "ada", `{"title":"secret","privacy":"private"}`)

	feed := func(query string) videoFeedResponse {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("feed%s = %d, want 200; body=%s", query, rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	// Anonymous feed shows only the 2 public videos.
	all := feed("")
	if len(all.Videos) != 2 || all.Limit != 20 || all.Offset != 0 {
		t.Fatalf("default feed = %+v, want 2 videos, limit 20, offset 0", all)
	}
	for _, v := range all.Videos {
		if v.Privacy != "public" {
			t.Errorf("feed leaked non-public video: %+v", v)
		}
	}

	// Pagination: limit clamps, offset advances.
	page1 := feed("?limit=1&offset=0")
	page2 := feed("?limit=1&offset=1")
	page3 := feed("?limit=1&offset=2")
	if len(page1.Videos) != 1 || page1.Limit != 1 {
		t.Errorf("page1 = %+v, want 1 video, limit 1", page1)
	}
	if len(page2.Videos) != 1 {
		t.Errorf("page2 = %d videos, want 1", len(page2.Videos))
	}
	if len(page3.Videos) != 0 {
		t.Errorf("page3 = %d videos, want 0 (only 2 public)", len(page3.Videos))
	}
	if page1.Videos[0].ID == page2.Videos[0].ID {
		t.Error("pages returned the same video")
	}

	// Over-max limit is clamped to 100.
	if huge := feed("?limit=99999"); huge.Limit != 100 {
		t.Errorf("limit clamp = %d, want 100", huge.Limit)
	}
}

func TestSearchVideos(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"Go concurrency patterns","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"Rust ownership","privacy":"public"}`)
	_ = createVideo(t, srv, tok, "ada", `{"title":"Go generics secret","privacy":"private"}`)

	search := func(query string) (int, videoSearchResponse) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/search"+query, nil))
		var body videoSearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	// Missing q -> 400.
	if code, _ := search(""); code != http.StatusBadRequest {
		t.Fatalf("missing q = %d, want 400", code)
	}

	// "go" matches the public Go video but not the private one.
	code, body := search("?q=go")
	if code != http.StatusOK {
		t.Fatalf("search = %d, want 200", code)
	}
	if body.Query != "go" || len(body.Videos) != 1 {
		t.Fatalf("search result = %+v, want 1 public match", body)
	}
	if body.Videos[0].Title != "Go concurrency patterns" {
		t.Errorf("matched %q, want the public Go video", body.Videos[0].Title)
	}

	// No matches -> empty.
	if _, none := search("?q=kubernetes"); len(none.Videos) != 0 {
		t.Errorf("no-match search = %+v, want empty", none.Videos)
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

// uploadVideoFile POSTs a multipart "file" field to the upload endpoint.
func uploadVideoFile(srv *Server, id, filename, contentType, content, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, _ := w.CreatePart(hdr)
	_, _ = part.Write([]byte(content))
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/file", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUploadVideoFileRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	rec := uploadVideoFile(srv, uuid.New().String(), "clip.mp4", "video/mp4", "bytes", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUploadVideoFileStoresAndPublishes(t *testing.T) {
	srv := videoServer(t) // no prober configured -> the original is published directly
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	const content = "pretend this is an mp4"
	rec := uploadVideoFile(srv, id, "Clip.MP4", "video/mp4", content, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Video.State != "published" {
		t.Errorf("state = %q, want published", resp.Video.State)
	}
	if resp.File.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", resp.File.SizeBytes, len(content))
	}
	if resp.File.Kind != "original" || resp.File.ContentType != "video/mp4" || resp.File.OriginalName != "Clip.MP4" {
		t.Errorf("unexpected file: %+v", resp.File)
	}

	// The video reports published on a fresh read, too.
	got := getVideo(srv, id, tok)
	var v videoView
	_ = json.Unmarshal(got.Body.Bytes(), &v)
	if v.State != "published" {
		t.Errorf("refetched state = %q, want published", v.State)
	}
}

func TestUploadVideoFileProbeFailureMarksFailed(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithProber(fakeProber{err: errors.New("corrupt media")}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "not really a video", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Video.State != "failed" {
		t.Errorf("state = %q, want failed (probe rejected the file)", resp.Video.State)
	}
}

func TestUploadVideoFileMissingField(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/file", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+tok)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadVideoFileNonOwner404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "bytes", otherTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner upload = %d, want 404", rec.Code)
	}
}

func TestUploadVideoFileUnknownVideo404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := uploadVideoFile(srv, uuid.New().String(), "clip.mp4", "video/mp4", "bytes", tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video upload = %d, want 404", rec.Code)
	}
}

func TestUploadVideoFileUnsupportedExtension(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	rec := uploadVideoFile(srv, id, "notes.pdf", "application/pdf", "not a video", tok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadVideoFileTooLarge(t *testing.T) {
	srv := videoServer(t) // UploadMaxSize is 64K in testConfig
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	big := strings.Repeat("x", 80*1024) // 80K > 64K cap
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", big, tok)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadRouteBypassesJSONBodyLimit(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPBodyLimit = "2K"   // tiny JSON cap
	cfg.UploadMaxSize = "256K" // generous upload cap
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	// An upload above the JSON limit but under the upload cap succeeds — proving
	// the upload route is exempt from the small default body limit.
	body := strings.Repeat("x", 8*1024) // 8K > 2K JSON cap, < 256K upload cap
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", body, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("large upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// ...and the JSON API is still capped by the small default limit.
	bigJSON := `{"title":"` + strings.Repeat("a", 3*1024) + `"}`
	rec = postJSONAuth(srv, "/api/v1/channels/ada/videos", bigJSON, tok)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

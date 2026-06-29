package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// playlistFakeRepo is an in-memory playlist.Repository that resolves item card
// columns from the shared videoFakeRepo, mirroring the real join.
type playlistFakeRepo struct {
	videos    *videoFakeRepo
	playlists map[uuid.UUID]sqlcgen.Playlist
	items     map[uuid.UUID][]uuid.UUID
}

// pubPub returns the video if it exists and is public + published.
func (f *playlistFakeRepo) pubPub(vid uuid.UUID) (sqlcgen.GetVideoByIDRow, bool) {
	v, ok := f.videos.videos[vid]
	if !ok || v.Privacy != "public" || v.State != "published" {
		return sqlcgen.GetVideoByIDRow{}, false
	}
	return v, true
}

func (f *playlistFakeRepo) publicCount(playlistID uuid.UUID) int64 {
	var n int64
	for _, vid := range f.items[playlistID] {
		if _, ok := f.pubPub(vid); ok {
			n++
		}
	}
	return n
}

func (f *playlistFakeRepo) CreatePlaylist(_ context.Context, a sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error) {
	p := sqlcgen.Playlist{
		ID: uuid.New(), OwnerID: a.OwnerID, Title: a.Title, Description: a.Description,
		Visibility: a.Visibility, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.playlists[p.ID] = p
	return p, nil
}

func (f *playlistFakeRepo) GetPlaylistByID(_ context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error) {
	p, ok := f.playlists[id]
	if !ok {
		return sqlcgen.GetPlaylistByIDRow{}, context.Canceled
	}
	return sqlcgen.GetPlaylistByIDRow{
		ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
		Visibility: p.Visibility, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		VideoCount: f.publicCount(id),
	}, nil
}

func (f *playlistFakeRepo) ListPlaylistsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	var rows []sqlcgen.ListPlaylistsByOwnerRow
	for _, p := range f.playlists {
		if p.OwnerID == ownerID {
			rows = append(rows, sqlcgen.ListPlaylistsByOwnerRow{
				ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
				Visibility: p.Visibility, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
				VideoCount: f.publicCount(p.ID),
			})
		}
	}
	return rows, nil
}

func (f *playlistFakeRepo) UpdatePlaylist(_ context.Context, a sqlcgen.UpdatePlaylistParams) (sqlcgen.Playlist, error) {
	p := f.playlists[a.ID]
	if a.Title != nil {
		p.Title = *a.Title
	}
	if a.Description != nil {
		p.Description = *a.Description
	}
	if a.Visibility != nil {
		p.Visibility = *a.Visibility
	}
	p.UpdatedAt = time.Now()
	f.playlists[a.ID] = p
	return p, nil
}

func (f *playlistFakeRepo) DeletePlaylist(_ context.Context, id uuid.UUID) error {
	delete(f.playlists, id)
	delete(f.items, id)
	return nil
}

func (f *playlistFakeRepo) AddPlaylistItem(_ context.Context, a sqlcgen.AddPlaylistItemParams) error {
	for _, v := range f.items[a.PlaylistID] {
		if v == a.VideoID {
			return nil
		}
	}
	f.items[a.PlaylistID] = append(f.items[a.PlaylistID], a.VideoID)
	return nil
}

func (f *playlistFakeRepo) RemovePlaylistItem(_ context.Context, a sqlcgen.RemovePlaylistItemParams) error {
	cur := f.items[a.PlaylistID]
	out := cur[:0:0]
	for _, v := range cur {
		if v != a.VideoID {
			out = append(out, v)
		}
	}
	f.items[a.PlaylistID] = out
	return nil
}

func (f *playlistFakeRepo) ListPlaylistItems(_ context.Context, playlistID uuid.UUID) ([]sqlcgen.ListPlaylistItemsRow, error) {
	var rows []sqlcgen.ListPlaylistItemsRow
	for _, vid := range f.items[playlistID] {
		v, ok := f.pubPub(vid)
		if !ok {
			continue
		}
		handle, name := f.videos.channelInfo(v.ChannelID)
		rows = append(rows, sqlcgen.ListPlaylistItemsRow{
			ID: v.ID, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
			Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
			Views: f.videos.views[v.ID], HasThumbnail: f.videos.hasThumb(v.ID),
			ChannelHandle: handle, ChannelDisplayName: name,
		})
	}
	return rows, nil
}

func createPlaylist(t *testing.T, srv *Server, token, body string) playlistView {
	t.Helper()
	rec := postJSONAuth(srv, "/api/v1/playlists", body, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create playlist = %d; body=%s", rec.Code, rec.Body.String())
	}
	var p playlistView
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p
}

func getPlaylist(srv *Server, id, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+id, "", token)
}

func TestPlaylistCRUDAndItems(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if pl.VideoCount != 0 || pl.Visibility != "public" {
		t.Fatalf("new playlist = %+v, want count 0 / public", pl)
	}

	// Add the video (idempotent).
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+vid+`"}`, tok); rec.Code != http.StatusNoContent {
			t.Fatalf("add item = %d; body=%s", rec.Code, rec.Body.String())
		}
	}

	// Detail (public, no auth) shows the card + count.
	var detail playlistDetailResponse
	rec := getPlaylist(srv, pl.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get playlist = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.VideoCount != 1 || len(detail.Videos) != 1 || detail.Videos[0].ID != vid || detail.Videos[0].Title != "Clip" {
		t.Fatalf("detail = %+v, want 1 video 'Clip'", detail)
	}
	if detail.Videos[0].ChannelHandle == nil || *detail.Videos[0].ChannelHandle != "ada" {
		t.Errorf("card missing channel handle: %+v", detail.Videos[0])
	}

	// List my playlists.
	var list playlistListResponse
	lrec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/playlists", "", tok)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list.Playlists) != 1 || list.Playlists[0].VideoCount != 1 {
		t.Fatalf("my playlists = %+v, want 1 with count 1", list.Playlists)
	}

	// Update (rename).
	urec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+pl.ID, `{"title":"Renamed"}`, tok)
	if urec.Code != http.StatusOK {
		t.Fatalf("update = %d; body=%s", urec.Code, urec.Body.String())
	}
	var updated playlistView
	_ = json.Unmarshal(urec.Body.Bytes(), &updated)
	if updated.Title != "Renamed" || updated.VideoCount != 1 {
		t.Errorf("updated = %+v, want title Renamed / count 1", updated)
	}

	// Remove the item.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/videos/"+vid, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("remove item = %d", rec.Code)
	}
	detail = playlistDetailResponse{}
	_ = json.Unmarshal(getPlaylist(srv, pl.ID, "").Body.Bytes(), &detail)
	if detail.VideoCount != 0 || len(detail.Videos) != 0 {
		t.Errorf("after remove = %+v, want empty", detail)
	}

	// Delete the playlist → gone.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := getPlaylist(srv, pl.ID, tok); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestPlaylistVisibility(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	priv := createPlaylist(t, srv, tok, `{"title":"Secret","visibility":"private"}`)
	pub := createPlaylist(t, srv, tok, `{"title":"Open","visibility":"public"}`)

	// Private: hidden from anon (404), visible to owner.
	if rec := getPlaylist(srv, priv.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon get private = %d, want 404", rec.Code)
	}
	if rec := getPlaylist(srv, priv.ID, tok); rec.Code != http.StatusOK {
		t.Errorf("owner get private = %d, want 200", rec.Code)
	}
	// Public: visible to anon.
	if rec := getPlaylist(srv, pub.ID, ""); rec.Code != http.StatusOK {
		t.Errorf("anon get public = %d, want 200", rec.Code)
	}
}

func TestPlaylistOwnerGating(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, ownerTok, `{"title":"Mine"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+pl.ID, `{"title":"Hax"}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner update = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID, "", otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner delete = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+uuid.New().String()+`"}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner add = %d, want 404", rec.Code)
	}
}

func TestPlaylistAddNonPublicVideoIs404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	draft := createVideo(t, srv, tok, "ada", `{"title":"draft","privacy":"private"}`)
	pl := createPlaylist(t, srv, tok, `{"title":"P"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+draft+`"}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("add non-public video = %d, want 404", rec.Code)
	}
}

func TestPlaylistValidationAndAuth(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := postJSONAuth(srv, "/api/v1/playlists", `{"title":""}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank title = %d, want 422", rec.Code)
	}

	someID := uuid.New().String()
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/playlists", `{"title":"x"}`},
		{http.MethodGet, "/api/v1/me/playlists", ""},
		{http.MethodPatch, "/api/v1/playlists/" + someID, `{"title":"x"}`},
		{http.MethodDelete, "/api/v1/playlists/" + someID, ""},
		{http.MethodPost, "/api/v1/playlists/" + someID + "/videos", `{"video_id":"` + someID + `"}`},
		{http.MethodDelete, "/api/v1/playlists/" + someID + "/videos/" + someID, ""},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

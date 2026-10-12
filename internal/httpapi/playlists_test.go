package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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
		Visibility: a.Visibility, ChannelID: a.ChannelID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
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
		Visibility: p.Visibility, ThumbnailExt: p.ThumbnailExt, ChannelID: p.ChannelID, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		VideoCount: f.publicCount(id),
	}, nil
}

func (f *playlistFakeRepo) GetChannelByID(ctx context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	return f.videos.GetChannelByID(ctx, id)
}

// channelVisible mirrors the ListPlaylistsByChannel predicate.
func (f *playlistFakeRepo) channelVisible(a sqlcgen.CountPlaylistsByChannelParams) []sqlcgen.Playlist {
	var out []sqlcgen.Playlist
	for _, p := range f.playlists {
		if !p.ChannelID.Valid || uuid.UUID(p.ChannelID.Bytes) != a.ChannelID {
			continue
		}
		if f.videos.mutedFromFeed(a.ViewerID, uuid.UUID(p.ChannelID.Bytes)) {
			continue
		}
		if p.Visibility == "public" || (a.ViewerID.Valid && uuid.UUID(a.ViewerID.Bytes) == p.OwnerID) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (f *playlistFakeRepo) CountPlaylistsByChannel(_ context.Context, a sqlcgen.CountPlaylistsByChannelParams) (int64, error) {
	return int64(len(f.channelVisible(a))), nil
}

func (f *playlistFakeRepo) ListPlaylistsByChannel(_ context.Context, a sqlcgen.ListPlaylistsByChannelParams) ([]sqlcgen.ListPlaylistsByChannelRow, error) {
	var rows []sqlcgen.ListPlaylistsByChannelRow
	for _, p := range f.channelVisible(sqlcgen.CountPlaylistsByChannelParams{ChannelID: a.ChannelID, ViewerID: a.ViewerID}) {
		rows = append(rows, sqlcgen.ListPlaylistsByChannelRow{
			ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
			Visibility: p.Visibility, ChannelID: p.ChannelID, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
			VideoCount: f.publicCount(p.ID),
		})
	}
	lo := min(int(a.ResultOffset), len(rows))
	hi := min(lo+int(a.ResultLimit), len(rows))
	return rows[lo:hi], nil
}

func (f *playlistFakeRepo) ListPlaylistsByOwner(_ context.Context, a sqlcgen.ListPlaylistsByOwnerParams) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	ownerID := a.OwnerID
	var rows []sqlcgen.ListPlaylistsByOwnerRow
	for _, p := range f.playlists {
		if p.OwnerID == ownerID {
			rows = append(rows, sqlcgen.ListPlaylistsByOwnerRow{
				ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
				Visibility: p.Visibility, ThumbnailExt: p.ThumbnailExt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
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
	if a.SetChannel {
		p.ChannelID = a.ChannelID
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

func (f *playlistFakeRepo) ListPlaylistItems(_ context.Context, a sqlcgen.ListPlaylistItemsParams) ([]sqlcgen.ListPlaylistItemsRow, error) {
	playlistID := a.PlaylistID
	var rows []sqlcgen.ListPlaylistItemsRow
	for _, vid := range f.items[playlistID] {
		v, ok := f.pubPub(vid)
		if !ok {
			continue
		}
		handle, name := f.videos.channelInfo(v.ChannelID)
		rows = append(rows, sqlcgen.ListPlaylistItemsRow{
			ID: v.ID, ShortCode: v.ShortCode, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
			Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
			Views: f.videos.views[v.ID], HasThumbnail: f.videos.hasThumb(v.ID),
			ChannelHandle: handle, ChannelDisplayName: name,
		})
	}
	return rows, nil
}

func (f *playlistFakeRepo) ListPlaylistItemVideoIDs(_ context.Context, playlistID uuid.UUID) ([]uuid.UUID, error) {
	return append([]uuid.UUID(nil), f.items[playlistID]...), nil
}

func (f *playlistFakeRepo) ReorderPlaylistItems(_ context.Context, a sqlcgen.ReorderPlaylistItemsParams) error {
	f.items[a.PlaylistID] = append([]uuid.UUID(nil), a.VideoIds...)
	return nil
}

// MovePlaylistItem mirrors the SQL over ALL items (visible or not): 0 rows when
// the video or the anchor is not an item; otherwise the video lands directly
// after the anchor, or at the front when the anchor is NULL.
func (f *playlistFakeRepo) MovePlaylistItem(_ context.Context, a sqlcgen.MovePlaylistItemParams) (int64, error) {
	cur, after := f.items[a.PlaylistID], uuid.UUID(a.AfterVideoID.Bytes)
	var out []uuid.UUID
	if !a.AfterVideoID.Valid {
		out = append(out, a.VideoID)
	}
	found, anchored := false, !a.AfterVideoID.Valid
	for _, v := range cur {
		if v == a.VideoID {
			found = true
			continue
		}
		out = append(out, v)
		if a.AfterVideoID.Valid && v == after {
			out, anchored = append(out, a.VideoID), true
		}
	}
	if !found || !anchored {
		return 0, nil
	}
	f.items[a.PlaylistID] = out
	return int64(len(out)), nil
}

func (f *playlistFakeRepo) SetPlaylistThumbnail(_ context.Context, a sqlcgen.SetPlaylistThumbnailParams) error {
	p := f.playlists[a.ID]
	p.ThumbnailExt = a.ThumbnailExt
	f.playlists[a.ID] = p
	return nil
}

func (f *playlistFakeRepo) ClearPlaylistThumbnail(_ context.Context, id uuid.UUID) error {
	p := f.playlists[id]
	p.ThumbnailExt = nil
	f.playlists[id] = p
	return nil
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
		{http.MethodPut, "/api/v1/playlists/" + someID + "/videos", `{"video_ids":["` + someID + `"]}`},
		{http.MethodDelete, "/api/v1/playlists/" + someID + "/videos/" + someID, ""},
		{http.MethodPost, "/api/v1/playlists/" + someID + "/videos/" + someID + "/move", `{"after_video_id":null}`},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestPlaylistReorder(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	v1 := createPublishedVideo(t, srv, tok, "ada", `{"title":"One","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok, "ada", `{"title":"Two","privacy":"public"}`)
	v3 := createPublishedVideo(t, srv, tok, "ada", `{"title":"Three","privacy":"public"}`)

	pl := createPlaylist(t, srv, tok, `{"title":"Mix","visibility":"public"}`)
	for _, v := range []string{v1, v2, v3} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+v+`"}`, tok); rec.Code != http.StatusNoContent {
			t.Fatalf("add %s = %d", v, rec.Code)
		}
	}

	reorder := func(body, token string) *httptest.ResponseRecorder {
		return sendJSONAuth(srv, http.MethodPut, "/api/v1/playlists/"+pl.ID+"/videos", body, token)
	}

	// Happy path: reverse the order → 204, and the detail reflects the new order.
	if rec := reorder(`{"video_ids":["`+v3+`","`+v2+`","`+v1+`"]}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder = %d; body=%s", rec.Code, rec.Body.String())
	}
	var detail playlistDetailResponse
	_ = json.Unmarshal(getPlaylist(srv, pl.ID, "").Body.Bytes(), &detail)
	if len(detail.Videos) != 3 || detail.Videos[0].ID != v3 || detail.Videos[1].ID != v2 || detail.Videos[2].ID != v1 {
		t.Fatalf("order after reorder = %+v, want [v3 v2 v1]", detail.Videos)
	}

	// A non-permutation (missing an id) → 422.
	if rec := reorder(`{"video_ids":["`+v1+`","`+v2+`"]}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing id = %d, want 422", rec.Code)
	}
	// A malformed uuid → 422 (body validation).
	if rec := reorder(`{"video_ids":["not-a-uuid"]}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad uuid = %d, want 422", rec.Code)
	}
	// A non-owner sees 404 (existence hidden), even with a valid permutation.
	otherTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	if rec := reorder(`{"video_ids":["`+v3+`","`+v2+`","`+v1+`"]}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner = %d, want 404", rec.Code)
	}
	// Anonymous → 401.
	if rec := reorder(`{"video_ids":["`+v1+`"]}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
}

// TestPlaylistMoveItem proves the single-item move: it needs no copy of the
// full list, anchors on a neighbour (or the front), and answers 409 for a stale
// video/anchor, 422 for a malformed or self anchor, 404 for a non-owner.
func TestPlaylistMoveItem(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	v1 := createPublishedVideo(t, srv, tok, "ada", `{"title":"One","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok, "ada", `{"title":"Two","privacy":"public"}`)
	v3 := createPublishedVideo(t, srv, tok, "ada", `{"title":"Three","privacy":"public"}`)
	pl := createPlaylist(t, srv, tok, `{"title":"Mix","visibility":"private"}`)
	for _, v := range []string{v1, v2, v3} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+v+`"}`, tok); rec.Code != http.StatusNoContent {
			t.Fatalf("add %s = %d", v, rec.Code)
		}
	}
	move := func(video, body, token string) *httptest.ResponseRecorder {
		return sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos/"+video+"/move", body, token)
	}
	order := func() []string {
		var d playlistDetailResponse
		_ = json.Unmarshal(getPlaylist(srv, pl.ID, tok).Body.Bytes(), &d)
		ids := make([]string, 0, len(d.Videos))
		for _, v := range d.Videos {
			ids = append(ids, v.ID)
		}
		return ids
	}
	expectOrder := func(step string, want ...string) {
		t.Helper()
		if got := order(); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: order = %v, want %v", step, got, want)
		}
	}

	if rec := move(v3, `{"after_video_id":null}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("move to front = %d; body=%s", rec.Code, rec.Body.String())
	}
	expectOrder("to front", v3, v1, v2)
	if rec := move(v3, `{"after_video_id":"`+v2+`"}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("move after v2 = %d; body=%s", rec.Code, rec.Body.String())
	}
	expectOrder("after v2", v1, v2, v3)
	if rec := move(v2, `{}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("omitted anchor = %d; body=%s", rec.Code, rec.Body.String())
	}
	expectOrder("omitted anchor moves to front", v2, v1, v3)

	stray := uuid.NewString()
	for name, tc := range map[string]struct {
		video, body string
		want        int
	}{
		"stray video":    {stray, `{"after_video_id":null}`, http.StatusConflict},
		"stray anchor":   {v1, `{"after_video_id":"` + stray + `"}`, http.StatusConflict},
		"self anchor":    {v1, `{"after_video_id":"` + v1 + `"}`, http.StatusUnprocessableEntity},
		"bad anchor":     {v1, `{"after_video_id":"nope"}`, http.StatusUnprocessableEntity},
		"bad video path": {"nope", `{}`, http.StatusNotFound},
	} {
		if rec := move(tc.video, tc.body, tok); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d; body=%s", name, rec.Code, tc.want, rec.Body.String())
		}
	}
	expectOrder("after rejected moves", v2, v1, v3)

	// A non-owner of the private playlist sees 404, the same as a missing one.
	otherTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	if rec := move(v1, `{}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+uuid.NewString()+"/videos/"+v1+"/move", `{}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown playlist = %d, want 404", rec.Code)
	}
	expectOrder("after non-owner move", v2, v1, v3)
}

// TestPlaylistCarriesOwnerID proves every playlist shape names its owner, so a
// client decides owner controls from the playlist itself rather than by
// searching one page of its own playlists.
func TestPlaylistCarriesOwnerID(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok).Body.Bytes(), &me)
	if me.ID == "" {
		t.Fatal("could not read the caller's id")
	}
	pl := createPlaylist(t, srv, tok, `{"title":"Mine","visibility":"public"}`)
	if pl.OwnerID != me.ID {
		t.Errorf("create owner_id = %q, want %q", pl.OwnerID, me.ID)
	}
	var detail playlistDetailResponse
	_ = json.Unmarshal(getPlaylist(srv, pl.ID, "").Body.Bytes(), &detail)
	if detail.OwnerID != me.ID {
		t.Errorf("anonymous get owner_id = %q, want %q", detail.OwnerID, me.ID)
	}
	var list playlistListResponse
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/me/playlists", "", tok).Body.Bytes(), &list)
	if len(list.Playlists) != 1 || list.Playlists[0].OwnerID != me.ID {
		t.Errorf("list owner_id = %+v, want %q", list.Playlists, me.ID)
	}
	var updated playlistView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+pl.ID, `{"title":"Renamed"}`, tok).Body.Bytes(), &updated)
	if updated.OwnerID != me.ID {
		t.Errorf("update owner_id = %q, want %q", updated.OwnerID, me.ID)
	}
}

func (f *playlistFakeRepo) CountPlaylistsByOwner(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	rows, err := f.ListPlaylistsByOwner(ctx, sqlcgen.ListPlaylistsByOwnerParams{OwnerID: ownerID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *playlistFakeRepo) CountPlaylistItems(ctx context.Context, playlistID uuid.UUID) (int64, error) {
	rows, err := f.ListPlaylistItems(ctx, sqlcgen.ListPlaylistItemsParams{PlaylistID: playlistID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func listChannelPlaylists(t *testing.T, srv *Server, handle, query, token string) (playlistListResponse, int) {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/"+handle+"/playlists"+query, "", token)
	var out playlistListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func TestChannelPlaylists(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	adaCh, bobCh := channelIDOf(t, srv, "ada"), channelIDOf(t, srv, "bob")

	pub := createPlaylist(t, srv, ada, `{"title":"Pub","visibility":"public","channel_id":"`+adaCh+`"}`)
	unl := createPlaylist(t, srv, ada, `{"title":"Unl","visibility":"unlisted","channel_id":"`+adaCh+`"}`)
	priv := createPlaylist(t, srv, ada, `{"title":"Priv","visibility":"private","channel_id":"`+adaCh+`"}`)
	createPlaylist(t, srv, ada, `{"title":"NoChannel","visibility":"public"}`)
	if pub.ChannelID == nil || *pub.ChannelID != adaCh {
		t.Fatalf("create did not echo channel_id: %+v", pub)
	}

	// Anonymous and other users: public only. Never unlisted or private.
	for name, tok := range map[string]string{"anonymous": "", "other user": bob} {
		got, code := listChannelPlaylists(t, srv, "ada", "", tok)
		if code != http.StatusOK || len(got.Playlists) != 1 || got.Playlists[0].ID != pub.ID || got.Total != 1 {
			t.Errorf("%s sees %+v (code %d, total %d), want only the public playlist", name, got.Playlists, code, got.Total)
		}
	}
	// Owner: all three, newest first; pagination follows the shared convention.
	got, _ := listChannelPlaylists(t, srv, "ada", "", ada)
	if got.Total != 3 || len(got.Playlists) != 3 || got.Playlists[0].ID != priv.ID || got.Playlists[2].ID != pub.ID {
		t.Errorf("owner sees %+v total %d, want 3 newest-first", got.Playlists, got.Total)
	}
	_ = unl
	page, _ := listChannelPlaylists(t, srv, "ada", "?limit=1&offset=1", ada)
	if len(page.Playlists) != 1 || page.Total != 3 || page.Playlists[0].ID != unl.ID {
		t.Errorf("page = %+v total %d, want the middle playlist of 3", page.Playlists, page.Total)
	}
	if _, code := listChannelPlaylists(t, srv, "nobody", "", ""); code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", code)
	}

	// A viewer who blocked the owner sees none of their playlists.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+ownerIDOf(t, srv, ada), "", bob); rec.Code >= 300 {
		t.Fatalf("block owner = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := listChannelPlaylists(t, srv, "ada", "", bob); len(got.Playlists) != 0 || got.Total != 0 {
		t.Errorf("a viewer who blocked the owner sees %+v", got.Playlists)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+ownerIDOf(t, srv, ada), "", bob); rec.Code >= 300 {
		t.Fatalf("unblock owner = %d", rec.Code)
	}

	// Another user cannot attach a playlist to my channel (create or update);
	// the answer is the same 404 as a channel that does not exist.
	if rec := postJSONAuth(srv, "/api/v1/playlists", `{"title":"Hijack","visibility":"public","channel_id":"`+adaCh+`"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("create on another user's channel = %d, want 404", rec.Code)
	}
	bobPl := createPlaylist(t, srv, bob, `{"title":"Bob","visibility":"public"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"channel_id":"`+adaCh+`"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("update to another user's channel = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"channel_id":"`+uuid.NewString()+`"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("update to a missing channel = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"channel_id":"nope"}`, bob); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("malformed channel_id = %d, want 422", rec.Code)
	}
	if got, _ := listChannelPlaylists(t, srv, "ada", "", ""); len(got.Playlists) != 1 {
		t.Errorf("a refused attach leaked into the channel list: %+v", got.Playlists)
	}

	// Own channel works; null clears; omitting the key leaves it alone.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"channel_id":"`+bobCh+`"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("attach own channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"title":"Renamed"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("title-only update = %d", rec.Code)
	}
	if got, _ := listChannelPlaylists(t, srv, "bob", "", ""); len(got.Playlists) != 1 {
		t.Fatalf("title-only update dropped the link: %+v", got.Playlists)
	}
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+bobPl.ID, `{"channel_id":null}`, bob)
	var cleared playlistView
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if rec.Code != http.StatusOK || cleared.ChannelID != nil {
		t.Fatalf("clear = %d %+v, want 200 with no channel_id", rec.Code, cleared)
	}
	if got, _ := listChannelPlaylists(t, srv, "bob", "", ""); len(got.Playlists) != 0 {
		t.Errorf("cleared playlist still listed: %+v", got.Playlists)
	}
}

func ownerIDOf(t *testing.T, srv *Server, token string) string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", token)
	var u struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.ID == "" {
		t.Fatalf("auth/me = %d; body=%s", rec.Code, rec.Body.String())
	}
	return u.ID
}

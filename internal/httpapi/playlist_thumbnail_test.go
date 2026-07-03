package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// uploadPlaylistThumbnail posts a multipart "file" image to the playlist cover
// endpoint.
func uploadPlaylistThumbnail(srv *Server, id, filename, content, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	part, _ := w.CreatePart(hdr)
	_, _ = part.Write([]byte(content))
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/"+id+"/thumbnail", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPlaylistThumbnailLifecycle(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if pl.HasThumbnail {
		t.Fatal("new playlist should have no cover")
	}

	// No cover yet → 404.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("cover before upload = %d, want 404", rec.Code)
	}

	// Upload a JPEG cover.
	rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", rec.Code, rec.Body.String())
	}
	var updated playlistView
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if !updated.HasThumbnail {
		t.Errorf("response has_thumbnail = false, want true")
	}

	// GET serves it with image/jpeg.
	g := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", "")
	if g.Code != http.StatusOK {
		t.Fatalf("get cover = %d, want 200", g.Code)
	}
	if ct := g.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("served content-type = %q, want image/jpeg", ct)
	}

	// Detail reflects has_thumbnail.
	var detail playlistDetailResponse
	_ = json.Unmarshal(getPlaylist(srv, pl.ID, "").Body.Bytes(), &detail)
	if !detail.HasThumbnail {
		t.Errorf("detail has_thumbnail = false, want true")
	}

	// Delete removes it (idempotent), and GET is 404 again.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete cover = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("cover after delete = %d, want 404", rec.Code)
	}
}

func TestPlaylistThumbnailAuthzAndType(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)

	// Non-image → 415.
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.gif", "GIF89a", tok); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("gif cover = %d, want 415", rec.Code)
	}
	// Anonymous → 401.
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "img", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon cover = %d, want 401", rec.Code)
	}
	// Non-owner → 404 (existence not leaked).
	other := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "img", other); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner cover = %d, want 404", rec.Code)
	}
}

func TestPlaylistThumbnailPrivateVisibility(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Secret","visibility":"private"}`)
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "img", tok); rec.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Anonymous cannot see a private playlist's cover.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon private cover = %d, want 404", rec.Code)
	}
	// The owner can.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", tok); rec.Code != http.StatusOK {
		t.Errorf("owner private cover = %d, want 200", rec.Code)
	}
}

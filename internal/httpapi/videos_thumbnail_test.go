package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// uploadThumbnail posts a multipart "file" image to the set-thumbnail endpoint.
func uploadThumbnail(srv *Server, id, filename, contentType, content, token string) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/thumbnail", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestSetVideoThumbnail: an owner uploads a custom poster; it is stored, served
// by GET /thumbnail, and the detail endpoint reports has_thumbnail.
func TestSetVideoThumbnail(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Poster","privacy":"public"}`)

	// No thumbnail yet.
	if g := getThumbnail(srv, id, ""); g.Code != http.StatusNotFound {
		t.Fatalf("thumbnail before upload = %d, want 404", g.Code)
	}

	rec := uploadThumbnail(srv, id, "poster.png", "image/png", "\x89PNG\r\n-fake-bytes", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("set thumbnail = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var fv videoFileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if fv.Kind != "thumbnail" || fv.ContentType != "image/png" {
		t.Fatalf("file view = %+v, want kind=thumbnail content_type=image/png", fv)
	}

	// GET /thumbnail now serves it with the derived content type.
	g := getThumbnail(srv, id, "")
	if g.Code != http.StatusOK {
		t.Fatalf("thumbnail after upload = %d, want 200", g.Code)
	}
	if ct := g.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("served content-type = %q, want image/png", ct)
	}

	// The detail endpoint reports the thumbnail.
	var detail videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &detail)
	if detail.HasThumbnail == nil || !*detail.HasThumbnail {
		t.Errorf("detail has_thumbnail = %v, want true", detail.HasThumbnail)
	}
}

func TestSetVideoThumbnailRejectsNonImage(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	rec := uploadThumbnail(srv, id, "notimage.txt", "text/plain", "nope", tok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image thumbnail = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetVideoThumbnailAuthz(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"x"}`)

	// Anonymous → 401.
	if rec := uploadThumbnail(srv, id, "poster.png", "image/png", "img", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon set thumbnail = %d, want 401", rec.Code)
	}
	// Non-owner → 404 (existence not leaked).
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := uploadThumbnail(srv, id, "poster.png", "image/png", "img", otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner set thumbnail = %d, want 404", rec.Code)
	}
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/video"
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

// frameThumbnail posts the application/json frame-pick variant (W2.C5) of the
// set-thumbnail endpoint.
func frameThumbnail(srv *Server, id, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/thumbnail", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// frameServer builds a server whose Process records a 10s duration and whose
// thumbnailer returns the given JPEG, so a processed original exists for frame-pick.
func frameServer(t *testing.T, frame []byte) *Server {
	t.Helper()
	return videoServerCfg(t, testConfig(),
		video.WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10, Width: 320, Height: 240}}),
		video.WithThumbnailer(fakeThumbnailer{jpg: frame}),
	)
}

// TestSetVideoThumbnailFromFrame: an owner picks a frame at an exact timestamp;
// the extracted JPEG is stored at the poster path and served by GET /thumbnail.
func TestSetVideoThumbnailFromFrame(t *testing.T) {
	frame := []byte("\xff\xd8\xff\xe0framejpeg")
	srv := frameServer(t, frame)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := frameThumbnail(srv, id, `{"at_seconds":5}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("frame-pick = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var fv videoFileView
	_ = json.Unmarshal(rec.Body.Bytes(), &fv)
	if fv.Kind != "thumbnail" || fv.ContentType != "image/jpeg" {
		t.Fatalf("file view = %+v, want kind=thumbnail image/jpeg", fv)
	}

	// A fractional timestamp is accepted too.
	if rec := frameThumbnail(srv, id, `{"at_seconds":9.5}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("fractional frame-pick = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	g := getThumbnail(srv, id, "")
	if g.Code != http.StatusOK {
		t.Fatalf("GET thumbnail after frame-pick = %d, want 200", g.Code)
	}
	if g.Body.String() != string(frame) {
		t.Errorf("served frame body mismatch (%d bytes, want %d)", g.Body.Len(), len(frame))
	}
	if ct := g.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("served content-type = %q, want image/jpeg", ct)
	}

	var detail videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &detail)
	if detail.HasThumbnail == nil || !*detail.HasThumbnail {
		t.Errorf("detail has_thumbnail = %v, want true", detail.HasThumbnail)
	}
}

// TestSetVideoThumbnailFromFrameOutOfRange: at_seconds at/after the duration or
// below zero is 422.
func TestSetVideoThumbnailFromFrameOutOfRange(t *testing.T) {
	srv := frameServer(t, []byte("\xff\xd8jpg"))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	for _, body := range []string{`{"at_seconds":10}`, `{"at_seconds":11}`, `{"at_seconds":-1}`} {
		if rec := frameThumbnail(srv, id, body, tok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("frame-pick %s = %d, want 422; body=%s", body, rec.Code, rec.Body.String())
		}
	}
}

// TestSetVideoThumbnailFromFrameNoOriginal: a draft with no processed original is
// 409, even when at_seconds is otherwise plausible.
func TestSetVideoThumbnailFromFrameNoOriginal(t *testing.T) {
	srv := frameServer(t, []byte("\xff\xd8jpg"))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`) // no upload

	if rec := frameThumbnail(srv, id, `{"at_seconds":1}`, tok); rec.Code != http.StatusConflict {
		t.Errorf("frame-pick before processing = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSetVideoThumbnailFromFrameUnavailable: with no server-side extractor wired,
// frame-pick is 503 (the multipart upload path is unaffected).
func TestSetVideoThumbnailFromFrameUnavailable(t *testing.T) {
	// A prober records duration, but no thumbnailer is wired.
	srv := videoServerCfg(t, testConfig(),
		video.WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10}}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := frameThumbnail(srv, id, `{"at_seconds":5}`, tok); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("frame-pick without extractor = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSetVideoThumbnailFromFrameBadBody: a missing or malformed at_seconds is 400.
func TestSetVideoThumbnailFromFrameBadBody(t *testing.T) {
	srv := frameServer(t, []byte("\xff\xd8jpg"))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	for _, body := range []string{`{}`, `{"at_seconds":null}`, `not json`} {
		if rec := frameThumbnail(srv, id, body, tok); rec.Code != http.StatusBadRequest {
			t.Errorf("frame-pick %q = %d, want 400; body=%s", body, rec.Code, rec.Body.String())
		}
	}
}

// TestSetVideoThumbnailFromFrameAuthz: anonymous is 401; a non-owner is 404.
func TestSetVideoThumbnailFromFrameAuthz(t *testing.T) {
	srv := frameServer(t, []byte("\xff\xd8jpg"))
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := frameThumbnail(srv, id, `{"at_seconds":5}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon frame-pick = %d, want 401", rec.Code)
	}
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := frameThumbnail(srv, id, `{"at_seconds":5}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner frame-pick = %d, want 404", rec.Code)
	}
}

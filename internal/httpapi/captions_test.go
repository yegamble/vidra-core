package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleVTT = "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello\n"

// uploadCaption POSTs a multipart caption (file + language [+ label]).
func uploadCaption(srv *Server, videoID, language, label, content, token string, withFile bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if language != "" {
		_ = w.WriteField("language", language)
	}
	if label != "" {
		_ = w.WriteField("label", label)
	}
	if withFile {
		part, _ := w.CreateFormFile("file", "cap.vtt")
		_, _ = part.Write([]byte(content))
	}
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/captions", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func listCaptions(srv *Server, videoID string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/captions", nil))
	return rec
}

func downloadCaption(srv *Server, videoID, lang string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/captions/"+lang, nil))
	return rec
}

type captionsBody struct {
	Captions []struct {
		Language string `json:"language"`
		Label    string `json:"label"`
	} `json:"captions"`
}

func TestCaptionsFlow(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	// Upload an English caption.
	rec := uploadCaption(srv, vid, "en", "English", sampleVTT, admin, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created captionView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Language != "en" || created.Label != "English" {
		t.Fatalf("created = %+v, want en/English", created)
	}

	// List shows it.
	var list captionsBody
	_ = json.Unmarshal(listCaptions(srv, vid).Body.Bytes(), &list)
	if len(list.Captions) != 1 || list.Captions[0].Language != "en" {
		t.Fatalf("list = %+v, want one en", list.Captions)
	}

	// Download returns the VTT bytes with the WebVTT content type.
	dl := downloadCaption(srv, vid, "en")
	if dl.Code != http.StatusOK {
		t.Fatalf("download = %d", dl.Code)
	}
	if ct := dl.Header().Get("Content-Type"); !strings.Contains(ct, "text/vtt") {
		t.Errorf("download content-type = %q, want text/vtt", ct)
	}
	if dl.Body.String() != sampleVTT {
		t.Errorf("download body = %q, want the uploaded VTT", dl.Body.String())
	}

	// Re-uploading the same language replaces the file (list stays at 1).
	newVTT := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nUpdated\n"
	if rec := uploadCaption(srv, vid, "en", "English", newVTT, admin, true); rec.Code != http.StatusCreated {
		t.Fatalf("re-upload = %d", rec.Code)
	}
	if got := downloadCaption(srv, vid, "en").Body.String(); got != newVTT {
		t.Errorf("download after replace = %q, want updated", got)
	}

	// A second language, then delete the first.
	if rec := uploadCaption(srv, vid, "es", "Español", sampleVTT, admin, true); rec.Code != http.StatusCreated {
		t.Fatalf("upload es = %d", rec.Code)
	}
	_ = json.Unmarshal(listCaptions(srv, vid).Body.Bytes(), &list)
	if len(list.Captions) != 2 {
		t.Fatalf("list after es = %d, want 2", len(list.Captions))
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+vid+"/captions/en", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete en = %d", rec.Code)
	}
	_ = json.Unmarshal(listCaptions(srv, vid).Body.Bytes(), &list)
	if len(list.Captions) != 1 || list.Captions[0].Language != "es" {
		t.Errorf("list after delete = %+v, want only es", list.Captions)
	}
	// Downloading the deleted caption → 404.
	if dl := downloadCaption(srv, vid, "en"); dl.Code != http.StatusNotFound {
		t.Errorf("download deleted = %d, want 404", dl.Code)
	}
}

func TestCaptionsValidationAndAuth(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// A non-WebVTT file → 422.
	if rec := uploadCaption(srv, vid, "en", "", "this is not a caption", admin, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-vtt = %d, want 422", rec.Code)
	}
	// A bad language tag → 422.
	if rec := uploadCaption(srv, vid, "not a lang!", "", sampleVTT, admin, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad lang = %d, want 422", rec.Code)
	}
	// A missing file field → 400.
	if rec := uploadCaption(srv, vid, "en", "", "", admin, false); rec.Code != http.StatusBadRequest {
		t.Errorf("missing file = %d, want 400", rec.Code)
	}
	// A non-owner cannot upload → 404 (existence not leaked).
	if rec := uploadCaption(srv, vid, "en", "", sampleVTT, bob, true); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner upload = %d, want 404", rec.Code)
	}
	// Anonymous cannot upload or delete.
	if rec := uploadCaption(srv, vid, "en", "", sampleVTT, "", true); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon upload = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+vid+"/captions/en", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon delete = %d, want 401", rec.Code)
	}

	// Captions on a non-public (draft) video are not publicly listed/downloaded.
	draft := createVideo(t, srv, admin, "ada", `{"title":"secret","privacy":"private"}`)
	if rec := listCaptions(srv, draft); rec.Code != http.StatusNotFound {
		t.Errorf("list draft captions = %d, want 404", rec.Code)
	}
}

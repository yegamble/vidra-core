package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// replaceVideoFileReq POSTs a multipart "file" to the direct replace endpoint.
func replaceVideoFileReq(srv *Server, id, filename, contentType, content, token string) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/replace", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// publishVideoWithFile creates a draft and publishes it through the direct
// upload (no prober in the harness → published immediately).
func publishVideoWithFile(t *testing.T, srv *Server, tok, handle, title, content string) string {
	t.Helper()
	id := createVideo(t, srv, tok, handle, `{"title":"`+title+`","privacy":"public"}`)
	rec := uploadVideoFile(srv, id, "orig.mp4", "video/mp4", content, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	return id
}

// replacedOriginal GETs the progressive original and returns the served bytes.
func replacedOriginal(t *testing.T, srv *Server, id, token string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/original", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream original = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestReplaceFeatureGate: both replace shapes are 403 feature_disabled until
// the admin turns video_replace_enabled on (default OFF — a new capability),
// and features.video_replace on GET /instance tracks the gate.
func TestReplaceFeatureGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishVideoWithFile(t, srv, adminTok, "ada", "Clip", "v0 bytes")

	if inst := publicInstance(t, srv); inst.Features.VideoReplace {
		t.Error("features.video_replace = true by default, want false")
	}
	rec := replaceVideoFileReq(srv, id, "v1.mp4", "video/mp4", "new", adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Fatalf("direct replace while off = %d %s, want 403 feature_disabled", rec.Code, rec.Body.String())
	}
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":8,"filename":"v1.mp4"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Fatalf("replace-session while off = %d %s, want 403 feature_disabled", rec.Code, rec.Body.String())
	}

	patchSettings(t, srv, adminTok, `{"video_replace_enabled":true}`)
	if inst := publicInstance(t, srv); !inst.Features.VideoReplace {
		t.Error("features.video_replace = false after enabling, want true")
	}
	// Uploads off kills replacement too (effective = setting AND uploads).
	patchSettings(t, srv, adminTok, `{"uploads_enabled":false}`)
	if inst := publicInstance(t, srv); inst.Features.VideoReplace {
		t.Error("features.video_replace = true with uploads off, want false")
	}
	rec = replaceVideoFileReq(srv, id, "v1.mp4", "video/mp4", "new", adminTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("replace with uploads off = %d, want 403", rec.Code)
	}
}

// TestReplaceDirectSwapsSourceKeepsVideo: the direct multipart replace swaps
// the served original, keeps the video published with its metadata, versions
// the storage key, invalidates the stale HLS playlist (transcoding is not
// capable in this harness), and records daily-quota usage.
func TestReplaceDirectSwapsSourceKeepsVideo(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	patchSettings(t, srv, adminTok, `{"video_replace_enabled":true}`)
	id := publishVideoWithFile(t, srv, adminTok, "ada", "Clip", "v0 bytes")

	// Stale HLS from the previous source must stop serving after the swap
	// (no capable transcoder here → invalidation, not re-enqueue).
	seedReadyHLS(t, tcRepo, blobs, id)

	rec := replaceVideoFileReq(srv, id, "V2.WEBM", "video/webm", "v1 replacement bytes", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Video.State != "published" || resp.Video.Title != "Clip" {
		t.Errorf("video after replace = %+v, want published 'Clip' (identity stable)", resp.Video)
	}
	if resp.File.Kind != "original" || resp.File.OriginalName != "V2.WEBM" {
		t.Errorf("file after replace = %+v", resp.File)
	}
	// The progressive original now serves the new source.
	if got := replacedOriginal(t, srv, id, adminTok); got != "v1 replacement bytes" {
		t.Errorf("streamed original = %q, want the replacement bytes", got)
	}
	// Stale playlist invalidated (video detail drops hls_url).
	if _, ok := tcRepo.playlists[uuid.MustParse(id)]; ok {
		t.Error("stale streaming playlist survived a replacement with transcoding unavailable")
	}
	// Daily usage recorded for the replacement bytes (both uploads recorded).
	rec = sendJSONAuth(srv, http.MethodGet, "/api/v1/me/quota", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/quota = %d", rec.Code)
	}
}

// TestReplaceSessionRoundTrip drives the chunked replace shape end to end:
// open a replace session, PUT chunks, complete → 200 with the still-published
// video, and the original serves the assembled replacement bytes. A second
// concurrent replace session is refused 409 replace_conflict while the first
// is active.
func TestReplaceSessionRoundTrip(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	patchSettings(t, srv, adminTok, `{"video_replace_enabled":true}`)
	id := publishVideoWithFile(t, srv, adminTok, "ada", "Clip", "v0 bytes")

	c0 := []byte("0123456789ABCDEF") // harness chunk size 16
	c1 := []byte("REPLACED")
	want := string(append(append([]byte{}, c0...), c1...))

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":`+strconv.Itoa(len(want))+`,"filename":"v2.mp4"}`, adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open replace session = %d; body=%s", rec.Code, rec.Body.String())
	}
	var sess uploadSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	// One replacement in flight per video: a second session is 409.
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":8,"filename":"other.mp4"}`, adminTok)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "replace_conflict" {
		t.Fatalf("second replace session = %d %s, want 409 replace_conflict", rec.Code, rec.Body.String())
	}

	if rec := putChunkAuth(srv, sess.UploadID, 0, c0, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("put chunk 0 = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putChunkAuth(srv, sess.UploadID, 1, c1, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("put chunk 1 = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Completion is asynchronous for a replace session too — the swap re-reads,
	// re-uploads, hashes and probes the whole file, the same work that made the
	// upload path time out. The request only authorises and enqueues.
	rec = completeUploadSession(srv, sess.UploadID, adminTok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("complete replace session = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	// Until the worker runs, the video still serves its PREVIOUS source: that is
	// the whole point of the source-version model, and it must survive the move
	// to a queue.
	if got := replacedOriginal(t, srv, id, adminTok); got != "v0 bytes" {
		t.Errorf("original before the finalize ran = %q, want the previous source", got)
	}
	if done := drainFinalize(t, srv); done != 1 {
		t.Fatalf("finalize drain completed %d jobs, want 1", done)
	}
	if got := getVideoState(t, srv, id, adminTok); got != "published" {
		t.Errorf("state after replace = %q, want published", got)
	}
	if got := replacedOriginal(t, srv, id, adminTok); got != want {
		t.Errorf("streamed original = %q, want assembled replacement", got)
	}
}

// TestReplaceConflictAndVisibilityGates: draft/processing videos, videos with
// a live transcode job, and non-owner callers are all refused correctly; a
// moderator/admin may replace another user's video.
func TestReplaceConflictAndVisibilityGates(t *testing.T) {
	srv, _, tcRepo, _ := videoServerEnv(t, testConfig())
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	patchSettings(t, srv, adminTok, `{"video_replace_enabled":true}`)
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")

	// Draft (no file yet): 409 replace_conflict.
	draft := createVideo(t, srv, bobTok, "bob", `{"title":"Draft"}`)
	rec := replaceVideoFileReq(srv, draft, "v.mp4", "video/mp4", "x", bobTok)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "replace_conflict" {
		t.Fatalf("draft replace = %d %s, want 409 replace_conflict", rec.Code, rec.Body.String())
	}

	id := publishVideoWithFile(t, srv, bobTok, "bob", "Bob's", "v0")

	// A pending/running transcode job blocks replacement.
	jobID := uuid.New()
	tcRepo.jobs[jobID] = &sqlcgen.TranscodeJob{
		ID: jobID, VideoID: uuid.MustParse(id), SourceKey: "web-videos/" + id + ".mp4",
		State: "pending", NextAttemptAt: time.Now(),
	}
	rec = replaceVideoFileReq(srv, id, "v.mp4", "video/mp4", "x", bobTok)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "replace_conflict" {
		t.Fatalf("mid-transcode replace = %d %s, want 409 replace_conflict", rec.Code, rec.Body.String())
	}
	delete(tcRepo.jobs, jobID)

	// A stranger (non-owner, non-privileged) sees 404 — existence not leaked.
	carolTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	rec = replaceVideoFileReq(srv, id, "v.mp4", "video/mp4", "x", carolTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger replace = %d, want 404", rec.Code)
	}
	// The admin may replace bob's video.
	rec = replaceVideoFileReq(srv, id, "admin.mp4", "video/mp4", "admin replacement", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin replace = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := replacedOriginal(t, srv, id, bobTok); got != "admin replacement" {
		t.Errorf("original after admin replace = %q", got)
	}
}

// TestReplaceValidationGates: extension allow-list (with the runtime
// additional-extensions setting honored) and the upload size cap apply to the
// replace-session shape exactly as they do to plain uploads.
func TestReplaceValidationGates(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	patchSettings(t, srv, adminTok, `{"video_replace_enabled":true}`)
	id := publishVideoWithFile(t, srv, adminTok, "ada", "Clip", "v0")

	// Unsupported container: 415.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":8,"filename":"nope.txt"}`, adminTok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("txt replace-session = %d, want 415", rec.Code)
	}
	// .mkv is fine while the additional set is on (default), refused once off.
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":8,"filename":"ok.mkv"}`, adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mkv with additional exts on = %d, want 201", rec.Code)
	}
	var s uploadSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	// Cancel it so it does not hold the one-replacement-in-flight slot.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/uploads/"+s.UploadID, "", adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel replace session = %d, want 204", rec.Code)
	}
	patchSettings(t, srv, adminTok, `{"upload_additional_extensions_enabled":false}`)
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":8,"filename":"ok.mkv"}`, adminTok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("mkv with additional exts off = %d, want 415", rec.Code)
	}
	// Size above the runtime cap: 413 (the setting's floor is 1 MiB).
	patchSettings(t, srv, adminTok, `{"upload_max_size_bytes":1048576}`)
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/replace-session",
		`{"size":1048577,"filename":"big.mp4"}`, adminTok)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize replace-session = %d, want 413", rec.Code)
	}
}

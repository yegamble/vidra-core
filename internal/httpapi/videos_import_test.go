package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// stubExtractor is a fake yt-dlp seam for the HTTP-level import tests: it writes
// fixed bytes into the caller's private workdir (no real binary, no network), so
// the ytdlp import path can be driven end to end through AttachOriginal → Process
// (the ClamAV scan hook). It satisfies videoimport.Extractor.
type stubExtractor struct {
	body []byte
	meta ytdlp.Meta
}

func (s stubExtractor) Metadata(context.Context, string) (ytdlp.Meta, error) { return s.meta, nil }
func (s stubExtractor) Download(_ context.Context, _, workdir string) (string, error) {
	p := filepath.Join(workdir, "media.mp4")
	if err := os.WriteFile(p, s.body, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// importServiceWithYtdlp builds a videoimport.Service over the given (real,
// scanner-wired) video service, driven by the stub extractor — the seam that
// lets a ytdlp import be exercised without the yt-dlp binary while still landing
// bytes through the real AttachOriginal → Process scan hook.
func importServiceWithYtdlp(t *testing.T, videosvc *video.Service, body []byte) *videoimport.Service {
	t.Helper()
	return videoimport.NewService(newImportFakeRepo(), videosvc, 1<<20,
		videoimport.WithYtdlp(stubExtractor{body: body, meta: ytdlp.Meta{Title: "From platform"}}, t.TempDir()),
	)
}

// TestImportViaYtdlpInfectedNeverPublishes proves the W2.C1 invariant at the
// import boundary: bytes fetched by the yt-dlp resolver land EXCLUSIVELY through
// AttachOriginal → Process, so the malware scan hook fires. With the scanner
// reporting the imported body infected (fail-closed), the video finishes in
// 'failed' and is never published/servable — the same guarantee the direct
// upload path has. (The real-clamd EICAR-bytes variant is the integration test.)
func TestImportViaYtdlpInfectedNeverPublishes(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanEnabled = true
	cfg.MalwareScanMode = "fail-closed"
	srv := videoServerCfg(t, cfg,
		video.WithScanner(scanFake{clean: false}), // stands in for an EICAR verdict
		video.WithScanMode("fail-closed"),
	)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	imp := importServiceWithYtdlp(t, srv.videosvc, []byte("pretend-platform-mp4"))
	vid := uuid.MustParse(id)
	if _, err := imp.Enqueue(context.Background(), vid, "https://platform.example/watch?v=abc", videoimport.ResolverYtdlp); err != nil {
		t.Fatalf("enqueue ytdlp import: %v", err)
	}
	if _, err := imp.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got, err := srv.videosvc.GetByID(context.Background(), vid)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	// The scan verdict lives on the VIDEO state (the import job may report done —
	// it successfully ran the pipeline; the scanner is what rejected the bytes).
	// The invariant: an infected import is never published/servable.
	if got.State != "failed" {
		t.Fatalf("imported infected video state = %q, want failed (never published)", got.State)
	}
}

// loopbackAsLocalhost rewrites an httptest server's 127.0.0.1 URL to use the
// "localhost" hostname. urlsafety.ValidateURL rejects a literal loopback IP up
// front, but accepts a hostname (names are only resolved at dial time) — so the
// enqueue up-front check passes; the harness import service then fetches with a
// plain client (the production SSRF guard, tested in the videoimport package,
// refuses loopback at dial time).
func loopbackAsLocalhost(u string) string {
	return strings.Replace(u, "127.0.0.1", "localhost", 1)
}

// importMediaServer is a loopback test origin serving a few fixtures: a tiny
// "video" (the harness has no prober, so any bytes publish), a non-video file,
// and an extension-less video (type only in the Content-Type).
func importMediaServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clip.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("tiny-video-bytes"))
		case "/notavideo.txt":
			_, _ = w.Write([]byte("not a video"))
		case "/download": // no file extension; type is only in the Content-Type
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("extensionless-video-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// enqueueImport POSTs an import and asserts a 202 with a pending job, returning
// the job.
func enqueueImport(t *testing.T, srv *Server, videoID, url, token string) importJobView {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+videoID+"/import", `{"url":"`+url+`"}`, token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("enqueue import = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp importJobResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ImportJob.State != "pending" {
		t.Fatalf("enqueued job state = %q, want pending", resp.ImportJob.State)
	}
	return resp.ImportJob
}

// getImportStatus GETs the video's import job status.
func getImportStatus(t *testing.T, srv *Server, videoID, token string) importJobView {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+videoID+"/import", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get import status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp importJobResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.ImportJob
}

// TestImportVideoAsyncPublishes enqueues a URL import (202), drives the worker
// to completion, and checks the job reports done and the video is published and
// publicly served — the same end state the synchronous path produced.
func TestImportVideoAsyncPublishes(t *testing.T) {
	srv := videoServer(t)
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	enqueueImport(t, srv, id, loopbackAsLocalhost(media.URL)+"/clip.mp4", tok)

	if _, err := srv.importsvc.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if job := getImportStatus(t, srv, id, tok); job.State != "done" {
		t.Fatalf("job state after drain = %q (err=%q), want done", job.State, job.Error)
	}
	// The imported original is now publicly reachable and the video is published.
	if g := getVideo(srv, id, ""); g.Code != http.StatusOK {
		t.Errorf("public detail after import = %d, want 200", g.Code)
	}
}

// TestImportVideoAsyncContentTypeFallback: an extension-less URL is accepted via
// its response Content-Type and imports to published.
func TestImportVideoAsyncContentTypeFallback(t *testing.T) {
	srv := videoServer(t)
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"CDN import","privacy":"public"}`)

	enqueueImport(t, srv, id, loopbackAsLocalhost(media.URL)+"/download", tok)
	if _, err := srv.importsvc.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if job := getImportStatus(t, srv, id, tok); job.State != "done" {
		t.Fatalf("extensionless import state = %q (err=%q), want done", job.State, job.Error)
	}
}

// TestImportVideoAsyncNonVideoFails: a non-video URL is accepted for enqueue but
// the worker fails the job with a safe reason — no raw error, no URL leak.
func TestImportVideoAsyncNonVideoFails(t *testing.T) {
	srv := videoServer(t)
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	enqueueImport(t, srv, id, loopbackAsLocalhost(media.URL)+"/notavideo.txt", tok)
	if _, err := srv.importsvc.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	job := getImportStatus(t, srv, id, tok)
	if job.State == "done" {
		t.Fatalf("non-video job state = done, want a failure state")
	}
	if job.Error == "" || strings.Contains(job.Error, media.URL) {
		t.Errorf("job error = %q, want a safe non-empty reason that does not leak the URL", job.Error)
	}
}

// TestImportVideoIdempotentWhileActive: a second POST while an import is still
// in flight returns the SAME job (single active import per video).
func TestImportVideoIdempotentWhileActive(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	first := enqueueImport(t, srv, id, "https://example.com/clip.mp4", tok)
	second := enqueueImport(t, srv, id, "https://example.com/other.mp4", tok)
	if first.ID != second.ID {
		t.Errorf("second enqueue id = %s, want the in-flight job %s", second.ID, first.ID)
	}
}

// TestImportVideoValidation: the up-front URL validation still applies before
// any job is created.
func TestImportVideoValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	// Missing URL → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty url = %d, want 422", rec.Code)
	}
	// Non-http scheme → 422 (urlsafety rejects it before any enqueue).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{"url":"ftp://example.com/x.mp4"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ftp url = %d, want 422", rec.Code)
	}
	// Anonymous → 401.
	if rec := postTo(srv, "/api/v1/videos/"+id+"/import", `{"url":"https://example.com/x.mp4"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon import = %d, want 401", rec.Code)
	}
}

// TestImportVideoNonOwner: a non-owner is 404 before any job is enqueued —
// ownership is checked first, so the server never imports on their behalf.
func TestImportVideoNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"x"}`)

	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://example.com/clip.mp4"}`, otherTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner import = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// GET status is likewise owner-only.
	if g := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/import", "", otherTok); g.Code != http.StatusNotFound {
		t.Errorf("non-owner status = %d, want 404", g.Code)
	}
}

// TestImportVideoStatusBeforeAnyImport: GET status on a video that was never
// imported is 404.
func TestImportVideoStatusBeforeAnyImport(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/import", "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("status before import = %d, want 404", rec.Code)
	}
}

// TestImportResolverRequestValidation: the resolver field is validated up front
// (unknown → 422) and an explicit resolver=ytdlp is refused with 503 while the
// yt-dlp platform importer is disabled (the default in the test harness).
func TestImportResolverRequestValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	// Unknown resolver → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://example.com/clip.mp4","resolver":"magnet"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bogus resolver = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// resolver=ytdlp while disabled → 503 with the stable service_unavailable code.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://platform.example/watch?v=abc","resolver":"ytdlp"}`, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ytdlp-while-disabled = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "service_unavailable" {
		t.Errorf("503 code = %q, want service_unavailable", body.Error.Code)
	}
}

// TestImportResolverEchoedInView: an explicit resolver=direct import is accepted
// and the enqueued + drained job echoes resolver=direct and (after completion) a
// cleared stage, so the UI can render honest progress.
func TestImportResolverEchoedInView(t *testing.T) {
	srv := videoServer(t)
	media := importMediaServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/clip.mp4","resolver":"direct"}`, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("direct import = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp importJobResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ImportJob.Resolver != "direct" {
		t.Errorf("enqueued resolver = %q, want direct", resp.ImportJob.Resolver)
	}

	if _, err := srv.importsvc.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	job := getImportStatus(t, srv, id, tok)
	if job.State != "done" {
		t.Fatalf("state = %q (err=%q), want done", job.State, job.Error)
	}
	if job.Resolver != "direct" {
		t.Errorf("resolver after drain = %q, want direct", job.Resolver)
	}
	if job.Stage != "" {
		t.Errorf("stage after done = %q, want cleared", job.Stage)
	}
}

// RenewImportJobLease is the lease heartbeat; the fake has no leases to keep.
func (*importFakeRepo) RenewImportJobLease(_ context.Context, _ uuid.UUID) error { return nil }

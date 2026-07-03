package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

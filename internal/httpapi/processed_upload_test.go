package httpapi

import (
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/processedupload"
)

func TestProcessedUploadSessionAtTwentyGiBBoundary(t *testing.T) {
	cfg := quotaCfg(0)
	cfg.UploadMaxSize = "21474836480"
	cfg.UploadNormalizerSocket = "/run/processor.sock"
	srv := videoServerCfg(t, cfg)
	token := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, token, "ada", `{"title":"large"}`)
	session := openUploadSession(t, srv, id, "large.mp4", int(processedupload.MaxInputBytes), token)
	if session.TotalChunks != int(processedupload.MaxInputBytes)/session.ChunkSize {
		t.Fatalf("unexpected chunk geometry: %+v", session)
	}
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"filename":"large.mp4","size":21474836481}`, token)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte over: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	if got := srv.uploadMaxSizeBytes(); got != processedupload.MaxScannedBytes {
		t.Fatalf("raw upload allowed %d bytes", got)
	}
	if got := cfg.UploadLimit(1024, true); got != 1024 {
		t.Fatalf("overrode operator cap: %d", got)
	}
	cfg.UploadNormalizerSocket, cfg.ClamAVAddr, cfg.MalwareScanMode = "", "clamav:3310", "fail-closed"
	cfg.MalwareScanEnabled = true
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"filename":"large.mp4","size":21474836480}`, token)
	if rec.Code != http.StatusRequestEntityTooLarge || srv.uploadMaxSizeBytes() != processedupload.MaxScannedBytes {
		t.Fatalf("broker-off instance accepted oversized raw media: HTTP %d, direct cap %d", rec.Code, srv.uploadMaxSizeBytes())
	}
}

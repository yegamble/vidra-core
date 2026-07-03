package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// remoteVideoFakeRepo serves remote-video rows by id.
type remoteVideoFakeRepo map[uuid.UUID]sqlcgen.GetRemoteVideoByIDRow

func (f remoteVideoFakeRepo) GetRemoteVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return sqlcgen.GetRemoteVideoByIDRow{}, pgx.ErrNoRows
}

// remoteVideoServer builds a server with only the remote-video read side
// mounted, plus a seeded video (with cached thumbnail) in a local blob backend.
func remoteVideoServer(t *testing.T) (*Server, uuid.UUID) {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	id := uuid.New()
	key := "remote-thumbnails/" + id.String() + ".jpg"
	if _, err := blobs.Put(context.Background(), key, strings.NewReader("jpegbytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	stream := "https://remote.example/hls/x.m3u8"
	repo := remoteVideoFakeRepo{id: {
		ID:             id,
		ObjectUrl:      "https://remote.example/videos/watch/x",
		RemoteActorUrl: "https://remote.example/video-channels/movies",
		Domain:         "remote.example",
		Title:          "Remote clip",
		Description:    "from afar",
		WatchUrl:       "https://remote.example/w/x",
		StreamUrl:      &stream,
		ThumbnailKey:   &key,
	}}
	srv := New(testConfig(), nil, nil, WithRemoteVideoService(remotevideo.NewService(repo, blobs)))
	return srv, id
}

func TestGetRemoteVideo(t *testing.T) {
	srv, id := remoteVideoServer(t)
	rec := getWithAuth(srv, "/api/v1/remote-videos/"+id.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var view remoteVideoView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !view.Remote {
		t.Error("remote = false, want true")
	}
	if view.Domain != "remote.example" || view.Title != "Remote clip" {
		t.Errorf("view = %+v", view)
	}
	if view.WatchURL != "https://remote.example/w/x" {
		t.Errorf("watch_url = %q", view.WatchURL)
	}
	if view.StreamURL == nil || *view.StreamURL != "https://remote.example/hls/x.m3u8" {
		t.Errorf("stream_url = %v", view.StreamURL)
	}
	if !view.HasThumbnail {
		t.Error("has_thumbnail = false, want true")
	}
}

func TestGetRemoteVideoNotFound(t *testing.T) {
	srv, _ := remoteVideoServer(t)
	if rec := getWithAuth(srv, "/api/v1/remote-videos/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/remote-videos/not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id = %d, want 404", rec.Code)
	}
}

func TestGetRemoteVideoThumbnail(t *testing.T) {
	srv, id := remoteVideoServer(t)
	rec := getWithAuth(srv, "/api/v1/remote-videos/"+id.String()+"/thumbnail", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/jpeg") {
		t.Errorf("content-type = %q, want image/jpeg", ct)
	}
	if rec.Body.String() != "jpegbytes" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/remote-videos/"+uuid.NewString()+"/thumbnail", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id thumbnail = %d, want 404", rec.Code)
	}
}

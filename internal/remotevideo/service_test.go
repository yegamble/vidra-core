package remotevideo

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo serves remote-video rows by id.
type fakeRepo map[uuid.UUID]sqlcgen.GetRemoteVideoByIDRow

func (f fakeRepo) GetRemoteVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return sqlcgen.GetRemoteVideoByIDRow{}, pgx.ErrNoRows
}

func TestGetMapsRow(t *testing.T) {
	id := uuid.New()
	dur := int32(120)
	stream := "https://remote.example/hls/x.m3u8"
	key := "remote-thumbnails/" + id.String() + ".jpg"
	published := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	repo := fakeRepo{id: {
		ID:              id,
		ObjectUrl:       "https://remote.example/videos/watch/x",
		RemoteActorUrl:  "https://remote.example/video-channels/movies",
		Domain:          "remote.example",
		Title:           "Clip",
		Description:     "desc",
		DurationSeconds: &dur,
		PublishedAt:     pgtype.Timestamptz{Time: published, Valid: true},
		WatchUrl:        "https://remote.example/w/x",
		StreamUrl:       &stream,
		ThumbnailKey:    &key,
	}}
	svc := NewService(repo, nil)

	rv, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rv.Domain != "remote.example" || rv.Title != "Clip" || rv.WatchURL != "https://remote.example/w/x" {
		t.Errorf("mapped fields wrong: %+v", rv)
	}
	if rv.StreamURL == nil || *rv.StreamURL != stream {
		t.Errorf("stream = %v", rv.StreamURL)
	}
	if rv.DurationSeconds == nil || *rv.DurationSeconds != 120 {
		t.Errorf("duration = %v", rv.DurationSeconds)
	}
	if rv.PublishedAt == nil || !rv.PublishedAt.Equal(published) {
		t.Errorf("published = %v", rv.PublishedAt)
	}
	if !rv.HasThumbnail {
		t.Error("HasThumbnail = false, want true")
	}
}

func TestGetNotFound(t *testing.T) {
	svc := NewService(fakeRepo{}, nil)
	if _, err := svc.Get(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestOpenThumbnail(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	id := uuid.New()
	key := "remote-thumbnails/" + id.String() + ".jpg"
	if _, err := blobs.Put(context.Background(), key, strings.NewReader("jpegbytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	repo := fakeRepo{id: {ID: id, ThumbnailKey: &key}}
	svc := NewService(repo, blobs)

	rc, err := svc.OpenThumbnail(context.Background(), id)
	if err != nil {
		t.Fatalf("OpenThumbnail: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "jpegbytes" {
		t.Errorf("bytes = %q", got)
	}
}

func TestOpenThumbnailAbsent(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	id := uuid.New()
	repo := fakeRepo{id: {ID: id}} // no thumbnail key
	svc := NewService(repo, blobs)
	if _, err := svc.OpenThumbnail(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("no key: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.OpenThumbnail(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

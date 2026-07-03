package playlist

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

func TestSetAndClearThumbnail(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(newFakeRepo(), WithStorage(blobs))
	owner := uuid.New()
	p, err := svc.Create(ctx, owner, CreateInput{Title: "t", Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}

	ext, err := svc.SetThumbnail(ctx, owner, p.ID, UploadInput{Filename: "cover.jpeg", Reader: strings.NewReader("img")})
	if err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}
	if ext != "jpg" {
		t.Errorf("ext = %q, want jpg (jpeg canonicalized)", ext)
	}
	key := media.PlaylistThumbnailKey(p.ID, "jpg")
	if ok, _ := blobs.Exists(ctx, key); !ok {
		t.Errorf("cover blob %q not stored", key)
	}
	row, _ := svc.GetByID(ctx, p.ID)
	if row.ThumbnailExt == nil || *row.ThumbnailExt != "jpg" {
		t.Errorf("thumbnail_ext = %v, want jpg", row.ThumbnailExt)
	}

	// Replacing with a different extension removes the stale blob.
	if _, err := svc.SetThumbnail(ctx, owner, p.ID, UploadInput{Filename: "cover.png", Reader: strings.NewReader("img2")}); err != nil {
		t.Fatalf("SetThumbnail png: %v", err)
	}
	if ok, _ := blobs.Exists(ctx, key); ok {
		t.Errorf("stale jpg blob %q not removed on replace", key)
	}
	pngKey := media.PlaylistThumbnailKey(p.ID, "png")
	if ok, _ := blobs.Exists(ctx, pngKey); !ok {
		t.Errorf("png cover blob %q not stored", pngKey)
	}

	// Clear removes blob + column, idempotently.
	if err := svc.ClearThumbnail(ctx, owner, p.ID); err != nil {
		t.Fatalf("ClearThumbnail: %v", err)
	}
	if ok, _ := blobs.Exists(ctx, pngKey); ok {
		t.Errorf("png cover blob %q not removed on clear", pngKey)
	}
	row, _ = svc.GetByID(ctx, p.ID)
	if row.ThumbnailExt != nil {
		t.Errorf("thumbnail_ext = %v after clear, want nil", row.ThumbnailExt)
	}
	if err := svc.ClearThumbnail(ctx, owner, p.ID); err != nil {
		t.Errorf("ClearThumbnail idempotent: %v", err)
	}
}

func TestSetThumbnailRejectsNonImageAndNonOwner(t *testing.T) {
	ctx := context.Background()
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(newFakeRepo(), WithStorage(blobs))
	owner := uuid.New()
	p, _ := svc.Create(ctx, owner, CreateInput{Title: "t", Visibility: "public"})

	if _, err := svc.SetThumbnail(ctx, owner, p.ID, UploadInput{Filename: "cover.gif", Reader: strings.NewReader("x")}); err != ErrUnsupportedMedia {
		t.Errorf("gif cover: err = %v, want ErrUnsupportedMedia", err)
	}
	if _, err := svc.SetThumbnail(ctx, uuid.New(), p.ID, UploadInput{Filename: "cover.jpg", Reader: strings.NewReader("x")}); err != ErrForbidden {
		t.Errorf("non-owner: err = %v, want ErrForbidden", err)
	}
	if _, err := svc.SetThumbnail(ctx, owner, uuid.New(), UploadInput{Filename: "cover.jpg", Reader: strings.NewReader("x")}); err != ErrNotFound {
		t.Errorf("unknown playlist: err = %v, want ErrNotFound", err)
	}
}

func TestThumbnailContentType(t *testing.T) {
	if ct, ok := ThumbnailContentType("jpg"); !ok || ct != "image/jpeg" {
		t.Errorf("jpg → (%q,%v), want image/jpeg,true", ct, ok)
	}
	if ct, ok := ThumbnailContentType("png"); !ok || ct != "image/png" {
		t.Errorf("png → (%q,%v), want image/png,true", ct, ok)
	}
	if _, ok := ThumbnailContentType("gif"); ok {
		t.Error("gif should not be an accepted cover type")
	}
}

package profileimage

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

type countingBump struct{ calls int }

func (c *countingBump) bump(context.Context) error { c.calls++; return nil }

func bumpTestService(t *testing.T, bump func(context.Context) error) *Service {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	svc := NewService(newFakeRepo(), blobs,
		WithInstanceImages(newInstanceFakeRepo()), WithVersionBump(bump))
	if err := svc.LoadInstanceImages(context.Background()); err != nil {
		t.Fatalf("load instance images: %v", err)
	}
	return svc
}

// TestInstanceImageWritesBumpSettingsVersion: branding metadata is a per-replica
// in-memory cache read by GET /instance on every page load. Without a bump, a
// new logo appears on 1 of N replicas and the site's header flickers between
// old and new depending on which one the request landed on.
func TestInstanceImageWritesBumpSettingsVersion(t *testing.T) {
	ctx := context.Background()
	bumper := &countingBump{}
	svc := bumpTestService(t, bumper.bump)

	if _, err := svc.SetInstanceImage(ctx, KindLogoFavicon,
		UploadInput{Filename: "icon.png", Reader: strings.NewReader("png-bytes")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if bumper.calls != 1 {
		t.Fatalf("bump calls after SetInstanceImage = %d, want 1", bumper.calls)
	}

	if err := svc.DeleteInstanceImage(ctx, KindLogoFavicon); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if bumper.calls != 2 {
		t.Fatalf("bump calls after DeleteInstanceImage = %d, want 2", bumper.calls)
	}
}

// TestInstanceImageNoBumpWhenNothingWritten: a rejected upload and a delete of
// an absent slot changed no row, so they must not invalidate the fleet.
func TestInstanceImageNoBumpWhenNothingWritten(t *testing.T) {
	ctx := context.Background()
	bumper := &countingBump{}
	svc := bumpTestService(t, bumper.bump)

	if _, err := svc.SetInstanceImage(ctx, KindLogoFavicon,
		UploadInput{Filename: "icon.gif", Reader: strings.NewReader("gif")}); err != ErrUnsupportedMedia {
		t.Fatalf("gif upload error = %v, want ErrUnsupportedMedia", err)
	}
	if err := svc.DeleteInstanceImage(ctx, KindLogoOpengraph); err != ErrNotFound {
		t.Fatalf("delete of unset slot error = %v, want ErrNotFound", err)
	}
	if bumper.calls != 0 {
		t.Fatalf("bump calls for no-op writes = %d, want 0", bumper.calls)
	}
}

// TestUserImageWritesDoNotBump: per-user avatars/banners are NOT in the
// instance-wide cache the counter guards — they are read from the database per
// request. Bumping for them would make every user's avatar change reload every
// replica's settings, docs and branding caches.
func TestUserImageWritesDoNotBump(t *testing.T) {
	bumper := &countingBump{}
	svc := bumpTestService(t, bumper.bump)
	if _, err := svc.SetUserImage(context.Background(), uuid.New(), KindAvatar,
		UploadInput{Filename: "me.png", Reader: strings.NewReader("png-bytes")}); err != nil {
		t.Fatalf("set user image: %v", err)
	}
	if bumper.calls != 0 {
		t.Fatalf("bump calls for a user avatar = %d, want 0", bumper.calls)
	}
}

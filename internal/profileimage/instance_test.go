package profileimage

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// instanceFakeRepo is an in-memory InstanceRepository keyed by kind.
type instanceFakeRepo struct {
	rows map[string]sqlcgen.InstanceImage
}

func newInstanceFakeRepo() *instanceFakeRepo {
	return &instanceFakeRepo{rows: map[string]sqlcgen.InstanceImage{}}
}

func (f *instanceFakeRepo) ListInstanceImages(context.Context) ([]sqlcgen.InstanceImage, error) {
	out := make([]sqlcgen.InstanceImage, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

func (f *instanceFakeRepo) UpsertInstanceImage(_ context.Context, a sqlcgen.UpsertInstanceImageParams) (sqlcgen.InstanceImage, error) {
	row := sqlcgen.InstanceImage{Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes}
	f.rows[a.Kind] = row
	return row, nil
}

func (f *instanceFakeRepo) GetInstanceImage(_ context.Context, kind string) (sqlcgen.InstanceImage, error) {
	row, ok := f.rows[kind]
	if !ok {
		return sqlcgen.InstanceImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *instanceFakeRepo) DeleteInstanceImage(_ context.Context, kind string) (int64, error) {
	if _, ok := f.rows[kind]; !ok {
		return 0, nil
	}
	delete(f.rows, kind)
	return 1, nil
}

func instanceTestService(t *testing.T) (*Service, storage.Backend) {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	svc := NewService(newFakeRepo(), blobs, WithInstanceImages(newInstanceFakeRepo()))
	if err := svc.LoadInstanceImages(context.Background()); err != nil {
		t.Fatalf("load instance images: %v", err)
	}
	return svc, blobs
}

func TestInstanceImageRoundTripAllKinds(t *testing.T) {
	svc, blobs := instanceTestService(t)
	ctx := context.Background()

	kinds := append([]string{KindAvatar, KindBanner}, InstanceLogoKinds...)
	for _, kind := range kinds {
		if _, ok := svc.InstanceImage(kind); ok {
			t.Fatalf("%s: fresh store reports an image", kind)
		}
		img, err := svc.SetInstanceImage(ctx, kind, UploadInput{Filename: "logo.png", Reader: strings.NewReader("png-bytes")})
		if err != nil {
			t.Fatalf("%s: set: %v", kind, err)
		}
		if img.ContentType != "image/png" || img.SizeBytes != int64(len("png-bytes")) {
			t.Errorf("%s: stored image = %+v", kind, img)
		}
		got, ok := svc.InstanceImage(kind)
		if !ok || got.StorageKey != img.StorageKey {
			t.Errorf("%s: cache lookup = %+v ok=%v", kind, got, ok)
		}
		if _, err := blobs.Open(ctx, img.StorageKey); err != nil {
			t.Errorf("%s: stored blob missing at %s: %v", kind, img.StorageKey, err)
		}
	}
}

func TestInstanceImageKeyLayout(t *testing.T) {
	cases := map[string]string{
		KindAvatar:         "avatars/instance/instance.png",
		KindBanner:         "banners/instance/instance.png",
		KindLogoFavicon:    "logos/instance/favicon.png",
		KindLogoHeaderWide: "logos/instance/header-wide.png",
		KindLogoOpengraph:  "logos/instance/opengraph.png",
	}
	for kind, want := range cases {
		if got := instanceImageKey(kind, ".png"); got != want {
			t.Errorf("instanceImageKey(%s) = %q, want %q", kind, got, want)
		}
	}
}

func TestInstanceImageReplaceEvictsOldBlob(t *testing.T) {
	svc, blobs := instanceTestService(t)
	ctx := context.Background()

	first, err := svc.SetInstanceImage(ctx, KindAvatar, UploadInput{Filename: "a.png", Reader: strings.NewReader("one")})
	if err != nil {
		t.Fatalf("first set: %v", err)
	}
	second, err := svc.SetInstanceImage(ctx, KindAvatar, UploadInput{Filename: "b.webp", Reader: strings.NewReader("two")})
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if first.StorageKey == second.StorageKey {
		t.Fatal("expected extension change to produce a new storage key")
	}
	if _, err := blobs.Open(ctx, first.StorageKey); err == nil {
		t.Error("old blob still present after replacement with a different extension")
	}
	if got, _ := svc.InstanceImage(KindAvatar); got.ContentType != "image/webp" {
		t.Errorf("replaced image content type = %q, want image/webp", got.ContentType)
	}
}

func TestInstanceImageDeleteAndGuards(t *testing.T) {
	svc, blobs := instanceTestService(t)
	ctx := context.Background()

	// Unsupported extension -> ErrUnsupportedMedia.
	if _, err := svc.SetInstanceImage(ctx, KindAvatar, UploadInput{Filename: "x.gif", Reader: strings.NewReader("gif")}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("gif upload err = %v, want ErrUnsupportedMedia", err)
	}
	// Unknown kind -> ErrNotFound.
	if _, err := svc.SetInstanceImage(ctx, "poster", UploadInput{Filename: "x.png", Reader: strings.NewReader("p")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown kind err = %v, want ErrNotFound", err)
	}
	// Delete without an image -> ErrNotFound.
	if err := svc.DeleteInstanceImage(ctx, KindBanner); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unset err = %v, want ErrNotFound", err)
	}

	img, err := svc.SetInstanceImage(ctx, KindBanner, UploadInput{Filename: "b.jpg", Reader: strings.NewReader("jpg")})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := svc.DeleteInstanceImage(ctx, KindBanner); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := svc.InstanceImage(KindBanner); ok {
		t.Error("image still cached after delete")
	}
	if _, err := blobs.Open(ctx, img.StorageKey); err == nil {
		t.Error("blob still present after delete")
	}

	// A service without an instance repo is inert.
	bare := NewService(newFakeRepo(), blobs)
	if _, err := bare.SetInstanceImage(ctx, KindAvatar, UploadInput{Filename: "a.png", Reader: strings.NewReader("x")}); !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("set without instance repo err = %v, want ErrStorageUnavailable", err)
	}
	if err := bare.DeleteInstanceImage(ctx, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete without instance repo err = %v, want ErrNotFound", err)
	}
}

func TestLoadInstanceImagesPopulatesCache(t *testing.T) {
	repo := newInstanceFakeRepo()
	repo.rows[KindAvatar] = sqlcgen.InstanceImage{Kind: KindAvatar, StorageKey: "avatars/instance/instance.png", ContentType: "image/png", SizeBytes: 3}
	repo.rows["legacy-kind"] = sqlcgen.InstanceImage{Kind: "legacy-kind", StorageKey: "x"}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	svc := NewService(newFakeRepo(), blobs, WithInstanceImages(repo))
	if err := svc.LoadInstanceImages(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if img, ok := svc.InstanceImage(KindAvatar); !ok || img.StorageKey != "avatars/instance/instance.png" {
		t.Errorf("avatar after load = %+v ok=%v", img, ok)
	}
	if _, ok := svc.InstanceImage("legacy-kind"); ok {
		t.Error("unknown legacy kind surfaced from the cache")
	}
}

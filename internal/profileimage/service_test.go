package profileimage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository keyed by owner|kind.
type fakeRepo struct {
	users    map[string]sqlcgen.UserImage
	channels map[string]sqlcgen.ChannelImage
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[string]sqlcgen.UserImage{}, channels: map[string]sqlcgen.ChannelImage{}}
}

func key(id uuid.UUID, kind string) string { return id.String() + "|" + kind }

func (f *fakeRepo) UpsertUserImage(_ context.Context, a sqlcgen.UpsertUserImageParams) (sqlcgen.UserImage, error) {
	row := sqlcgen.UserImage{UserID: a.UserID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes}
	f.users[key(a.UserID, a.Kind)] = row
	return row, nil
}

func (f *fakeRepo) GetUserImage(_ context.Context, a sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error) {
	row, ok := f.users[key(a.UserID, a.Kind)]
	if !ok {
		return sqlcgen.UserImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *fakeRepo) DeleteUserImage(_ context.Context, a sqlcgen.DeleteUserImageParams) (int64, error) {
	k := key(a.UserID, a.Kind)
	if _, ok := f.users[k]; !ok {
		return 0, nil
	}
	delete(f.users, k)
	return 1, nil
}

func (f *fakeRepo) UpsertChannelImage(_ context.Context, a sqlcgen.UpsertChannelImageParams) (sqlcgen.ChannelImage, error) {
	row := sqlcgen.ChannelImage{ChannelID: a.ChannelID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes}
	f.channels[key(a.ChannelID, a.Kind)] = row
	return row, nil
}

func (f *fakeRepo) GetChannelImage(_ context.Context, a sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error) {
	row, ok := f.channels[key(a.ChannelID, a.Kind)]
	if !ok {
		return sqlcgen.ChannelImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *fakeRepo) DeleteChannelImage(_ context.Context, a sqlcgen.DeleteChannelImageParams) (int64, error) {
	k := key(a.ChannelID, a.Kind)
	if _, ok := f.channels[k]; !ok {
		return 0, nil
	}
	delete(f.channels, k)
	return 1, nil
}

func testService(t *testing.T) (*Service, storage.Backend) {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	return NewService(newFakeRepo(), blobs), blobs
}

func mustExist(t *testing.T, blobs storage.Backend, key string, want bool) {
	t.Helper()
	ok, err := blobs.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists(%q): %v", key, err)
	}
	if ok != want {
		t.Fatalf("Exists(%q) = %v, want %v", key, ok, want)
	}
}

// TestSetUserImageStoresAtDeterministicKey: the blob lands at
// avatars/users/<id><ext> (banner → banners/) with the extension-derived type.
func TestSetUserImageStoresAtDeterministicKey(t *testing.T) {
	svc, blobs := testService(t)
	ctx := context.Background()
	userID := uuid.New()

	img, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "me.PNG", Reader: strings.NewReader("png-bytes")})
	if err != nil {
		t.Fatalf("SetUserImage: %v", err)
	}
	wantKey := "avatars/users/" + userID.String() + ".png"
	if img.StorageKey != wantKey || img.ContentType != "image/png" || img.SizeBytes != 9 {
		t.Fatalf("image = %+v, want key=%s type=image/png size=9", img, wantKey)
	}
	mustExist(t, blobs, wantKey, true)

	banner, err := svc.SetUserImage(ctx, userID, KindBanner, UploadInput{Filename: "wide.jpg", Reader: strings.NewReader("jpg")})
	if err != nil {
		t.Fatalf("SetUserImage banner: %v", err)
	}
	if want := "banners/users/" + userID.String() + ".jpg"; banner.StorageKey != want || banner.ContentType != "image/jpeg" {
		t.Fatalf("banner = %+v, want key=%s type=image/jpeg", banner, want)
	}
	if !svc.HasUserImage(ctx, userID, KindAvatar) || !svc.HasUserImage(ctx, userID, KindBanner) {
		t.Error("HasUserImage should report both kinds set")
	}
}

// TestSetUserImageReplaceEvictsOldBlob: re-uploading with a different extension
// moves the deterministic key and must not orphan the previous object.
func TestSetUserImageReplaceEvictsOldBlob(t *testing.T) {
	svc, blobs := testService(t)
	ctx := context.Background()
	userID := uuid.New()

	if _, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "a.png", Reader: strings.NewReader("old")}); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	img, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "b.webp", Reader: strings.NewReader("new-bytes")})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if img.ContentType != "image/webp" {
		t.Errorf("content type after replace = %q, want image/webp", img.ContentType)
	}
	mustExist(t, blobs, "avatars/users/"+userID.String()+".png", false)
	mustExist(t, blobs, "avatars/users/"+userID.String()+".webp", true)

	got, err := svc.UserImage(ctx, userID, KindAvatar)
	if err != nil || got.StorageKey != img.StorageKey {
		t.Fatalf("UserImage after replace = (%+v, %v), want the webp row", got, err)
	}
}

// TestDeleteUserImage removes the row and the stored object; a second delete
// (or a delete with nothing set) is ErrNotFound.
func TestDeleteUserImage(t *testing.T) {
	svc, blobs := testService(t)
	ctx := context.Background()
	userID := uuid.New()

	if err := svc.DeleteUserImage(ctx, userID, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete with nothing set = %v, want ErrNotFound", err)
	}
	if _, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "a.jpg", Reader: strings.NewReader("x")}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := svc.DeleteUserImage(ctx, userID, KindAvatar); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustExist(t, blobs, "avatars/users/"+userID.String()+".jpg", false)
	if svc.HasUserImage(ctx, userID, KindAvatar) {
		t.Error("HasUserImage after delete = true, want false")
	}
	if _, err := svc.UserImage(ctx, userID, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserImage after delete = %v, want ErrNotFound", err)
	}
}

// TestSetImageRejectsNonImage: the extension gate mirrors the thumbnail upload.
func TestSetImageRejectsNonImage(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	for _, name := range []string{"notes.txt", "movie.mp4", "noext", "evil.svg", "a.png.exe"} {
		if _, err := svc.SetUserImage(ctx, uuid.New(), KindAvatar, UploadInput{Filename: name, Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
			t.Errorf("SetUserImage(%q) = %v, want ErrUnsupportedMedia", name, err)
		}
		if _, err := svc.SetChannelImage(ctx, uuid.New(), KindBanner, UploadInput{Filename: name, Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
			t.Errorf("SetChannelImage(%q) = %v, want ErrUnsupportedMedia", name, err)
		}
	}
}

// TestChannelImageLifecycle: set → get → replace-same-ext → delete for channels.
func TestChannelImageLifecycle(t *testing.T) {
	svc, blobs := testService(t)
	ctx := context.Background()
	chID := uuid.New()

	img, err := svc.SetChannelImage(ctx, chID, KindAvatar, UploadInput{Filename: "logo.webp", Reader: strings.NewReader("v1")})
	if err != nil {
		t.Fatalf("SetChannelImage: %v", err)
	}
	wantKey := "avatars/channels/" + chID.String() + ".webp"
	if img.StorageKey != wantKey || img.ContentType != "image/webp" {
		t.Fatalf("image = %+v, want key=%s type=image/webp", img, wantKey)
	}

	// Replace with the same extension: same key, new content/size.
	img2, err := svc.SetChannelImage(ctx, chID, KindAvatar, UploadInput{Filename: "logo2.webp", Reader: strings.NewReader("v2-longer")})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if img2.StorageKey != wantKey || img2.SizeBytes != 9 {
		t.Fatalf("replaced image = %+v, want same key, size 9", img2)
	}

	if err := svc.DeleteChannelImage(ctx, chID, KindAvatar); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustExist(t, blobs, wantKey, false)
	if err := svc.DeleteChannelImage(ctx, chID, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if _, err := svc.ChannelImage(ctx, chID, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Errorf("ChannelImage after delete = %v, want ErrNotFound", err)
	}
}

// TestNoStorageBackendGuard: without a blob backend, writes fail closed.
func TestNoStorageBackendGuard(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	ctx := context.Background()
	if _, err := svc.SetUserImage(ctx, uuid.New(), KindAvatar, UploadInput{Filename: "a.png", Reader: strings.NewReader("x")}); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("SetUserImage without backend = %v, want ErrStorageUnavailable", err)
	}
}

// TestImageKeyScheme pins the storage-layout contract (one top-level dir per
// asset kind; see .ralph/specs/storage-layout.md).
func TestImageKeyScheme(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	cases := []struct{ kind, ownerDir, ext, want string }{
		{KindAvatar, "users", ".png", "avatars/users/11111111-2222-3333-4444-555555555555.png"},
		{KindBanner, "users", ".jpg", "banners/users/11111111-2222-3333-4444-555555555555.jpg"},
		{KindAvatar, "channels", ".webp", "avatars/channels/11111111-2222-3333-4444-555555555555.webp"},
		{KindBanner, "channels", ".jpeg", "banners/channels/11111111-2222-3333-4444-555555555555.jpeg"},
	}
	for _, c := range cases {
		if got := imageKey(c.kind, c.ownerDir, id, c.ext); got != c.want {
			t.Errorf("imageKey(%s, %s) = %q, want %q", c.kind, c.ownerDir, got, c.want)
		}
	}
}

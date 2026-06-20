package video

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory video.Repository. Each video remembers its channel's
// owner so GetVideoByID can return the joined owner_id.
type fakeRepo struct {
	videos map[uuid.UUID]sqlcgen.GetVideoByIDRow
	files  map[uuid.UUID][]sqlcgen.VideoFile
	owner  uuid.UUID
}

func newFakeRepo(owner uuid.UUID) *fakeRepo {
	return &fakeRepo{
		videos: map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		files:  map[uuid.UUID][]sqlcgen.VideoFile{},
		owner:  owner,
	}
}

func (f *fakeRepo) CreateVideo(_ context.Context, a sqlcgen.CreateVideoParams) (sqlcgen.Video, error) {
	v := sqlcgen.Video{
		ID: uuid.New(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		OwnerID: f.owner,
	}
	return v, nil
}

func (f *fakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	return v, nil
}

func rowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (f *fakeRepo) ListVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, rowToVideo(r))
		}
	}
	return out, nil
}

func (f *fakeRepo) ListPublicVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" {
			out = append(out, rowToVideo(r))
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateVideo(_ context.Context, a sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	if a.Title != nil {
		r.Title = *a.Title
	}
	if a.Description != nil {
		r.Description = *a.Description
	}
	if a.Privacy != nil {
		r.Privacy = *a.Privacy
	}
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return rowToVideo(r), nil
}

func (f *fakeRepo) DeleteVideo(_ context.Context, id uuid.UUID) error {
	delete(f.videos, id)
	return nil
}

func (f *fakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
		CreatedAt: time.Now(),
	}
	f.files[a.VideoID] = append(f.files[a.VideoID], vf)
	return vf, nil
}

func (f *fakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	kept := f.files[a.VideoID][:0]
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind != a.Kind {
			kept = append(kept, vf)
		}
	}
	f.files[a.VideoID] = kept
	return nil
}

func (f *fakeRepo) SetVideoState(_ context.Context, a sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	r.State = a.State
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return rowToVideo(r), nil
}

func (f *fakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.Video, error) {
	q := ""
	if a.Query != nil {
		q = strings.ToLower(*a.Query)
	}
	var all []sqlcgen.Video
	for _, r := range f.videos {
		if r.Privacy == "public" && strings.Contains(strings.ToLower(r.Title), q) {
			all = append(all, rowToVideo(r))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	lo := int(a.ResultOffset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func (f *fakeRepo) ListPublicVideos(_ context.Context, a sqlcgen.ListPublicVideosParams) ([]sqlcgen.Video, error) {
	var all []sqlcgen.Video
	for _, r := range f.videos {
		if r.Privacy == "public" {
			all = append(all, rowToVideo(r))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	lo := int(a.Offset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(a.Limit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func TestCreateDraftDefaultsToDraftState(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ch := uuid.New()
	v, err := svc.CreateDraft(context.Background(), ch, CreateInput{Title: "Hello", Privacy: "private"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if v.State != "draft" {
		t.Errorf("state = %q, want draft", v.State)
	}
	if v.ChannelID != ch || v.Title != "Hello" || v.Privacy != "private" {
		t.Errorf("unexpected video: %+v", v)
	}
}

func TestGetByIDReturnsOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	created, _ := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "T", Privacy: "public"})

	got, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OwnerID != owner {
		t.Errorf("owner = %v, want %v", got.OwnerID, owner)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil)
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func strptr(s string) *string { return &s }

func TestUpdateVideoOwnerNonOwnerNotFound(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "old", Privacy: "private"})

	// Owner partial update.
	up, err := svc.Update(ctx, owner, v.ID, UpdateInput{Title: strptr("new")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Title != "new" || up.Privacy != "private" {
		t.Errorf("unexpected update: %+v", up)
	}
	// Non-owner forbidden.
	if _, err := svc.Update(ctx, uuid.New(), v.ID, UpdateInput{Title: strptr("hax")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	// Unknown id not found.
	if _, err := svc.Update(ctx, owner, uuid.New(), UpdateInput{Title: strptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err = %v, want ErrNotFound", err)
	}
}

func TestDeleteVideoOwnerAndNonOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if err := svc.Delete(ctx, uuid.New(), v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner delete err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(ctx, owner, v.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := svc.GetByID(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestListPublicPaginates(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	// 3 public + 1 private across (logically) the instance.
	for i := 0; i < 3; i++ {
		_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	}
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	page1, err := svc.ListPublic(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1 = %d, want 2", len(page1))
	}
	page2, _ := svc.ListPublic(ctx, 2, 2)
	if len(page2) != 1 { // only 3 public total
		t.Errorf("page2 = %d, want 1", len(page2))
	}
	for _, v := range append(page1, page2...) {
		if v.Privacy != "public" {
			t.Errorf("feed contained non-public video: %+v", v)
		}
	}
}

func TestSearchPublicMatchesTitleAndExcludesPrivate(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "Go concurrency", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "Rust basics", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "Go internals", Privacy: "private"})

	res, err := svc.SearchPublic(ctx, "go", 20, 0)
	if err != nil {
		t.Fatalf("SearchPublic: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Go concurrency" {
		t.Errorf("search = %+v, want only the public Go video", res)
	}
}

func TestListByChannelVsPublic(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	all, _ := svc.ListByChannel(ctx, ch)
	if len(all) != 2 {
		t.Errorf("ListByChannel = %d, want 2", len(all))
	}
	pub, _ := svc.ListPublicByChannel(ctx, ch)
	if len(pub) != 1 || pub[0].Privacy != "public" {
		t.Errorf("ListPublicByChannel = %+v, want 1 public", pub)
	}
}

func TestAttachOriginalStoresBytesAndFlipsToProcessing(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	const content = "fake original video bytes"
	updated, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "Clip.MP4", ContentType: "video/mp4", Reader: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if updated.State != "processing" {
		t.Errorf("state = %q, want processing", updated.State)
	}
	if file.Kind != "original" || file.ContentType != "video/mp4" || file.OriginalName != "Clip.MP4" {
		t.Errorf("unexpected file metadata: %+v", file)
	}
	if file.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", file.SizeBytes, len(content))
	}
	if want := "videos/" + v.ID.String() + "/original.mp4"; file.StorageKey != want {
		t.Errorf("key = %q, want %q", file.StorageKey, want)
	}
	rc, err := blobs.Open(ctx, file.StorageKey)
	if err != nil {
		t.Fatalf("Open stored blob: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != content {
		t.Errorf("stored bytes = %q, want %q", got, content)
	}
}

func TestAttachOriginalRejectsNonOwnerAndUnknown(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(newFakeRepo(owner), blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, uuid.New(), v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	if _, _, err := svc.AttachOriginal(ctx, owner, uuid.New(), UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v, want ErrNotFound", err)
	}
}

func TestAttachOriginalReplacesPreviousOriginal(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("first")}); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "b.mp4", Reader: strings.NewReader("second-take")}); err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if got := len(repo.files[v.ID]); got != 1 {
		t.Errorf("original rows = %d, want 1 (re-upload replaces)", got)
	}
}

func TestAttachOriginalWithoutStorage(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("err = %v, want ErrStorageUnavailable", err)
	}
}

func TestAcceptedExtAndOriginalKey(t *testing.T) {
	id := uuid.New()
	accepted := map[string]string{ // filename -> normalized extension
		"clip.mp4":  ".mp4",
		"clip.WEBM": ".webm",
		"a.MOV":     ".mov",
		"x.mkv":     ".mkv",
	}
	for filename, wantExt := range accepted {
		ext, ok := acceptedExt(filename)
		if !ok || ext != wantExt {
			t.Errorf("acceptedExt(%q) = (%q, %v), want (%q, true)", filename, ext, ok, wantExt)
		}
		if got, want := originalKey(id, ext), "videos/"+id.String()+"/original"+wantExt; got != want {
			t.Errorf("originalKey(%q) = %q, want %q", ext, got, want)
		}
	}
	for _, filename := range []string{"noext", "weird.tar.gz", "evil.../x.m p", "doc.pdf", "image.png", "a.exe", ""} {
		if ext, ok := acceptedExt(filename); ok {
			t.Errorf("acceptedExt(%q) = (%q, true), want false (not a video container)", filename, ext)
		}
	}
}

func TestAttachOriginalRejectsUnsupportedExtension(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "notes.pdf", Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("err = %v, want ErrUnsupportedMedia", err)
	}
	// A rejected upload stores nothing and leaves the video a draft.
	if n := len(repo.files[v.ID]); n != 0 {
		t.Errorf("file rows = %d, want 0", n)
	}
	if got, _ := svc.GetByID(ctx, v.ID); got.State != "draft" {
		t.Errorf("state = %q, want draft (unchanged)", got.State)
	}
	// Ownership is still checked before media type: a non-owner gets ErrForbidden.
	if _, _, err := svc.AttachOriginal(ctx, uuid.New(), v.ID, UploadInput{Filename: "notes.pdf", Reader: strings.NewReader("x")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
}

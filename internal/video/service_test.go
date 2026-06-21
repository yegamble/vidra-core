package video

import (
	"context"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory video.Repository. Each video remembers its channel's
// owner so GetVideoByID can return the joined owner_id.
type fakeRepo struct {
	videos   map[uuid.UUID]sqlcgen.GetVideoByIDRow
	files    map[uuid.UUID][]sqlcgen.VideoFile
	metadata map[uuid.UUID]sqlcgen.VideoMetadatum
	views    map[uuid.UUID]int64
	followed map[uuid.UUID]bool // channel IDs the test subject follows
	saved    map[uuid.UUID]bool // video IDs the test subject has saved
	owner    uuid.UUID
}

func newFakeRepo(owner uuid.UUID) *fakeRepo {
	return &fakeRepo{
		videos:   map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		files:    map[uuid.UUID][]sqlcgen.VideoFile{},
		metadata: map[uuid.UUID]sqlcgen.VideoMetadatum{},
		views:    map[uuid.UUID]int64{},
		followed: map[uuid.UUID]bool{},
		saved:    map[uuid.UUID]bool{},
		owner:    owner,
	}
}

func (f *fakeRepo) SaveVideo(_ context.Context, a sqlcgen.SaveVideoParams) error {
	f.saved[a.VideoID] = true
	return nil
}

func (f *fakeRepo) UnsaveVideo(_ context.Context, a sqlcgen.UnsaveVideoParams) error {
	delete(f.saved, a.VideoID)
	return nil
}

func (f *fakeRepo) ListSavedVideos(_ context.Context, a sqlcgen.ListSavedVideosParams) ([]sqlcgen.ListSavedVideosRow, error) {
	var rows []sqlcgen.ListSavedVideosRow
	for _, r := range f.videos {
		if f.saved[r.ID] && r.Privacy == "public" && r.State == "published" {
			rows = append(rows, sqlcgen.ListSavedVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *fakeRepo) ListSubscriptionVideos(_ context.Context, a sqlcgen.ListSubscriptionVideosParams) ([]sqlcgen.ListSubscriptionVideosRow, error) {
	var rows []sqlcgen.ListSubscriptionVideosRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && f.followed[r.ChannelID] {
			rows = append(rows, sqlcgen.ListSubscriptionVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *fakeRepo) IncrementVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	f.views[videoID]++
	return f.views[videoID], nil
}

func (f *fakeRepo) GetVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	n, ok := f.views[videoID]
	if !ok {
		return 0, errors.New("not found")
	}
	return n, nil
}

func (f *fakeRepo) UpsertVideoMetadata(_ context.Context, a sqlcgen.UpsertVideoMetadataParams) (sqlcgen.VideoMetadatum, error) {
	m := sqlcgen.VideoMetadatum{
		VideoID: a.VideoID, DurationSeconds: a.DurationSeconds, Width: a.Width, Height: a.Height,
		UpdatedAt: time.Now(),
	}
	f.metadata[a.VideoID] = m
	return m, nil
}

func (f *fakeRepo) GetVideoMetadata(_ context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, error) {
	m, ok := f.metadata[videoID]
	if !ok {
		return sqlcgen.VideoMetadatum{}, errors.New("not found")
	}
	return m, nil
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

func (f *fakeRepo) ListVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error) {
	var out []sqlcgen.ListVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeRepo) ListPublicVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListPublicVideosByChannelRow, error) {
	var out []sqlcgen.ListPublicVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" && r.State == "published" {
			out = append(out, sqlcgen.ListPublicVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
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

func (f *fakeRepo) GetVideoFileByKind(_ context.Context, a sqlcgen.GetVideoFileByKindParams) (sqlcgen.VideoFile, error) {
	var newest sqlcgen.VideoFile
	found := false
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind == a.Kind && (!found || vf.CreatedAt.After(newest.CreatedAt)) {
			newest, found = vf, true
		}
	}
	if !found {
		return sqlcgen.VideoFile{}, errors.New("not found")
	}
	return newest, nil
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

func (f *fakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.SearchPublicVideosRow, error) {
	q := ""
	if a.Query != nil {
		q = strings.ToLower(*a.Query)
	}
	var all []sqlcgen.SearchPublicVideosRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && strings.Contains(strings.ToLower(r.Title), q) {
			all = append(all, sqlcgen.SearchPublicVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
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

func (f *fakeRepo) hasThumb(id uuid.UUID) bool {
	for _, vf := range f.files[id] {
		if vf.Kind == "thumbnail" {
			return true
		}
	}
	return false
}

func (f *fakeRepo) ListPublicVideosSorted(_ context.Context, a sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error) {
	var rows []sqlcgen.ListPublicVideosSortedRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" {
			rows = append(rows, sqlcgen.ListPublicVideosSortedRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		switch a.Sort {
		case "popular":
			if rows[i].Views != rows[j].Views {
				return rows[i].Views > rows[j].Views
			}
		case "trending":
			si, sj := trendingScore(rows[i]), trendingScore(rows[j])
			if si != sj {
				return si > sj
			}
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	lo := int(a.ResultOffset)
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(rows) {
		hi = len(rows)
	}
	return rows[lo:hi], nil
}

func trendingScore(r sqlcgen.ListPublicVideosSortedRow) float64 {
	hours := time.Since(r.CreatedAt).Hours()
	return float64(r.Views) / math.Pow(hours+2, 1.5)
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

func TestListSubscriptionsOnlyFollowedChannels(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	followed, other := uuid.New(), uuid.New()
	now := time.Now()
	inFollowed, inOther := uuid.New(), uuid.New()
	repo.videos[inFollowed] = sqlcgen.GetVideoByIDRow{
		ID: inFollowed, ChannelID: followed, Title: "Followed", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now,
	}
	repo.videos[inOther] = sqlcgen.GetVideoByIDRow{
		ID: inOther, ChannelID: other, Title: "Other", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now,
	}
	repo.followed[followed] = true

	items, err := NewService(repo, nil).ListSubscriptions(context.Background(), uuid.New(), 20, 0)
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(items) != 1 || items[0].Video.ID != inFollowed {
		t.Fatalf("want only the followed-channel video, got %d items: %+v", len(items), items)
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
	// 3 public + 1 private across (logically) the instance. Only published
	// videos surface in the public feed.
	for i := 0; i < 3; i++ {
		publishDraft(t, svc, ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	}
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	page1, err := svc.ListPublic(ctx, "recent", 2, 0)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1 = %d, want 2", len(page1))
	}
	page2, _ := svc.ListPublic(ctx, "recent", 2, 2)
	if len(page2) != 1 { // only 3 public total
		t.Errorf("page2 = %d, want 1", len(page2))
	}
	for _, it := range append(page1, page2...) {
		if it.Video.Privacy != "public" {
			t.Errorf("feed contained non-public video: %+v", it)
		}
	}
}

func TestListPublicSortModes(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	ctx := context.Background()
	ch := uuid.New()
	a := publishDraft(t, svc, ctx, ch, CreateInput{Title: "a", Privacy: "public"})
	b := publishDraft(t, svc, ctx, ch, CreateInput{Title: "b", Privacy: "public"})
	// b has more views than a.
	for i := 0; i < 5; i++ {
		_, _ = repo.IncrementVideoViews(ctx, b.ID)
	}
	_, _ = repo.IncrementVideoViews(ctx, a.ID)

	popular, _ := svc.ListPublic(ctx, "popular", 20, 0)
	if len(popular) != 2 || popular[0].Video.ID != b.ID {
		t.Errorf("popular order = %v, want most-viewed (b) first", feedIDs(popular))
	}
	if popular[0].Views != 5 {
		t.Errorf("top item views = %d, want 5", popular[0].Views)
	}
	// Unknown sort falls back to recent (newest first -> b created after a).
	recent, _ := svc.ListPublic(ctx, "bogus", 20, 0)
	if len(recent) != 2 || recent[0].Video.ID != b.ID {
		t.Errorf("fallback order = %v, want recent (b first)", feedIDs(recent))
	}
}

func feedIDs(items []FeedItem) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		out = append(out, it.Video.ID)
	}
	return out
}

func TestSearchPublicMatchesTitleAndExcludesPrivate(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "Go concurrency", Privacy: "public"})
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "Rust basics", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "Go internals", Privacy: "private"})

	res, err := svc.SearchPublic(ctx, "go", 20, 0)
	if err != nil {
		t.Fatalf("SearchPublic: %v", err)
	}
	if len(res) != 1 || res[0].Video.Title != "Go concurrency" {
		t.Errorf("search = %+v, want only the public Go video", res)
	}
}

func TestListByChannelVsPublic(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	all, _ := svc.ListByChannel(ctx, ch)
	if len(all) != 2 {
		t.Errorf("ListByChannel = %d, want 2 (owner sees all states)", len(all))
	}
	pub, _ := svc.ListPublicByChannel(ctx, ch)
	if len(pub) != 1 || pub[0].Video.Privacy != "public" {
		t.Errorf("ListPublicByChannel = %+v, want 1 public", pub)
	}
}

// publishDraft creates a draft and publishes it via Process (nil-prober path),
// returning the created video. Used by feed/search tests that need published rows.
func publishDraft(t *testing.T, svc *Service, ctx context.Context, ch uuid.UUID, in CreateInput) sqlcgen.Video {
	t.Helper()
	v, err := svc.CreateDraft(ctx, ch, in)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := svc.Process(ctx, v.ID, ""); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return v
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

type fakeProber struct {
	md  media.Metadata
	err error
}

func (p fakeProber) Probe(_ context.Context, _ string) (media.Metadata, error) { return p.md, p.err }

func TestProcessPublishesWithoutProber(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "videos/x/original.mp4")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (no prober trusts the original)", got.State)
	}
}

func TestProcessPublishesWhenProberSucceeds(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil, WithProber(fakeProber{}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, _ := svc.Process(ctx, v.ID, "k")
	if got.State != "published" {
		t.Errorf("state = %q, want published", got.State)
	}
}

func TestProcessFailsWhenProberErrors(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo, nil, WithProber(fakeProber{err: errors.New("not media")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil { // a probe failure is a state transition, not a call error
		t.Fatalf("Process returned err: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed", got.State)
	}
	if _, ok, _ := svc.GetMetadata(ctx, v.ID); ok {
		t.Error("metadata stored for a failed probe, want none")
	}
}

func TestProcessPersistsProbeMetadata(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo, nil, WithProber(fakeProber{md: media.Metadata{DurationSeconds: 42, Width: 1280, Height: 720}}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "k"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	md, ok, err := svc.GetMetadata(ctx, v.ID)
	if err != nil || !ok {
		t.Fatalf("GetMetadata ok=%v err=%v, want found", ok, err)
	}
	if md.DurationSeconds == nil || *md.DurationSeconds != 42 {
		t.Errorf("duration = %v, want 42", md.DurationSeconds)
	}
	if md.Width == nil || *md.Width != 1280 || md.Height == nil || *md.Height != 720 {
		t.Errorf("dimensions = %v x %v, want 1280x720", md.Width, md.Height)
	}
}

func TestProcessLeavesUnknownMetadataNull(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	// Audio-only style probe: duration known, dimensions zero (unknown).
	svc := NewService(repo, nil, WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10}}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "k"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	md, _, _ := svc.GetMetadata(ctx, v.ID)
	if md.DurationSeconds == nil || *md.DurationSeconds != 10 {
		t.Errorf("duration = %v, want 10", md.DurationSeconds)
	}
	if md.Width != nil || md.Height != nil {
		t.Errorf("dimensions = %v x %v, want NULL/NULL", md.Width, md.Height)
	}
}

type fakeThumbnailer struct {
	jpg []byte
	err error
}

func (f fakeThumbnailer) Thumbnail(_ context.Context, _ string, _ int) ([]byte, error) {
	return f.jpg, f.err
}

func TestProcessStoresThumbnail(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	jpg := []byte("\xff\xd8\xff\xe0fakejpeg")
	svc := NewService(repo, blobs, WithThumbnailer(fakeThumbnailer{jpg: jpg}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "videos/x/original.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !svc.HasThumbnail(ctx, v.ID) {
		t.Fatal("HasThumbnail = false, want true")
	}
	f, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "thumbnail")
	if err != nil {
		t.Fatalf("FileForView thumbnail: %v", err)
	}
	if f.ContentType != "image/jpeg" || f.StorageKey != "videos/"+v.ID.String()+"/thumbnail.jpg" {
		t.Errorf("unexpected thumbnail file: %+v", f)
	}
	rc, err := blobs.Open(ctx, f.StorageKey)
	if err != nil {
		t.Fatalf("open stored thumbnail: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != string(jpg) {
		t.Errorf("stored thumbnail bytes = %q, want %q", got, jpg)
	}
}

func TestProcessThumbnailFailureStillPublishes(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithThumbnailer(fakeThumbnailer{err: errors.New("ffmpeg boom")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (thumbnail failure is non-fatal)", got.State)
	}
	if svc.HasThumbnail(ctx, v.ID) {
		t.Error("thumbnail stored despite generator error")
	}
}

func TestProcessFailedProbeSkipsThumbnail(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs,
		WithProber(fakeProber{err: errors.New("bad media")}),
		WithThumbnailer(fakeThumbnailer{jpg: []byte("x")}),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, _ := svc.Process(ctx, v.ID, "k")
	if got.State != "failed" {
		t.Errorf("state = %q, want failed", got.State)
	}
	if svc.HasThumbnail(ctx, v.ID) {
		t.Error("thumbnail generated for a failed video")
	}
}

type fakeDeduper struct{ seen map[string]bool }

func newFakeDeduper() *fakeDeduper { return &fakeDeduper{seen: map[string]bool{}} }

func (d *fakeDeduper) First(_ context.Context, key string, _ time.Duration) (bool, error) {
	if d.seen[key] {
		return false, nil
	}
	d.seen[key] = true
	return true, nil
}

func TestRecordViewDedupesPerViewer(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil, WithViewDeduper(newFakeDeduper()))
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	for i := 0; i < 3; i++ { // viewer A pings 3x -> counts once
		if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "viewerA"); err != nil {
			t.Fatalf("RecordView A: %v", err)
		}
	}
	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "viewerB"); err != nil { // distinct viewer -> +1
		t.Fatalf("RecordView B: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 2 {
		t.Errorf("views = %d, want 2 (one per distinct viewer)", got)
	}
}

func TestRecordViewWithoutDeduperCountsEach(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	_ = svc.RecordView(ctx, v.ID, uuid.Nil, false, "x")
	_ = svc.RecordView(ctx, v.ID, uuid.Nil, false, "x")
	if got := svc.Views(ctx, v.ID); got != 2 {
		t.Errorf("views = %d, want 2 (no dedupe)", got)
	}
}

func TestRecordViewUnpublishedIsNoOp(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"}) // draft, not processed
	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "x"); err != nil {
		t.Fatalf("RecordView: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 0 {
		t.Errorf("views = %d, want 0 (drafts do not accrue views)", got)
	}
}

func TestRecordViewVisibilityAndUnknown(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("anon view of private = %v, want ErrNotFound", err)
	}
	if err := svc.RecordView(ctx, v.ID, owner, true, "owner"); err != nil {
		t.Fatalf("owner view of own private: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 1 {
		t.Errorf("views = %d, want 1", got)
	}
	if err := svc.RecordView(ctx, uuid.New(), uuid.Nil, false, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("view of unknown = %v, want ErrNotFound", err)
	}
}

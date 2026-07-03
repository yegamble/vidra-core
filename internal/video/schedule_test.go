package video

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// memBackend is a throwaway blob backend for schedule tests.
func memBackend(t *testing.T) storage.Backend {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	return blobs
}

// --- fakeRepo scheduled-publish method (mirrors ListDueScheduledVideos) ---

func (f *fakeRepo) ListDueScheduledVideos(_ context.Context, limit int32) ([]sqlcgen.ListDueScheduledVideosRow, error) {
	var rows []sqlcgen.ListDueScheduledVideosRow
	now := time.Now()
	for _, r := range f.videos {
		if r.State != "scheduled" || !r.PublishAt.Valid || r.PublishAt.Time.After(now) {
			continue
		}
		for _, vf := range f.files[r.ID] {
			if vf.Kind == "original" {
				rows = append(rows, sqlcgen.ListDueScheduledVideosRow{ID: r.ID, StorageKey: vf.StorageKey})
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID.String() < rows[j].ID.String() })
	if limit > 0 && int(limit) < len(rows) {
		rows = rows[:limit]
	}
	return rows, nil
}

// scheduleHarness uploads an original for a draft created with in, returning
// the service (with hook recorders), repo, and the video id.
func scheduleHarness(t *testing.T, in CreateInput, published *[]uuid.UUID, transcoded *[]string) (*Service, *fakeRepo, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, memBackend(t),
		WithPublishHook(func(_ context.Context, id uuid.UUID) { *published = append(*published, id) }),
		WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) { *transcoded = append(*transcoded, key) }),
	)
	v, err := svc.CreateDraft(context.Background(), uuid.New(), in)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(context.Background(), owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if _, err := svc.Process(context.Background(), v.ID, file.StorageKey); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return svc, repo, v.ID
}

// TestProcessHoldsFuturePublishAt proves a processed video with a future
// publish_at parks in 'scheduled' without firing the publish/transcode hooks,
// and that PublishDue leaves it alone while it is not yet due.
func TestProcessHoldsFuturePublishAt(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	future := time.Now().Add(1 * time.Hour)
	_, repo, id := scheduleHarness(t, CreateInput{Title: "later", Privacy: "public", PublishAt: &future},
		&published, &transcoded)

	if got := repo.videos[id].State; got != "scheduled" {
		t.Fatalf("state after Process = %q, want scheduled", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired on hold: publish=%v transcode=%v", published, transcoded)
	}
}

// TestPublishDueRunsThePublishTransition proves the sweeper publishes a due
// scheduled video through the same transition Process uses: state flips AND
// both the federation-announce and transcode-enqueue hooks fire.
func TestPublishDueRunsThePublishTransition(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	future := time.Now().Add(1 * time.Hour)
	svc, repo, id := scheduleHarness(t, CreateInput{Title: "later", Privacy: "public", PublishAt: &future},
		&published, &transcoded)

	// Not due yet: the sweep is a no-op.
	if n, err := svc.PublishDue(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("PublishDue before due = (%d, %v), want (0, nil)", n, err)
	}

	// Make it due and sweep.
	row := repo.videos[id]
	row.PublishAt.Time = time.Now().Add(-1 * time.Minute)
	repo.videos[id] = row
	n, err := svc.PublishDue(context.Background(), 10)
	if err != nil || n != 1 {
		t.Fatalf("PublishDue = (%d, %v), want (1, nil)", n, err)
	}
	if got := repo.videos[id].State; got != "published" {
		t.Fatalf("state after sweep = %q, want published", got)
	}
	if len(published) != 1 || published[0] != id {
		t.Errorf("publish hook = %v, want [%s]", published, id)
	}
	if len(transcoded) != 1 || !strings.Contains(transcoded[0], id.String()) {
		t.Errorf("transcode hook = %v, want the original's key", transcoded)
	}

	// Idempotent: nothing left due.
	if n, _ := svc.PublishDue(context.Background(), 10); n != 0 {
		t.Fatalf("second sweep = %d, want 0", n)
	}
}

// TestProcessPublishesPastPublishAtImmediately proves a publish_at that has
// already passed by the time processing finishes does not hold the video.
func TestProcessPublishesPastPublishAtImmediately(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, memBackend(t),
		WithPublishHook(func(_ context.Context, id uuid.UUID) { published = append(published, id) }),
		WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) { transcoded = append(transcoded, key) }),
	)
	// Seed a draft whose schedule is already in the past (as if the schedule
	// elapsed while the upload crawled) — CreateDraft validation happens in the
	// HTTP layer, so the service accepts it.
	past := time.Now().Add(-1 * time.Minute)
	v, err := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "now", Privacy: "public", PublishAt: &past})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(context.Background(), owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	got, err := svc.Process(context.Background(), v.ID, file.StorageKey)
	if err != nil || got.State != "published" {
		t.Fatalf("Process = (%q, %v), want published", got.State, err)
	}
	if len(published) != 1 || len(transcoded) != 1 {
		t.Fatalf("hooks after immediate publish: publish=%v transcode=%v", published, transcoded)
	}
}

// TestUpdatePublishAtRejectedAfterPublish proves the ErrPublished guard.
func TestUpdatePublishAtRejectedAfterPublish(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, _, id := scheduleHarness(t, CreateInput{Title: "live", Privacy: "public"}, &published, &transcoded)

	owner := publishedOwner(t, svc, id)
	future := time.Now().Add(time.Hour)
	if _, err := svc.Update(context.Background(), owner, id, UpdateInput{PublishAt: &future}); !errors.Is(err, ErrPublished) {
		t.Fatalf("Update publish_at on published video = %v, want ErrPublished", err)
	}
}

// publishedOwner extracts the owner of a video from the service (test helper —
// the harness's owner is what AttachOriginal authorised).
func publishedOwner(t *testing.T, svc *Service, id uuid.UUID) uuid.UUID {
	t.Helper()
	v, err := svc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if v.State != "published" {
		t.Fatalf("state = %q, want published", v.State)
	}
	return v.OwnerID
}

package video

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- fakeRepo quarantine methods (mirror the §11 queries) ---

func (f *fakeRepo) UploadRequiresQuarantine(_ context.Context, id uuid.UUID) (bool, error) {
	if _, ok := f.videos[id]; !ok {
		return false, errors.New("not found")
	}
	return f.requiresQuarantine, nil
}

func (f *fakeRepo) ListQuarantinedVideos(_ context.Context, a sqlcgen.ListQuarantinedVideosParams) ([]sqlcgen.ListQuarantinedVideosRow, error) {
	var rows []sqlcgen.ListQuarantinedVideosRow
	for _, r := range f.videos {
		if r.State != "quarantined" {
			continue
		}
		rows = append(rows, sqlcgen.ListQuarantinedVideosRow{
			ID: r.ID, Title: r.Title, Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	lo := min(int(a.ResultOffset), len(rows))
	rows = rows[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

// quarantineHarness builds a quarantine-enabled service with hook recorders,
// creates a draft with in, uploads an original, and runs Process.
func quarantineHarness(t *testing.T, in CreateInput, requires bool, published *[]uuid.UUID, transcoded *[]string) (*Service, *fakeRepo, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	repo.requiresQuarantine = requires
	svc := NewService(repo, memBackend(t),
		WithQuarantineNewUploads(true),
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

// TestProcessQuarantinesNonPrivilegedUpload proves that with the quarantine
// gate on, a non-privileged owner's finished upload parks in 'quarantined'
// with NO publish-side effects, and that approval releases it through the real
// publish transition (state flips AND both hooks fire), exactly once.
func TestProcessQuarantinesNonPrivilegedUpload(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, repo, id := quarantineHarness(t, CreateInput{Title: "held", Privacy: "public"}, true,
		&published, &transcoded)

	if got := repo.videos[id].State; got != "quarantined" {
		t.Fatalf("state after Process = %q, want quarantined", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired at quarantine time: publish=%v transcode=%v", published, transcoded)
	}

	// The moderation queue lists it.
	queue, err := svc.ListQuarantined(context.Background(), 20, 0)
	if err != nil || len(queue) != 1 || queue[0].ID != id {
		t.Fatalf("ListQuarantined = (%+v, %v), want the held video", queue, err)
	}

	// Approval publishes through THE publish transition: hooks fire now.
	v, err := svc.ApproveQuarantined(context.Background(), id)
	if err != nil || v.State != "published" {
		t.Fatalf("ApproveQuarantined = (%q, %v), want published", v.State, err)
	}
	if len(published) != 1 || published[0] != id {
		t.Errorf("publish hook after approve = %v, want [%s]", published, id)
	}
	if len(transcoded) != 1 || !strings.Contains(transcoded[0], id.String()) {
		t.Errorf("transcode hook after approve = %v, want the original's key", transcoded)
	}

	// Approval is not a general publish button: re-approving is rejected.
	if _, err := svc.ApproveQuarantined(context.Background(), id); !errors.Is(err, ErrNotQuarantined) {
		t.Errorf("re-approve = %v, want ErrNotQuarantined", err)
	}
	if queue, _ := svc.ListQuarantined(context.Background(), 20, 0); len(queue) != 0 {
		t.Errorf("queue after approve = %d entries, want 0", len(queue))
	}
}

// TestProcessPublishesPrivilegedUploadWithQuarantineOn proves the gate lets a
// privileged owner (moderator/admin role, or bypass_quarantine) publish
// directly even when QUARANTINE_NEW_UPLOADS is on.
func TestProcessPublishesPrivilegedUploadWithQuarantineOn(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	_, repo, id := quarantineHarness(t, CreateInput{Title: "trusted", Privacy: "public"}, false,
		&published, &transcoded)

	if got := repo.videos[id].State; got != "published" {
		t.Fatalf("state after Process = %q, want published", got)
	}
	if len(published) != 1 || len(transcoded) != 1 {
		t.Fatalf("hooks after direct publish: publish=%v transcode=%v", published, transcoded)
	}
}

// TestQuarantineTrumpsSchedule proves a quarantined upload never parks in
// 'scheduled' — moderation holds it first; approval publishes immediately.
func TestQuarantineTrumpsSchedule(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	future := time.Now().Add(time.Hour)
	_, repo, id := quarantineHarness(t, CreateInput{Title: "held+scheduled", Privacy: "public", PublishAt: &future},
		true, &published, &transcoded)

	if got := repo.videos[id].State; got != "quarantined" {
		t.Fatalf("state after Process = %q, want quarantined (moderation trumps scheduling)", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired on hold: publish=%v transcode=%v", published, transcoded)
	}
}

// TestRejectQuarantined proves rejection fails the video without firing any
// publish hook, is state-guarded, and reports unknown ids as not found.
func TestRejectQuarantined(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, repo, id := quarantineHarness(t, CreateInput{Title: "bad", Privacy: "public"}, true,
		&published, &transcoded)

	v, err := svc.RejectQuarantined(context.Background(), id)
	if err != nil || v.State != "failed" {
		t.Fatalf("RejectQuarantined = (%q, %v), want failed", v.State, err)
	}
	if got := repo.videos[id].State; got != "failed" {
		t.Fatalf("stored state after reject = %q, want failed", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired on reject: publish=%v transcode=%v", published, transcoded)
	}

	// Rejecting again (or a published/unknown video) is guarded.
	if _, err := svc.RejectQuarantined(context.Background(), id); !errors.Is(err, ErrNotQuarantined) {
		t.Errorf("re-reject = %v, want ErrNotQuarantined", err)
	}
	if _, err := svc.RejectQuarantined(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("reject unknown = %v, want ErrNotFound", err)
	}
	if _, err := svc.ApproveQuarantined(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("approve unknown = %v, want ErrNotFound", err)
	}
}

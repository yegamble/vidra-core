package video

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestProcessInfectedDropsStoredOriginal: an INFECTED verdict must leave nothing
// behind for the download surfaces to advertise or serve. Before this, Process
// failed the video but kept both the video_files row and the stored object, so
// GET /videos/{id}/download listed the infected file and
// GET /videos/{id}/download/original served its bytes to the owner (and to an
// admin) — the exact "never publish/link rejected bytes" clause.
//
// ReplaceSource has always dropped the rejected candidate blob; this is the same
// posture on the first-upload path.
func TestProcessInfectedDropsStoredOriginal(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithScanner(fakeScanner{clean: false}))
	ctx := context.Background()

	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("infected-bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}

	got, err := svc.Process(ctx, v.ID, file.StorageKey)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	// No file row: the download listing has nothing to advertise.
	if _, err := repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{
		VideoID: v.ID, Kind: "original",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetVideoFileByKind err = %v, want pgx.ErrNoRows (the row must be gone)", err)
	}
	// No object: the bytes cannot be served even by key.
	if rc, err := blobs.Open(ctx, file.StorageKey); err == nil {
		_ = rc.Close()
		t.Errorf("stored object %q still exists after an infected verdict", file.StorageKey)
	}
}

// TestProcessKeepsOriginalWhenOnlyUnscannable: an unscannable file is not proven
// bad, and under quarantine mode a moderator has to be able to review it — so
// neither fail-closed nor quarantine may drop the bytes. Only an INFECTED
// verdict does.
func TestProcessKeepsOriginalWhenOnlyUnscannable(t *testing.T) {
	for _, mode := range []string{"fail-closed", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			owner := uuid.New()
			repo := newFakeRepo(owner)
			blobs, _ := storage.NewLocal(t.TempDir())
			svc := NewService(repo, blobs,
				WithScanner(fakeScanner{err: errors.New("clamd down")}), WithScanMode(mode))
			ctx := context.Background()

			v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
			if err != nil {
				t.Fatalf("CreateDraft: %v", err)
			}
			_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
				Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("unscanned"),
			})
			if err != nil {
				t.Fatalf("AttachOriginal: %v", err)
			}
			if _, err := svc.Process(ctx, v.ID, file.StorageKey); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if _, err := repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{
				VideoID: v.ID, Kind: "original",
			}); err != nil {
				t.Errorf("GetVideoFileByKind err = %v, want the row kept", err)
			}
			rc, err := blobs.Open(ctx, file.StorageKey)
			if err != nil {
				t.Errorf("stored object %q was dropped for a mere scan error", file.StorageKey)
			} else {
				_ = rc.Close()
			}
		})
	}
}

package comment

// Comment approval (migration 0161): a held comment is stored, but none of the
// federation seams fire for it until a manager approves it, and approval is a
// one-way transition with exactly one winner.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateHeldStoresHeldAndFiresNoFederationHook(t *testing.T) {
	repo := newFakeRepo()
	var created, updated, deleted []uuid.UUID
	svc := NewService(repo,
		WithCreateHook(func(_ context.Context, id uuid.UUID) { created = append(created, id) }),
		WithUpdateHook(func(_ context.Context, id uuid.UUID) { updated = append(updated, id) }),
		WithDeleteHook(func(_ context.Context, id, _, _ uuid.UUID) { deleted = append(deleted, id) }),
	)
	ctx := context.Background()
	videoID, author := uuid.New(), uuid.New()

	c, err := svc.CreateHeld(ctx, videoID, author, "first!", nil)
	if err != nil {
		t.Fatalf("CreateHeld: %v", err)
	}
	if !c.HeldForReview || !repo.comments[c.ID].HeldForReview {
		t.Fatalf("held_for_review = %v (stored %v), want true", c.HeldForReview, repo.comments[c.ID].HeldForReview)
	}
	// The author may still edit and delete it, but nothing federates: the Note
	// was never announced, so neither an Update nor a Delete has anything to
	// refer to on a remote instance.
	if _, err := svc.Edit(ctx, c.ID, author, "edited"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if err := svc.Delete(ctx, c.ID, author, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(created)+len(updated)+len(deleted) != 0 {
		t.Errorf("federation hooks fired for a held comment: create=%v update=%v delete=%v", created, updated, deleted)
	}
}

func TestCreateRefusesAReplyToAHeldComment(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	videoID := uuid.New()
	held, err := svc.CreateHeld(ctx, videoID, uuid.New(), "waiting", nil)
	if err != nil {
		t.Fatalf("CreateHeld: %v", err)
	}
	// Nobody — not even a manager — may build a thread under a comment the
	// public cannot see: the reply would be published as an orphan.
	if _, err := svc.Create(ctx, videoID, uuid.New(), "reply", &held.ID); !errors.Is(err, ErrParentHeld) {
		t.Errorf("reply to held parent err = %v, want ErrParentHeld", err)
	}
	if _, err := svc.CreateHeld(ctx, videoID, uuid.New(), "reply", &held.ID); !errors.Is(err, ErrParentHeld) {
		t.Errorf("held reply to held parent err = %v, want ErrParentHeld", err)
	}
}

func TestApproveIsOneWayAndFiresTheCreateHookOnce(t *testing.T) {
	repo := newFakeRepo()
	var created []uuid.UUID
	svc := NewService(repo, WithCreateHook(func(_ context.Context, id uuid.UUID) { created = append(created, id) }))
	ctx := context.Background()
	author := uuid.New()
	repo.authors[author] = author2("eve")
	c, err := svc.CreateHeld(ctx, uuid.New(), author, "please", nil)
	if err != nil {
		t.Fatalf("CreateHeld: %v", err)
	}

	got, released, err := svc.Approve(ctx, c.ID, c.UpdatedAt)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !released || got.Comment.HeldForReview || repo.comments[c.ID].HeldForReview {
		t.Fatalf("first approve released=%v held=%v, want released and no longer held", released, got.Comment.HeldForReview)
	}
	if got.AuthorUsername != "eve" {
		t.Errorf("approve view author = %q, want eve", got.AuthorUsername)
	}
	if len(created) != 1 || created[0] != c.ID {
		t.Fatalf("create hook after approve = %v, want [%s]", created, c.ID)
	}

	// Idempotent: a second approval succeeds, releases nothing, fires nothing.
	if _, released, err := svc.Approve(ctx, c.ID, c.UpdatedAt); err != nil || released {
		t.Errorf("second approve released=%v err=%v, want false/nil", released, err)
	}
	if len(created) != 1 {
		t.Errorf("create hook fired %d times, want once", len(created))
	}
	if _, _, err := svc.Approve(ctx, uuid.New(), time.Time{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("approve unknown err = %v, want ErrNotFound", err)
	}
}

func TestPinRefusesAHeldComment(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	c, _ := svc.CreateHeld(ctx, uuid.New(), uuid.New(), "waiting", nil)
	if _, err := svc.Pin(ctx, c.ID); !errors.Is(err, ErrHeld) {
		t.Errorf("pin held err = %v, want ErrHeld", err)
	}
}

func author2(username string) author { return author{username, username} }

// TestEditForReviewReholdsAndRetracts: an approved comment edited where the
// edit must be reviewed (a non-reviewer on a requires_approval video) goes
// back to held, its new text is NOT federated, and the Note already announced
// is retracted; an unchanged body re-holds nothing. A plain Edit (a reviewer's
// own) stays published and federates the update as before.
func TestEditForReviewReholdsAndRetracts(t *testing.T) {
	repo := newFakeRepo()
	var updated, deleted []uuid.UUID
	svc := NewService(repo,
		WithUpdateHook(func(_ context.Context, id uuid.UUID) { updated = append(updated, id) }),
		WithDeleteHook(func(_ context.Context, id, _, _ uuid.UUID) { deleted = append(deleted, id) }),
	)
	ctx := context.Background()
	author := uuid.New()
	c, _ := svc.Create(ctx, uuid.New(), author, "approved text", nil)

	if got, err := svc.EditForReview(ctx, c.ID, author, "approved text"); err != nil || got.HeldForReview {
		t.Fatalf("unchanged body: held=%v err=%v, want still published", got.HeldForReview, err)
	}
	got, err := svc.EditForReview(ctx, c.ID, author, "something else")
	if err != nil || !got.HeldForReview {
		t.Fatalf("changed body: held=%v err=%v, want re-held", got.HeldForReview, err)
	}
	if len(updated) != 1 || len(deleted) != 1 || deleted[0] != c.ID {
		t.Errorf("hooks: update=%v delete=%v, want the retract (delete) only — plus the unchanged edit's update", updated, deleted)
	}
	// Editing again while held federates nothing at all.
	if _, err := svc.EditForReview(ctx, c.ID, author, "third"); err != nil {
		t.Fatalf("held edit: %v", err)
	}
	if len(updated) != 1 || len(deleted) != 1 {
		t.Errorf("a held edit fired hooks: update=%v delete=%v", updated, deleted)
	}
	if _, err := svc.EditForReview(ctx, c.ID, uuid.New(), "hijack"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-author EditForReview err = %v, want ErrForbidden", err)
	}

	other, _ := svc.Create(ctx, uuid.New(), author, "reviewer's", nil)
	if got, err := svc.Edit(ctx, other.ID, author, "reviewer's, edited"); err != nil || got.HeldForReview {
		t.Errorf("plain Edit: held=%v err=%v, want published", got.HeldForReview, err)
	}
}

// TestApproveRefusesAnEditedVersion: approving the version the reviewer saw
// fails with ErrStale once the held comment was edited since, and releases
// nothing; the current version approves.
func TestApproveRefusesAnEditedVersion(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	author := uuid.New()
	c, _ := svc.CreateHeld(ctx, uuid.New(), author, "seen", nil)
	time.Sleep(time.Millisecond)
	edited, _ := svc.EditForReview(ctx, c.ID, author, "changed")
	if _, released, err := svc.Approve(ctx, c.ID, c.UpdatedAt); !errors.Is(err, ErrStale) || released {
		t.Fatalf("stale approve released=%v err=%v, want ErrStale", released, err)
	}
	if !repo.comments[c.ID].HeldForReview {
		t.Fatal("a stale approval released the comment")
	}
	if _, released, err := svc.Approve(ctx, c.ID, edited.UpdatedAt); err != nil || !released {
		t.Errorf("current approve released=%v err=%v, want released", released, err)
	}
}

package federation

// Comment approval (migration 0161): a comment held for review never leaves
// this instance — no Create/Update{Note}, no dereferenceable Note, no resumed
// delivery — until a manager approves it; and an inbound Note on a
// requires_approval video is stored held.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHeldCommentNeverFederatesUntilApproved(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	c.HeldForReview = true
	repo.commentsByID[c.ID] = c
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()

	if err := svc.AnnounceComment(ctx, c.ID); err != nil {
		t.Fatalf("AnnounceComment: %v", err)
	}
	if err := svc.UpdateComment(ctx, c.ID); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("held comment queued %d deliveries, want 0", len(repo.deliveries))
	}
	if _, err := svc.NoteObject(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("NoteObject(held) err = %v, want ErrNotFound", err)
	}
	if ok, err := svc.commentStillPublishable(ctx, c.ID); err != nil || ok {
		t.Errorf("commentStillPublishable(held) = %v, %v; want false", ok, err)
	}

	// Approval flips the row; the create hook then announces it normally.
	c.HeldForReview = false
	repo.commentsByID[c.ID] = c
	if err := svc.AnnounceComment(ctx, c.ID); err != nil {
		t.Fatalf("AnnounceComment (approved): %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Errorf("approved comment deliveries = %d, want 1", len(repo.deliveries))
	}
	if _, err := svc.NoteObject(ctx, c.ID); err != nil {
		t.Errorf("NoteObject(approved) err = %v, want the Note", err)
	}
}

func TestInboundNoteOnApprovalVideoIsHeld(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	v := repo.videosByID[videoID]
	v.CommentsPolicy = "requires_approval"
	repo.videosByID[videoID] = v
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()
	videoURL := noteBase + "/videos/" + videoID.String()

	note := createNote("https://remote.example/act/h1", remoteBob,
		"https://remote.example/comments/h1", remoteBob, videoURL, "please approve me")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(note)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	held := onlyComment(t, repo)
	if !held.HeldForReview {
		t.Fatal("inbound Note on a requires_approval video was published, want held")
	}

	// A reply addressed to the held comment is dropped: nobody here can see
	// its parent, and the thread would surface it as an orphan.
	reply := createNote("https://remote.example/act/h2", remoteBob,
		"https://remote.example/comments/h2", remoteBob, "https://remote.example/comments/h1", "me again")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(reply)); err != nil {
		t.Fatalf("HandleInbox (reply): %v", err)
	}
	if len(repo.commentsByID) != 1 {
		t.Errorf("reply to a held comment was stored (%d comments)", len(repo.commentsByID))
	}

	// Once approved, a re-delivered Create (the upsert path) must not re-hold.
	held.HeldForReview = false
	repo.commentsByID[held.ID] = held
	redeliver := createNote("https://remote.example/act/h3", remoteBob,
		"https://remote.example/comments/h1", remoteBob, videoURL, "please approve me")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(redeliver)); err != nil {
		t.Fatalf("HandleInbox (redeliver): %v", err)
	}
	if onlyComment(t, repo).HeldForReview {
		t.Error("re-delivered Note re-held an approved comment")
	}
}

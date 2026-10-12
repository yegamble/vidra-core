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

// TestInboundHeldNoteFiresTheHeldHook: a Note stored held tells the video's
// managers (the hook cmd/api wires to NotifyCommentHeld); a published one
// does not.
func TestInboundHeldNoteFiresTheHeldHook(t *testing.T) {
	approval, open := uuid.New(), uuid.New()
	repo := newNotesRepo(approval)
	v := repo.videosByID[approval]
	v.CommentsPolicy = "requires_approval"
	repo.videosByID[approval] = v
	repo.videosByID[open] = v
	o := repo.videosByID[open]
	o.ID, o.CommentsPolicy = open, "enabled"
	repo.videosByID[open] = o
	var fired []uuid.UUID
	svc := NewService(repo, WithBaseURL(noteBase),
		WithHeldCommentHook(func(_ context.Context, id uuid.UUID) { fired = append(fired, id) }))
	ctx := context.Background()

	for i, vid := range []uuid.UUID{approval, open} {
		n := string(rune('a' + i))
		note := createNote("https://remote.example/act/k"+n, remoteBob,
			"https://remote.example/comments/k"+n, remoteBob, noteBase+"/videos/"+vid.String(), "hello "+n)
		if err := svc.HandleInbox(ctx, remoteBob, []byte(note)); err != nil {
			t.Fatalf("HandleInbox %s: %v", n, err)
		}
	}
	var heldID uuid.UUID
	for id, c := range repo.commentsByID {
		if c.HeldForReview {
			heldID = id
		}
	}
	if len(fired) != 1 || fired[0] != heldID {
		t.Errorf("held hook fired %v, want exactly the held comment %s", fired, heldID)
	}
}

// updateNote renders an Update{Note} activity JSON.
func updateNote(actID, actor, noteID, content string) string {
	return `{"id":"` + actID + `","type":"Update","actor":"` + actor + `","object":{` +
		`"id":"` + noteID + `","type":"Note","attributedTo":"` + actor + `","content":` + quote(content) + `}}`
}

// TestRemoteBodyChangeReholdsOnApprovalVideo: on a requires_approval video an
// approved remote comment whose body changes — by Update{Note} or by a
// re-delivered Create — goes back to held; an unchanged body does not.
func TestRemoteBodyChangeReholdsOnApprovalVideo(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	v := repo.videosByID[videoID]
	v.CommentsPolicy = "requires_approval"
	repo.videosByID[videoID] = v
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()
	videoURL := noteBase + "/videos/" + videoID.String()
	const noteID = "https://remote.example/comments/r1"
	approve := func() {
		c := onlyComment(t, repo)
		c.HeldForReview = false
		repo.commentsByID[c.ID] = c
	}

	if err := svc.HandleInbox(ctx, remoteBob, []byte(createNote("https://remote.example/act/r1", remoteBob, noteID, remoteBob, videoURL, "v1"))); err != nil {
		t.Fatalf("create: %v", err)
	}
	approve()
	if err := svc.HandleInbox(ctx, remoteBob, []byte(updateNote("https://remote.example/act/r2", remoteBob, noteID, "v1"))); err != nil {
		t.Fatalf("same-body update: %v", err)
	}
	if onlyComment(t, repo).HeldForReview {
		t.Fatal("an Update with an unchanged body re-held the comment")
	}
	if err := svc.HandleInbox(ctx, remoteBob, []byte(updateNote("https://remote.example/act/r3", remoteBob, noteID, "v2"))); err != nil {
		t.Fatalf("edit update: %v", err)
	}
	if c := onlyComment(t, repo); !c.HeldForReview || c.Body != "v2" {
		t.Fatalf("edited remote comment = held %v body %q, want held with the new body", c.HeldForReview, c.Body)
	}
	approve()
	if err := svc.HandleInbox(ctx, remoteBob, []byte(createNote("https://remote.example/act/r4", remoteBob, noteID, remoteBob, videoURL, "v3"))); err != nil {
		t.Fatalf("re-delivered create: %v", err)
	}
	if !onlyComment(t, repo).HeldForReview {
		t.Error("a re-delivered Create with a new body published unreviewed")
	}
}

// TestInboundNoteOnDisabledVideoIsDropped: a Note on a video whose comments are
// off — per video or instance-wide — is accepted and ignored, never stored.
func TestInboundNoteOnDisabledVideoIsDropped(t *testing.T) {
	for name, tc := range map[string]struct {
		policy  string
		enabled bool
	}{"video policy disabled": {"disabled", true}, "instance comments off": {"enabled", false}} {
		videoID := uuid.New()
		repo := newNotesRepo(videoID)
		v := repo.videosByID[videoID]
		v.CommentsPolicy = tc.policy
		repo.videosByID[videoID] = v
		svc := NewService(repo, WithBaseURL(noteBase), WithCommentsEnabledFunc(func() bool { return tc.enabled }))
		note := createNote("https://remote.example/act/d1", remoteBob, "https://remote.example/comments/d1",
			remoteBob, noteBase+"/videos/"+videoID.String(), "hello")
		if err := svc.HandleInbox(context.Background(), remoteBob, []byte(note)); err != nil {
			t.Fatalf("%s: HandleInbox: %v", name, err)
		}
		if len(repo.commentsByID) != 0 {
			t.Errorf("%s: stored %d comments, want 0", name, len(repo.commentsByID))
		}
	}
}

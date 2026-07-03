package atproto

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedPublishable wires a fake repo with a public video owned by a linked,
// auto-posting account, and enqueues one pending post for it. Returns the ids.
func seedPublishable(t *testing.T, repo *fakeRepo) (uid, vid uuid.UUID) {
	t.Helper()
	uid = uuid.New()
	vid = uuid.New()
	repo.videos[vid] = sqlcgen.GetATProtoPostVideoRow{ID: vid, Title: "Launch Day", Privacy: "public", OwnerID: uid}
	repo.accounts[uid] = sqlcgen.AtprotoAccount{
		UserID: uid, Handle: "alice.bsky.social", Did: "did:plc:test",
		PdsUrl: DefaultPDSURL, AutoPost: true, AppPasswordSealed: "raw-app-pass",
	}
	if err := repo.EnqueueATProtoPost(context.Background(), vid); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return uid, vid
}

func TestDrainPostsSuccess(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	uid, vid := seedPublishable(t, repo)
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://videos.example"))

	n, err := s.DrainPosts(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	if n != 1 {
		t.Fatalf("posted = %d, want 1", n)
	}
	post := repo.postForVideo(vid)
	if post.State != "posted" {
		t.Errorf("state = %q, want posted", post.State)
	}
	if post.PostUri != pds.recordURI {
		t.Errorf("post_uri = %q, want %q", post.PostUri, pds.recordURI)
	}
	if len(pds.createCalls) != 1 || len(pds.recordCalls) != 1 {
		t.Fatalf("expected one createSession + one createRecord, got %d/%d", len(pds.createCalls), len(pds.recordCalls))
	}
	// The record links to the public watch URL for this video.
	rec := pds.recordCalls[0]
	embed := rec["embed"].(map[string]any)
	external := embed["external"].(map[string]any)
	if external["uri"] != "https://videos.example/videos/watch/"+vid.String() {
		t.Errorf("watch url = %v", external["uri"])
	}
	// last_posted_at is bumped for the owner.
	found := false
	for _, u := range repo.touched {
		if u == uid {
			found = true
		}
	}
	if !found {
		t.Errorf("owner last_posted_at was not touched")
	}
}

func TestDrainPostsWithThumbnail(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	_, vid := seedPublishable(t, repo)
	thumbs := fakeThumbs{data: []byte("jpegbytes"), mime: "image/jpeg", ok: true}
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"), WithThumbnails(thumbs))

	if _, err := s.DrainPosts(context.Background(), 10); err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	if pds.uploadCalls != 1 {
		t.Errorf("expected the thumbnail to be uploaded once, got %d", pds.uploadCalls)
	}
	rec := pds.recordCalls[0]
	external := rec["embed"].(map[string]any)["external"].(map[string]any)
	if _, ok := external["thumb"]; !ok {
		t.Errorf("embed is missing the uploaded thumbnail blob")
	}
	_ = vid
}

func TestDrainPostsOversizeThumbnailSkipped(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	seedPublishable(t, repo)
	big := make([]byte, maxThumbBytes+1)
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"),
		WithThumbnails(fakeThumbs{data: big, mime: "image/jpeg", ok: true}))

	if _, err := s.DrainPosts(context.Background(), 10); err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	if pds.uploadCalls != 0 {
		t.Errorf("an oversize (>1 MiB) thumbnail must not be uploaded")
	}
	external := pds.recordCalls[0]["embed"].(map[string]any)["external"].(map[string]any)
	if _, ok := external["thumb"]; ok {
		t.Errorf("oversize thumbnail should not be embedded")
	}
}

func TestDrainPostsRateLimitReschedules(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	pds.createErr = &XRPCError{Status: 429, Op: nsidCreateSession, ErrName: "RateLimitExceeded"}
	_, vid := seedPublishable(t, repo)
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"))

	n, err := s.DrainPosts(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	if n != 0 {
		t.Fatalf("posted = %d, want 0 on rate limit", n)
	}
	post := repo.postForVideo(vid)
	if post.State != "pending" {
		t.Errorf("state = %q, want still pending (rescheduled)", post.State)
	}
	if post.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", post.Attempts)
	}
	// The stored error is safe: no password, no PDS host.
	if strings.Contains(post.Error, "raw-app-pass") || strings.Contains(post.Error, "bsky.social") {
		t.Errorf("stored error leaks a secret/host: %q", post.Error)
	}
}

func TestDrainPostsNonPublicCancelsImmediately(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	_, vid := seedPublishable(t, repo)
	// The video went private after enqueue.
	v := repo.videos[vid]
	v.Privacy = "private"
	repo.videos[vid] = v
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"))

	if _, err := s.DrainPosts(context.Background(), 10); err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	post := repo.postForVideo(vid)
	if post.State != "failed" {
		t.Errorf("state = %q, want failed (permanent cancel)", post.State)
	}
	if post.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retries for a permanent failure)", post.Attempts)
	}
	if len(pds.createCalls) != 0 {
		t.Errorf("PDS must not be contacted for a non-public video")
	}
}

func TestDrainPostsDeadLettersAfterMaxAttempts(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	pds.createErr = &XRPCError{Status: 500, Op: nsidCreateSession}
	_, vid := seedPublishable(t, repo)
	// Simulate a post that has already failed maxPostAttempts-1 times.
	repo.postForVideo(vid).Attempts = maxPostAttempts - 1
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"))

	if _, err := s.DrainPosts(context.Background(), 10); err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	post := repo.postForVideo(vid)
	if post.State != "failed" {
		t.Errorf("state = %q, want failed (dead-lettered)", post.State)
	}
	if post.Attempts != maxPostAttempts {
		t.Errorf("attempts = %d, want %d", post.Attempts, maxPostAttempts)
	}
}

func TestDrainPostsDisabledNoop(t *testing.T) {
	repo := newFakeRepo()
	seedPublishable(t, repo)
	s := NewService(repo, WithPDSClient(newFakePDS()), WithBaseURL("https://x")) // disabled
	n, err := s.DrainPosts(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("disabled DrainPosts = (%d, %v), want (0, nil)", n, err)
	}
}

func TestPostBackoffGrowsAndCaps(t *testing.T) {
	if postBackoff(1) != postBaseBackoff {
		t.Errorf("attempt 1 backoff = %v, want %v", postBackoff(1), postBaseBackoff)
	}
	if postBackoff(2) != 2*postBaseBackoff {
		t.Errorf("attempt 2 backoff = %v, want %v", postBackoff(2), 2*postBaseBackoff)
	}
	if got := postBackoff(100); got != maxPostBackoff {
		t.Errorf("attempt 100 backoff = %v, want capped %v", got, maxPostBackoff)
	}
}

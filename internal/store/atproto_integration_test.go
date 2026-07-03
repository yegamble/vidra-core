//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestATProtoQueriesPersist exercises the ATProto account + post-queue queries
// (migration 0066) against real PostgreSQL: the account upsert/read/touch/delete,
// the post-queue dedupe (video_id UNIQUE), the claim/reschedule/done state
// machine, the video-context join, and the ON DELETE CASCADE from users/videos.
func TestATProtoQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	// Seed a published PUBLIC video for the enqueue/context queries.
	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Launch Day', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	// --- account upsert + read ---
	acct, err := q.UpsertATProtoAccount(ctx, sqlcgen.UpsertATProtoAccountParams{
		UserID:            userID,
		Handle:            "alice.bsky.social",
		Did:               "did:plc:test",
		PdsUrl:            "https://bsky.social",
		AppPasswordSealed: "enc:sealedvalue",
		AutoPost:          true,
	})
	if err != nil {
		t.Fatalf("UpsertATProtoAccount: %v", err)
	}
	if acct.Handle != "alice.bsky.social" || !acct.AutoPost {
		t.Errorf("account = %+v", acct)
	}
	if acct.LastPostedAt.Valid {
		t.Errorf("last_posted_at should be NULL initially")
	}

	got, err := q.GetATProtoAccount(ctx, userID)
	if err != nil {
		t.Fatalf("GetATProtoAccount: %v", err)
	}
	if got.Did != "did:plc:test" {
		t.Errorf("did = %q", got.Did)
	}

	// Re-link (upsert) preserves created_at, overwrites the mutable fields.
	relinked, err := q.UpsertATProtoAccount(ctx, sqlcgen.UpsertATProtoAccountParams{
		UserID:            userID,
		Handle:            "alice2.bsky.social",
		Did:               "did:plc:test2",
		PdsUrl:            "https://pds.example",
		AppPasswordSealed: "enc:newsealed",
		AutoPost:          false,
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if relinked.Handle != "alice2.bsky.social" || relinked.AutoPost {
		t.Errorf("re-link did not overwrite mutable fields: %+v", relinked)
	}
	if !relinked.CreatedAt.Equal(acct.CreatedAt) {
		t.Errorf("re-link changed created_at (%v -> %v)", acct.CreatedAt, relinked.CreatedAt)
	}

	// --- post queue: enqueue + dedupe ---
	if err := q.EnqueueATProtoPost(ctx, videoID); err != nil {
		t.Fatalf("EnqueueATProtoPost: %v", err)
	}
	if err := q.EnqueueATProtoPost(ctx, videoID); err != nil {
		t.Fatalf("EnqueueATProtoPost (dedupe): %v", err)
	}
	post, err := q.GetATProtoPostByVideo(ctx, videoID)
	if err != nil {
		t.Fatalf("GetATProtoPostByVideo: %v", err)
	}
	if post.State != "pending" || post.Attempts != 0 {
		t.Errorf("post = %+v, want pending/0", post)
	}

	// --- claim ---
	due, err := q.ClaimDueATProtoPosts(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDueATProtoPosts: %v", err)
	}
	if len(due) != 1 || due[0].VideoID != videoID {
		t.Fatalf("claimed = %+v, want the single due post", due)
	}

	// --- reschedule (transient failure) ---
	if err := q.RescheduleATProtoPost(ctx, sqlcgen.RescheduleATProtoPostParams{
		ID:            post.ID,
		NextAttemptAt: time.Now().Add(time.Hour),
		Error:         "atproto: XRPC com.atproto.server.createSession failed: status 429",
	}); err != nil {
		t.Fatalf("RescheduleATProtoPost: %v", err)
	}
	// Now it is not due (next_attempt_at in the future).
	due, err = q.ClaimDueATProtoPosts(ctx, 10)
	if err != nil {
		t.Fatalf("claim after reschedule: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("post is scheduled in the future but was claimed: %+v", due)
	}
	post, _ = q.GetATProtoPostByVideo(ctx, videoID)
	if post.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after reschedule", post.Attempts)
	}

	// --- mark done ---
	if err := q.MarkATProtoPostDone(ctx, sqlcgen.MarkATProtoPostDoneParams{
		ID:      post.ID,
		PostUri: "at://did:plc:test/app.bsky.feed.post/abc",
	}); err != nil {
		t.Fatalf("MarkATProtoPostDone: %v", err)
	}
	post, _ = q.GetATProtoPostByVideo(ctx, videoID)
	if post.State != "posted" || post.PostUri == "" {
		t.Errorf("post after done = %+v", post)
	}

	// --- video-context join (owner/title/privacy) ---
	vinfo, err := q.GetATProtoPostVideo(ctx, videoID)
	if err != nil {
		t.Fatalf("GetATProtoPostVideo: %v", err)
	}
	if vinfo.OwnerID != userID || vinfo.Title != "Launch Day" || vinfo.Privacy != "public" {
		t.Errorf("video context = %+v", vinfo)
	}

	// --- touch last_posted_at ---
	if err := q.TouchATProtoAccountPosted(ctx, userID); err != nil {
		t.Fatalf("TouchATProtoAccountPosted: %v", err)
	}
	got, _ = q.GetATProtoAccount(ctx, userID)
	if !got.LastPostedAt.Valid {
		t.Errorf("last_posted_at not set after touch")
	}

	// --- delete account ---
	if err := q.DeleteATProtoAccount(ctx, userID); err != nil {
		t.Fatalf("DeleteATProtoAccount: %v", err)
	}
	if _, err := q.GetATProtoAccount(ctx, userID); err == nil {
		t.Errorf("account still present after delete")
	}

	// Deleting the video cascades the queued post away.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM videos WHERE id = $1`, videoID); err != nil {
		t.Fatalf("delete video: %v", err)
	}
	if _, err := q.GetATProtoPostByVideo(ctx, videoID); err == nil {
		t.Errorf("post row survived video deletion (missing ON DELETE CASCADE)")
	}
}

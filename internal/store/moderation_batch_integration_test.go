//go:build integration

// Integration tests for the moderation batch (product-decisions §11/§12/§13):
// the upload quarantine pipeline, watched-word video tagging, and the
// block-hides-content filters. Same harness contract as integration_test.go
// (live PostgreSQL via DATABASE_URL, migrations applied).
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestQuarantinePipelinePersists proves the §11 storage pieces against a real
// PostgreSQL: the widened state CHECK accepts 'quarantined', the gate query
// reflects role + users.bypass_quarantine (including the AdminUpdateUser
// COALESCE path), and the moderation queue lists exactly the quarantined
// videos with their channel + owner.
func TestQuarantinePipelinePersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID := seedOwnerChannel(ctx, t, st, "quar")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "held probe")

	// The gate: a plain role=user owner without bypass requires quarantine.
	requires, err := q.UploadRequiresQuarantine(ctx, videoID)
	if err != nil || !requires {
		t.Fatalf("UploadRequiresQuarantine(plain user) = (%v, %v), want true", requires, err)
	}

	// Granting bypass_quarantine through the admin edit flips the gate off.
	bypass := true
	if u, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, BypassQuarantine: &bypass}); err != nil || !u.BypassQuarantine {
		t.Fatalf("AdminUpdateUser bypass=true = (%+v, %v)", u, err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); requires {
		t.Fatalf("gate still on for a bypassed account")
	}
	// An unrelated admin edit leaves the flag alone (COALESCE path).
	role := "user"
	if u, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, Role: &role}); err != nil || !u.BypassQuarantine {
		t.Fatalf("unrelated edit flipped bypass_quarantine: (%+v, %v)", u, err)
	}
	// Revoking re-arms the gate; a privileged role disarms it regardless.
	revoked := false
	if _, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, BypassQuarantine: &revoked}); err != nil {
		t.Fatalf("revoke bypass: %v", err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); !requires {
		t.Fatalf("gate off after revoking bypass")
	}
	moderator := "moderator"
	if _, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, Role: &moderator}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); requires {
		t.Fatalf("gate on for a moderator")
	}

	// The widened CHECK accepts 'quarantined'; the queue lists it with context.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "quarantined"}); err != nil {
		t.Fatalf("SetVideoState quarantined: %v", err)
	}
	queue, err := q.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 500})
	if err != nil {
		t.Fatalf("ListQuarantinedVideos: %v", err)
	}
	found := false
	for _, r := range queue {
		if r.ID == videoID {
			found = true
			if r.State != "quarantined" || r.OwnerUsername == "" || r.ChannelHandle == "" {
				t.Fatalf("queue row missing context: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("quarantined video missing from the queue")
	}

	// Quarantined videos are absent from the public discovery queries.
	feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{Sort: "recent", ResultLimit: 500})
	if err != nil {
		t.Fatalf("ListPublicVideosSorted: %v", err)
	}
	for _, r := range feed {
		if r.ID == videoID {
			t.Fatalf("quarantined video leaked into the public feed")
		}
	}

	// Releasing it (the approve transition's write) empties the queue again.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "published"}); err != nil {
		t.Fatalf("SetVideoState published: %v", err)
	}
	queue, _ = q.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 500})
	for _, r := range queue {
		if r.ID == videoID {
			t.Fatalf("published video still in the quarantine queue")
		}
	}
}

// TestWatchedWordVideoMatchesPersist proves the §12 storage pieces against a
// real PostgreSQL: the video-target insert is idempotent via the partial
// unique index, the exactly-one-target CHECK rejects bad rows, the review
// query types both kinds with their context, and deleting the video cascades
// its match rows away.
func TestWatchedWordVideoMatchesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID := seedOwnerChannel(ctx, t, st, "wwv")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "flagged probe")

	// A unique term isolates the run on a shared dev database.
	term := "wwv-" + uuid.NewString()[:8]
	word, err := q.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word: term, CreatedBy: pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateWatchedWord: %v", err)
	}
	t.Cleanup(func() { _, _ = q.DeleteWatchedWord(context.Background(), word.ID) })

	// Idempotent video-target insert (partial unique index).
	for i := 0; i < 2; i++ {
		if err := q.RecordWatchedWordVideoMatch(ctx, sqlcgen.RecordWatchedWordVideoMatchParams{
			WatchedWordID: word.ID, VideoID: pgtype.UUID{Bytes: videoID, Valid: true},
		}); err != nil {
			t.Fatalf("RecordWatchedWordVideoMatch #%d: %v", i+1, err)
		}
	}

	// The CHECK rejects a row with both targets or neither.
	var commentID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, 'hi') RETURNING id`,
		videoID, userID,
	).Scan(&commentID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO watched_word_matches (watched_word_id, comment_id, video_id) VALUES ($1, $2, $3)`,
		word.ID, commentID, videoID,
	); err == nil {
		t.Fatalf("both-targets row accepted, want CHECK violation")
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO watched_word_matches (watched_word_id) VALUES ($1)`, word.ID,
	); err == nil {
		t.Fatalf("no-target row accepted, want CHECK violation")
	}

	// A comment match joins the same queue; both rows are typed with context.
	if err := q.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
		WatchedWordID: word.ID, CommentID: pgtype.UUID{Bytes: commentID, Valid: true},
	}); err != nil {
		t.Fatalf("RecordWatchedWordMatch: %v", err)
	}
	rows, err := q.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{ResultLimit: 500})
	if err != nil {
		t.Fatalf("ListWatchedWordMatches: %v", err)
	}
	var sawVideo, sawComment bool
	for _, r := range rows {
		if r.Word != term {
			continue
		}
		if r.CommentID.Valid {
			sawComment = true
			if uuid.UUID(r.CommentID.Bytes) != commentID || r.CommentBody == nil || *r.CommentBody != "hi" {
				t.Errorf("comment row context = %+v", r)
			}
			if r.VideoID != videoID {
				t.Errorf("comment row video_id = %s, want the comment's video %s", r.VideoID, videoID)
			}
		} else {
			sawVideo = true
			if r.VideoID != videoID || r.VideoTitle == nil || *r.VideoTitle != "flagged probe" {
				t.Errorf("video row context = %+v", r)
			}
			if r.AuthorUsername == "" {
				t.Errorf("video row missing owner username: %+v", r)
			}
		}
	}
	if !sawVideo || !sawComment {
		t.Fatalf("queue rows for term = video:%v comment:%v, want both", sawVideo, sawComment)
	}

	// Deleting the video cascades both match rows away (the comment goes too).
	if _, err := st.Pool.Exec(ctx, `DELETE FROM videos WHERE id = $1`, videoID); err != nil {
		t.Fatalf("delete video: %v", err)
	}
	rows, _ = q.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{ResultLimit: 500})
	for _, r := range rows {
		if r.Word == term {
			t.Fatalf("match row survived the video delete: %+v", r)
		}
	}
}

// TestBlockHidesContentFilters proves the §13 SQL against a real PostgreSQL:
// a user_blocks row (blocker = viewer) removes the blocked owner's videos from
// the feed/search/subscriptions queries and their comments from the comment
// list — per-viewer (an anonymous/other viewer is unaffected) — and deleting
// the block restores everything.
func TestBlockHidesContentFilters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	creatorID, channelID := seedOwnerChannel(ctx, t, st, "blk-creator")
	viewerID, _ := seedOwnerChannel(ctx, t, st, "blk-viewer")
	title := "block-probe-" + uuid.NewString()[:8]
	videoID := seedPublishedVideo(ctx, t, st, channelID, title)
	var commentID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, 'from creator') RETURNING id`,
		videoID, creatorID,
	).Scan(&commentID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO channel_follows (channel_id, follower_id) VALUES ($1, $2)`,
		channelID, viewerID,
	); err != nil {
		t.Fatalf("seed follow: %v", err)
	}

	viewer := pgtype.UUID{Bytes: viewerID, Valid: true}
	inFeed := func(as pgtype.UUID) bool {
		rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
			Sort: "recent", ViewerID: as, ResultLimit: 500,
		})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		for _, r := range rows {
			if r.ID == videoID {
				return true
			}
		}
		return false
	}
	inSearch := func(as pgtype.UUID) bool {
		rows, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
			Query: title, ViewerID: as, ResultLimit: 10,
		})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		for _, r := range rows {
			if r.ID == videoID {
				return true
			}
		}
		return false
	}
	inSubs := func() bool {
		rows, err := q.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{
			FollowerID: viewerID, ResultLimit: 500,
		})
		if err != nil {
			t.Fatalf("ListSubscriptionVideos: %v", err)
		}
		for _, r := range rows {
			if r.ID == videoID {
				return true
			}
		}
		return false
	}
	inComments := func(as pgtype.UUID) bool {
		rows, err := q.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
			VideoID: videoID, ViewerID: as, ResultLimit: 100,
		})
		if err != nil {
			t.Fatalf("ListCommentsByVideo: %v", err)
		}
		for _, r := range rows {
			if r.ID == commentID {
				return true
			}
		}
		return false
	}

	if !inFeed(viewer) || !inSearch(viewer) || !inSubs() || !inComments(viewer) {
		t.Fatalf("baseline: content missing before the block (feed=%v search=%v subs=%v comments=%v)",
			inFeed(viewer), inSearch(viewer), inSubs(), inComments(viewer))
	}

	// The viewer blocks the creator (the real BlockUser upsert).
	if _, err := q.BlockUser(ctx, sqlcgen.BlockUserParams{BlockerID: viewerID, BlockedID: creatorID}); err != nil {
		t.Fatalf("BlockUser: %v", err)
	}
	if inFeed(viewer) || inSearch(viewer) || inSubs() || inComments(viewer) {
		t.Fatalf("blocked owner still visible (feed=%v search=%v subs=%v comments=%v)",
			inFeed(viewer), inSearch(viewer), inSubs(), inComments(viewer))
	}
	// Per-viewer: an anonymous viewer (NULL) still sees everything.
	if !inFeed(pgtype.UUID{}) || !inSearch(pgtype.UUID{}) || !inComments(pgtype.UUID{}) {
		t.Fatalf("anon viewer affected by someone else's block")
	}

	// Unblocking restores visibility.
	if _, err := q.UnblockUser(ctx, sqlcgen.UnblockUserParams{BlockerID: viewerID, BlockedID: creatorID}); err != nil {
		t.Fatalf("UnblockUser: %v", err)
	}
	if !inFeed(viewer) || !inSearch(viewer) || !inSubs() || !inComments(viewer) {
		t.Fatalf("content not restored after unblock (feed=%v search=%v subs=%v comments=%v)",
			inFeed(viewer), inSearch(viewer), inSubs(), inComments(viewer))
	}
}

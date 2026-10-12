//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestHeldCommentVisibilityOnRealPG proves the 0161 predicate in the SQL the
// handlers trust: a held comment is in the rows AND the total only for its
// author and for a caller the HTTP layer marked as a reviewer (include_held);
// the comment notifications never resolve a recipient for it; approval is a
// one-way transition with exactly one winner, after which every reader sees it.
func TestHeldCommentVisibilityOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, author, stranger := mkUser("held-owner"), mkUser("held-author"), mkUser("held-stranger")
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Held') RETURNING id`,
		owner, "held-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// The widened CHECK accepts the new policy...
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state, comments_policy)
		 VALUES ($1, 'Held thread', 'public', 'published', 'requires_approval') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed requires_approval video: %v", err)
	}
	// ...and still refuses anything else.
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET comments_policy = 'moderated' WHERE id = $1`, videoID); err == nil {
		t.Fatal("comments_policy CHECK accepted an unknown policy")
	}

	create := func(user uuid.UUID, body string, parent pgtype.UUID, held bool) sqlcgen.Comment {
		t.Helper()
		c, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
			VideoID: videoID, UserID: pgtype.UUID{Bytes: user, Valid: true}, Body: body,
			ParentID: parent, HeldForReview: held,
		})
		if err != nil {
			t.Fatalf("create %q: %v", body, err)
		}
		return c
	}
	published := create(owner, "owner says hi", pgtype.UUID{}, false)
	held := create(author, "please approve", pgtype.UUID{}, true)
	heldReply := create(author, "a held reply", pgtype.UUID{Bytes: published.ID, Valid: true}, true)
	if !held.HeldForReview || published.HeldForReview {
		t.Fatalf("held_for_review round-trip: held=%v published=%v", held.HeldForReview, published.HeldForReview)
	}

	type viewer struct {
		name        string
		id          pgtype.UUID
		includeHeld bool
		want        int
	}
	viewers := []viewer{
		{"anonymous", pgtype.UUID{}, false, 1},
		{"stranger", pgtype.UUID{Bytes: stranger, Valid: true}, false, 1},
		{"author", pgtype.UUID{Bytes: author, Valid: true}, false, 3},
		{"reviewer", pgtype.UUID{Bytes: owner, Valid: true}, true, 3},
		{"anonymous+include_held", pgtype.UUID{}, true, 3},
	}
	check := func(stage string, stillHeld bool, vs []viewer) {
		t.Helper()
		for _, v := range vs {
			rows, err := q.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
				VideoID: videoID, ViewerID: v.id, IncludeHeld: v.includeHeld, ResultLimit: 50,
			})
			if err != nil {
				t.Fatalf("%s/%s list: %v", stage, v.name, err)
			}
			total, err := q.CountCommentsByVideo(ctx, sqlcgen.CountCommentsByVideoParams{
				VideoID: videoID, ViewerID: v.id, IncludeHeld: v.includeHeld,
			})
			if err != nil {
				t.Fatalf("%s/%s count: %v", stage, v.name, err)
			}
			if len(rows) != v.want || total != int64(v.want) {
				t.Errorf("%s/%s: rows=%d total=%d, want %d/%d", stage, v.name, len(rows), total, v.want, v.want)
			}
			for _, r := range rows {
				if r.ID == held.ID && r.HeldForReview != stillHeld {
					t.Errorf("%s/%s: held_for_review projected %v, want %v", stage, v.name, r.HeldForReview, stillHeld)
				}
			}
		}
	}
	check("held", true, viewers)

	// Neither comment notification resolves a recipient for a held comment.
	if _, err := q.CommentVideoOwnerRecipient(ctx, held.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("owner recipient for a held comment: err=%v, want no row", err)
	}
	if _, err := q.CommentReplyRecipient(ctx, heldReply.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("reply recipient for a held reply: err=%v, want no row", err)
	}

	// A stale version (the comment changed since it was reviewed) approves nothing.
	if _, err := q.ApproveComment(ctx, sqlcgen.ApproveCommentParams{ID: held.ID, ExpectedUpdatedAt: held.UpdatedAt.Add(-time.Second)}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("stale-version approve err = %v, want no row", err)
	}
	// Mentions in a held comment wait for approval (0161).
	mentionStranger := func() int64 {
		t.Helper()
		n, err := q.NotifyCommentMentions(ctx, sqlcgen.NotifyCommentMentionsParams{
			CommentID: held.ID, Usernames: []string{"held-stranger-" + suffix},
		})
		if err != nil {
			t.Fatalf("mention fan-out: %v", err)
		}
		return n
	}
	if n := mentionStranger(); n != 0 {
		t.Errorf("mention fan-out for a held comment notified %d, want 0", n)
	}
	// Approval: exactly one winner, then the comment is everyone's.
	got, err := approveCurrent(ctx, q, held.ID)
	if err != nil || got.HeldForReview {
		t.Fatalf("first approve: held=%v err=%v, want released", got.HeldForReview, err)
	}
	if !got.UpdatedAt.Equal(held.UpdatedAt) {
		t.Errorf("approval bumped updated_at (%v -> %v); it is not an edit", held.UpdatedAt, got.UpdatedAt)
	}
	if _, err := approveCurrent(ctx, q, held.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("second approve err = %v, want no row (nothing left to release)", err)
	}
	if _, err := approveCurrent(ctx, q, published.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("approving a never-held comment err = %v, want no row", err)
	}
	if row, err := q.CommentVideoOwnerRecipient(ctx, held.ID); err != nil || row.RecipientID != owner {
		t.Errorf("owner recipient after approval = %v, %v; want the owner", row.RecipientID, err)
	}
	if n := mentionStranger(); n != 1 {
		t.Errorf("mention fan-out after approval notified %d, want 1", n)
	}
	check("approved", false, []viewer{
		{"anonymous", pgtype.UUID{}, false, 2},
		{"stranger", pgtype.UUID{Bytes: stranger, Valid: true}, false, 2},
		{"author", pgtype.UUID{Bytes: author, Valid: true}, false, 3},
	})

	// An inbound federated Note is held on insert, and the upsert arm never
	// touches the flag: a re-delivery can neither release a held comment nor
	// re-hold an approved one.
	actor, name, object := "https://remote.example/accounts/r-"+suffix, "r", "https://remote.example/notes/"+suffix
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Person', 'k')`, actor); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor)
	})
	remote := func(held bool) sqlcgen.Comment {
		t.Helper()
		c, err := q.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
			VideoID: videoID, Body: "remote", RemoteActorUrl: &actor, RemoteAuthorName: &name,
			RemoteObjectUrl: &object, HeldForReview: held,
		})
		if err != nil {
			t.Fatalf("remote upsert: %v", err)
		}
		return c
	}
	first := remote(true)
	if !first.HeldForReview || remote(false).HeldForReview != true {
		t.Fatal("a re-delivered Note released a held remote comment")
	}
	if _, err := approveCurrent(ctx, q, first.ID); err != nil {
		t.Fatalf("approve remote: %v", err)
	}
	if remote(true).HeldForReview {
		t.Error("a re-delivered Note re-held an approved remote comment")
	}
}

// TestManagedCommentQueueOnRealPG proves the two 0161 statements PR 3 adds: the
// review queue lists comments on every video the caller owns OR edits (and
// nobody else's), with the held filter; and the comment_held fan-out reaches
// exactly the owner and editors — never the commenter, a muting/blocking
// manager, or anyone twice.
func TestManagedCommentQueueOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, editor, muter, commenter, stranger := mkUser("q-owner"), mkUser("q-editor"), mkUser("q-muter"), mkUser("q-commenter"), mkUser("q-stranger")
	blocker := mkUser("q-blocker") // an editor who BLOCKED the commenter
	var channelID, videoID, otherVideo uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Queue') RETURNING id`,
		owner, "queue-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, m := range []uuid.UUID{editor, muter, blocker} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO channel_members (channel_id, user_id) VALUES ($1, $2)`, channelID, m); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, muter, commenter); err != nil {
		t.Fatalf("seed mute: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, blocker, commenter); err != nil {
		t.Fatalf("seed block: %v", err)
	}
	for _, dst := range []*uuid.UUID{&videoID, &otherVideo} {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, comments_policy)
			 VALUES ($1, 'Queued', 'public', 'published', 'requires_approval') RETURNING id`, channelID).Scan(dst); err != nil {
			t.Fatalf("seed video: %v", err)
		}
	}
	mk := func(video, user uuid.UUID, held bool) uuid.UUID {
		t.Helper()
		c, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
			VideoID: video, UserID: pgtype.UUID{Bytes: user, Valid: true}, Body: "q", HeldForReview: held,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return c.ID
	}
	held := mk(videoID, commenter, true)
	mk(otherVideo, commenter, true)
	mk(videoID, owner, false)

	yes, no := true, false
	for _, tc := range []struct {
		name string
		user uuid.UUID
		held *bool
		want int64
	}{
		{"owner held", owner, &yes, 2}, {"editor held", editor, &yes, 2}, {"owner published", owner, &no, 1},
		{"owner all", owner, nil, 3}, {"stranger", stranger, nil, 0}, {"commenter", commenter, nil, 0},
	} {
		rows, err := q.ListManagedVideoComments(ctx, sqlcgen.ListManagedVideoCommentsParams{UserID: tc.user, Held: tc.held, ResultLimit: 50})
		if err != nil {
			t.Fatalf("%s list: %v", tc.name, err)
		}
		total, err := q.CountManagedVideoComments(ctx, sqlcgen.CountManagedVideoCommentsParams{UserID: tc.user, Held: tc.held})
		if err != nil {
			t.Fatalf("%s count: %v", tc.name, err)
		}
		if int64(len(rows)) != tc.want || total != tc.want {
			t.Errorf("%s: rows=%d total=%d, want %d", tc.name, len(rows), total, tc.want)
		}
		for _, r := range rows {
			if r.VideoTitle != "Queued" {
				t.Errorf("%s: video_title = %q", tc.name, r.VideoTitle)
			}
		}
	}

	notified := func() map[uuid.UUID]int {
		t.Helper()
		out := map[uuid.UUID]int{}
		r, err := st.Pool.Query(ctx, `SELECT user_id FROM notifications WHERE type = 'comment_held' AND comment_id = $1`, held)
		if err != nil {
			t.Fatalf("read notifications: %v", err)
		}
		defer r.Close()
		for r.Next() {
			var id uuid.UUID
			_ = r.Scan(&id)
			out[id]++
		}
		return out
	}
	for i := 0; i < 2; i++ { // the second fan-out must be a no-op
		n, err := q.NotifyManagersOfHeldComment(ctx, held)
		if err != nil {
			t.Fatalf("fan-out #%d: %v", i+1, err)
		}
		if want := int64(2 * (1 - i)); n != want {
			t.Errorf("fan-out #%d notified %d, want %d", i+1, n, want)
		}
	}
	got := notified()
	if len(got) != 2 || got[owner] != 1 || got[editor] != 1 {
		t.Errorf("comment_held recipients = %v, want owner and editor once each (not the muting or blocking editor, commenter or stranger)", got)
	}
	// Once read, a re-fire (a re-held edit) re-surfaces each notice unread —
	// still one row per reviewer — and an immediate repeat changes nothing.
	if _, err := st.Pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE comment_id = $1`, held); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	for i, want := range []int64{2, 0} {
		if n, err := q.NotifyManagersOfHeldComment(ctx, held); err != nil || n != want {
			t.Errorf("re-fire #%d after read = %d, %v; want %d", i+1, n, err, want)
		}
	}
	var unread int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE comment_id = $1 AND read_at IS NULL`, held).Scan(&unread); err != nil || unread != 2 {
		t.Errorf("unread comment_held rows after re-surface = %d, %v; want 2", unread, err)
	}
	// An approved comment no longer pings anyone.
	if _, err := approveCurrent(ctx, q, held); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM notifications WHERE comment_id = $1`, held); err != nil {
		t.Fatalf("reset notifications: %v", err)
	}
	if n, err := q.NotifyManagersOfHeldComment(ctx, held); err != nil || n != 0 {
		t.Errorf("fan-out for an approved comment = %d, %v; want 0", n, err)
	}
}

// TestHeldCommentReholdOnRealPG proves the re-hold rules (0161) in SQL: a
// body CHANGE that must be reviewed (UpdateComment hold, or a re-delivered
// remote Note on an approval video) sends an approved comment back to held; an
// unchanged body, or hold=false, never does; and NodeInfo's local comment count
// never includes a held comment.
func TestHeldCommentReholdOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	suffix := uuid.NewString()[:8]
	var owner, channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"rh-"+suffix, "rh-"+suffix+"@example.test").Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'R') RETURNING id`,
		owner, "rhc-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state, comments_policy)
		VALUES ($1, 'R', 'public', 'published', 'requires_approval') RETURNING id`, channelID).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	// NodeInfo's count is instance-global, so it is read inside one
	// REPEATABLE READ snapshot (rolled back): other packages' concurrent
	// inserts cannot move it between the reads.
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	tq := q.WithTx(tx)
	before, _ := tq.CountComments(ctx)
	counted, err := tq.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: videoID, UserID: pgtype.UUID{Bytes: owner, Valid: true}, Body: "counted", HeldForReview: true,
	})
	if err != nil {
		t.Fatalf("create in tx: %v", err)
	}
	if n, _ := tq.CountComments(ctx); n != before {
		t.Errorf("NodeInfo count with a held comment = %d, want %d (held excluded)", n, before)
	}
	if _, err := approveCurrent(ctx, tq, counted.ID); err != nil {
		t.Fatalf("approve in tx: %v", err)
	}
	if n, _ := tq.CountComments(ctx); n != before+1 {
		t.Errorf("NodeInfo count after approval = %d, want %d", n, before+1)
	}
	_ = tx.Rollback(ctx)

	c, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: videoID, UserID: pgtype.UUID{Bytes: owner, Valid: true}, Body: "v1", HeldForReview: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := approveCurrent(ctx, q, c.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	edit := func(body string, hold bool) bool {
		t.Helper()
		u, err := q.UpdateComment(ctx, sqlcgen.UpdateCommentParams{ID: c.ID, Body: body, Hold: hold})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		return u.HeldForReview
	}
	if edit("v1", true) {
		t.Error("an unchanged body re-held the comment")
	}
	if edit("v2", false) {
		t.Error("a reviewer's edit (hold=false) re-held the comment")
	}
	if !edit("v3", true) {
		t.Error("a changed body under hold stayed published")
	}
	if !edit("v4", false) {
		t.Error("an edit released a held comment")
	}

	actor, name, object := "https://remote.example/accounts/rh-"+suffix, "r", "https://remote.example/notes/rh-"+suffix
	if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Person', 'k')`, actor); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor)
	})
	upsert := func(body string) sqlcgen.Comment {
		t.Helper()
		r, err := q.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
			VideoID: videoID, Body: body, RemoteActorUrl: &actor, RemoteAuthorName: &name,
			RemoteObjectUrl: &object, HeldForReview: true,
		})
		if err != nil {
			t.Fatalf("remote upsert: %v", err)
		}
		return r
	}
	r := upsert("a")
	if _, err := approveCurrent(ctx, q, r.ID); err != nil {
		t.Fatalf("approve remote: %v", err)
	}
	if upsert("a").HeldForReview {
		t.Error("a same-body re-delivery re-held the approved remote comment")
	}
	if !upsert("b").HeldForReview {
		t.Error("a changed-body re-delivery published unreviewed")
	}

	// The upsert never moves a comment to another video, nor takes its hold
	// decision from one: a re-send of the same Note id naming a different
	// video (comments enabled, so hold=false) changes nothing.
	if _, err := approveCurrent(ctx, q, r.ID); err != nil {
		t.Fatalf("re-approve remote: %v", err)
	}
	var other uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state, comments_policy)
		VALUES ($1, 'Open', 'public', 'published', 'enabled') RETURNING id`, channelID).Scan(&other); err != nil {
		t.Fatalf("seed second video: %v", err)
	}
	_, err = q.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
		VideoID: other, Body: "elsewhere", RemoteActorUrl: &actor, RemoteAuthorName: &name,
		RemoteObjectUrl: &object, HeldForReview: false,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("cross-video re-send err = %v, want no row (refused)", err)
	}
	got, err := q.GetComment(ctx, r.ID)
	if err != nil || got.VideoID != videoID || got.Body != "b" || got.HeldForReview {
		t.Errorf("after a cross-video re-send: video=%s body=%q held=%v err=%v, want the approved row untouched",
			got.VideoID, got.Body, got.HeldForReview, err)
	}
}

// approveCurrent approves a comment at its current version (updated_at) — what
// a reviewer looking at it right now sends.
func approveCurrent(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID) (sqlcgen.Comment, error) {
	cur, err := q.GetComment(ctx, id)
	if err != nil {
		return cur, err
	}
	return q.ApproveComment(ctx, sqlcgen.ApproveCommentParams{ID: id, ExpectedUpdatedAt: cur.UpdatedAt})
}

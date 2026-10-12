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

	// Approval: exactly one winner, then the comment is everyone's.
	got, err := q.ApproveComment(ctx, held.ID)
	if err != nil || got.HeldForReview {
		t.Fatalf("first approve: held=%v err=%v, want released", got.HeldForReview, err)
	}
	if !got.UpdatedAt.Equal(held.UpdatedAt) {
		t.Errorf("approval bumped updated_at (%v -> %v); it is not an edit", held.UpdatedAt, got.UpdatedAt)
	}
	if _, err := q.ApproveComment(ctx, held.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("second approve err = %v, want no row (nothing left to release)", err)
	}
	if _, err := q.ApproveComment(ctx, published.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("approving a never-held comment err = %v, want no row", err)
	}
	if row, err := q.CommentVideoOwnerRecipient(ctx, held.ID); err != nil || row.RecipientID != owner {
		t.Errorf("owner recipient after approval = %v, %v; want the owner", row.RecipientID, err)
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
	if _, err := q.ApproveComment(ctx, first.ID); err != nil {
		t.Fatalf("approve remote: %v", err)
	}
	if remote(true).HeldForReview {
		t.Error("a re-delivered Note re-held an approved remote comment")
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
	if _, err := tq.ApproveComment(ctx, counted.ID); err != nil {
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
	if _, err := q.ApproveComment(ctx, c.ID); err != nil {
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
	if _, err := q.ApproveComment(ctx, r.ID); err != nil {
		t.Fatalf("approve remote: %v", err)
	}
	if upsert("a").HeldForReview {
		t.Error("a same-body re-delivery re-held the approved remote comment")
	}
	if !upsert("b").HeldForReview {
		t.Error("a changed-body re-delivery published unreviewed")
	}
}

//go:build integration

package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestCommentAutomaticTagsSQL (0177): ReplaceCommentAutomaticTags makes the set
// exact (drop + keep + add, idempotent), the admin list reports it, and the
// list and its count agree under ?auto_tag (case-insensitive).
func TestCommentAutomaticTagsSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	pool := st.Pool

	sfx := uuid.NewString()[:8]
	var userID, chanID, videoID, c1, c2 uuid.UUID
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`, "at"+sfx, "at"+sfx+"@example.test").Scan(&userID), "user")
	defer pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) //nolint:errcheck
	must(pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1,$2,'A') RETURNING id`, userID, "at-"+sfx).Scan(&chanID), "channel")
	must(pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1,'v','public','published') RETURNING id`, chanID).Scan(&videoID), "video")
	must(pool.QueryRow(ctx, `INSERT INTO comments (video_id, user_id, body) VALUES ($1,$2,'one') RETURNING id`, videoID, userID).Scan(&c1), "c1")
	must(pool.QueryRow(ctx, `INSERT INTO comments (video_id, user_id, body) VALUES ($1,$2,'two') RETURNING id`, videoID, userID).Scan(&c2), "c2")

	set := func(id uuid.UUID, tags ...string) {
		t.Helper()
		must(q.ReplaceCommentAutomaticTags(ctx, sqlcgen.ReplaceCommentAutomaticTagsParams{CommentID: id, Tags: tags}), "replace")
	}
	set(c1, "external-link", "Spam"+sfx)
	set(c1, "external-link", "Spam"+sfx) // idempotent
	set(c2, "external-link")
	set(c2) // an empty set clears

	tag := "SPAM" + sfx // case-insensitive
	rows, err := q.ListAdminComments(ctx, sqlcgen.ListAdminCommentsParams{AutoTag: &tag, ResultLimit: 50})
	must(err, "list")
	total, err := q.CountAdminComments(ctx, sqlcgen.CountAdminCommentsParams{AutoTag: &tag})
	must(err, "count")
	if len(rows) != 1 || total != 1 || rows[0].ID != c1 {
		t.Fatalf("filter %q: rows=%d total=%d", tag, len(rows), total)
	}
	if want := []string{"Spam" + sfx, "external-link"}; !slices.Equal(rows[0].AutomaticTags, want) {
		t.Errorf("tags = %v, want %v", rows[0].AutomaticTags, want)
	}
	set(c1, "external-link") // drop one, keep one
	if rows, _ = q.ListAdminComments(ctx, sqlcgen.ListAdminCommentsParams{AutoTag: &tag, ResultLimit: 50}); len(rows) != 0 {
		t.Errorf("dropped tag still matches %d rows", len(rows))
	}

	// §1 tombstoning erases the body, so its derived tags go with it.
	set(c1, "external-link")
	must(q.TombstoneUserComments(ctx, pgtype.UUID{Bytes: userID, Valid: true}), "tombstone")
	var n int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM comment_automatic_tags WHERE comment_id = ANY($1)`, []uuid.UUID{c1, c2}).Scan(&n), "count tags")
	if n != 0 {
		t.Errorf("tombstoned comments keep %d tags", n)
	}
}

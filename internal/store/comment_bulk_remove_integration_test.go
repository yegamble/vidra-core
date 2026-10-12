//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestDeleteCommentsByAuthorMatchesSingleDeleteOnRealPG holds the claim the
// bulk moderator removal rests on: one set-based DELETE leaves the SAME end
// state per comment as DeleteComment — replies cascade, reports cascade, the
// video's pin is cleared — and touches nobody else's comments. Remote-authored
// rows are reachable by actor URL. Proven in SQL because a fake that mirrors
// the cascade would only agree with itself.
func TestDeleteCommentsByAuthorMatchesSingleDeleteOnRealPG(t *testing.T) {
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
			`INSERT INTO users (username, email, password_hash, is_active) VALUES ($1, $2, 'x', false) RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	spammer, bystander := mkUser("bulk-spam"), mkUser("bulk-by")

	// The lookup is case-insensitive and finds a DEACTIVATED account.
	got, err := q.GetCommentAuthorIDByUsername(ctx, "BULK-SPAM-"+suffix)
	if err != nil || got != spammer {
		t.Fatalf("GetCommentAuthorIDByUsername = %s, %v; want %s", got, err, spammer)
	}

	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Bulk') RETURNING id`,
		bystander, "bulk-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'v', 'public', 'published') RETURNING id`,
		channelID).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	mkComment := func(user pgtype.UUID, actor *string, parent pgtype.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO comments (video_id, user_id, body, parent_id, remote_actor_url) VALUES ($1, $2, 'b', $3, $4) RETURNING id`,
			videoID, user, parent, actor).Scan(&id); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
		return id
	}
	sp := pgtype.UUID{Bytes: spammer, Valid: true}
	by := pgtype.UUID{Bytes: bystander, Valid: true}
	actor := "https://remote.example/users/spammer-" + suffix
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, public_key_pem) VALUES ($1, 'Person', 'spammer', 'remote.example', 'pem')`,
		actor); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor)
	})
	pinned := mkComment(sp, nil, pgtype.UUID{})
	other := mkComment(sp, nil, pgtype.UUID{})
	reply := mkComment(by, nil, pgtype.UUID{Bytes: pinned, Valid: true}) // bystander's reply UNDER the spam
	keep := mkComment(by, nil, pgtype.UUID{})
	remote := mkComment(pgtype.UUID{}, &actor, pgtype.UUID{})
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET pinned_comment_id = $1 WHERE id = $2`, pinned, videoID); err != nil {
		t.Fatalf("pin: %v", err)
	}
	var reporterReport uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO reports (reporter_id, target_type, comment_id, reason) VALUES ($1, 'comment', $2, 'spam') RETURNING id`,
		bystander, other).Scan(&reporterReport); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	exists := func(table string, id uuid.UUID) bool {
		var n int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("exists: %v", err)
		}
		return n > 0
	}

	rows, err := q.DeleteCommentsByAuthor(ctx, sqlcgen.DeleteCommentsByAuthorParams{UserID: sp})
	if err != nil {
		t.Fatalf("DeleteCommentsByAuthor: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("removed %d rows, want 2 (the spammer's, not the cascaded reply)", len(rows))
	}
	for _, r := range rows {
		if !r.UserID.Valid || r.UserID.Bytes != spammer || r.VideoID != videoID {
			t.Errorf("returned row %+v lost its author/video ids (federation needs them)", r)
		}
	}
	for name, id := range map[string]uuid.UUID{"pinned": pinned, "other": other, "reply under spam (CASCADE)": reply} {
		if exists("comments", id) {
			t.Errorf("%s comment survived", name)
		}
	}
	if exists("reports", reporterReport) {
		t.Error("report on a removed comment survived (CASCADE)")
	}
	var pin pgtype.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT pinned_comment_id FROM videos WHERE id = $1`, videoID).Scan(&pin); err != nil || pin.Valid {
		t.Errorf("pinned_comment_id = %+v, %v; want cleared (SET NULL)", pin, err)
	}
	if !exists("comments", keep) || !exists("comments", remote) {
		t.Fatal("a bystander's or the remote actor's comment was removed by a local-account bulk delete")
	}

	// Remote actor: reached by URL; a nil/empty arg set deletes nothing.
	if rows, err := q.DeleteCommentsByAuthor(ctx, sqlcgen.DeleteCommentsByAuthorParams{}); err != nil || len(rows) != 0 {
		t.Fatalf("empty params removed %d rows, %v; want 0", len(rows), err)
	}
	rows, err = q.DeleteCommentsByAuthor(ctx, sqlcgen.DeleteCommentsByAuthorParams{RemoteActorUrl: &actor})
	if err != nil || len(rows) != 1 || rows[0].ID != remote || rows[0].UserID.Valid {
		t.Fatalf("remote bulk delete = %+v, %v; want only the remote comment, no local author", rows, err)
	}
	if !exists("comments", keep) {
		t.Error("bystander's comment removed by the remote-actor delete")
	}
}

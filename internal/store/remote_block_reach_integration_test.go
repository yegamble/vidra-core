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

// Blocks are ACCOUNT-scoped and RETROACTIVE (A29 parity, migration 0142).
//
// The rehearsal named two failures and they are one failure: a viewer blocked
// `@name@domain`, the block stored the Person url while the videos were
// attributed to the Group, and nothing changed — and separately, the comments
// the blocked actor had ALREADY written stayed under the blocker's own video,
// "against the settings page's own promise that their replies stay off your
// videos".
//
// Both halves are read filters. Nothing is deleted to hide it, which is what
// makes "unblock restores exactly" a property of the design rather than a
// promise about a restore path.

type reachFixture struct {
	viewer, other uuid.UUID
	ownerID       uuid.UUID
	channelID     uuid.UUID
	videoID       uuid.UUID
	person, group string
	remoteVideoID uuid.UUID
	personComment uuid.UUID
	groupComment  uuid.UUID
}

// seedReachFixture builds the shape the finding needs: a remote ACCOUNT (Person)
// that owns a remote CHANNEL (Group), a remote video attributed to the Group,
// and two comments on a LOCAL video — one from each actor — so a block of the
// account can be shown to reach both.
func seedReachFixture(t *testing.T, st *Store) (reachFixture, func()) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var f reachFixture
	f.person = "https://peer.test/accounts/kaisa-" + suffix
	f.group = "https://peer.test/video-channels/films-" + suffix

	mustUser := func(prefix string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			prefix+"-"+suffix, prefix+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", prefix, err)
		}
		return id
	}
	f.viewer = mustUser("reach-viewer")
	f.other = mustUser("reach-other")
	f.ownerID = mustUser("reach-owner")

	cleanup := func() {
		bg := context.Background()
		_, _ = st.Pool.Exec(bg, `DELETE FROM blocked_remote_actors WHERE remote_actor_url IN ($1, $2)`, f.person, f.group)
		_, _ = st.Pool.Exec(bg, `DELETE FROM remote_actors WHERE actor_url IN ($1, $2)`, f.person, f.group)
		_, _ = st.Pool.Exec(bg, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{f.viewer, f.other, f.ownerID})
	}

	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Reach') RETURNING id`,
		f.ownerID, "reach-"+suffix).Scan(&f.channelID); err != nil {
		cleanup()
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, description, privacy, state)
		 VALUES ($1, 'Reach', '', 'public', 'published') RETURNING id`,
		f.channelID).Scan(&f.videoID); err != nil {
		cleanup()
		t.Fatalf("seed video: %v", err)
	}

	// The Group names its owner, which is the edge the reach travels.
	for _, a := range []struct{ url, typ, name, owner string }{
		{f.person, "Person", "kaisa", ""},
		{f.group, "Group", "films", f.person},
	} {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, inbox_url, public_key_pem, attributed_to)
			 VALUES ($1, $2, $3, 'peer.test', $1 || '/inbox', 'pem', $4)`,
			a.url, a.typ, a.name, a.owner); err != nil {
			cleanup()
			t.Fatalf("seed actor %s: %v", a.url, err)
		}
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO remote_videos (object_url, remote_actor_url, title, watch_url)
		 VALUES ($1, $2, 'Remote reach', $3) RETURNING id`,
		"https://peer.test/videos/"+suffix, f.group, "https://peer.test/w/"+suffix).Scan(&f.remoteVideoID); err != nil {
		cleanup()
		t.Fatalf("seed remote video: %v", err)
	}
	for i, actor := range []string{f.person, f.group} {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO comments (video_id, body, remote_actor_url, remote_author_name, remote_object_url)
			 VALUES ($1, $2, $3, 'kaisa', $4) RETURNING id`,
			f.videoID, "existing comment", actor,
			"https://peer.test/notes/"+suffix+"-"+string(rune('a'+i))).Scan(&id); err != nil {
			cleanup()
			t.Fatalf("seed comment from %s: %v", actor, err)
		}
		if i == 0 {
			f.personComment = id
		} else {
			f.groupComment = id
		}
	}
	return f, cleanup
}

// countCommentsFor is the viewer-scoped comment read the settings page's promise
// is about.
func countCommentsFor(t *testing.T, q *sqlcgen.Queries, videoID uuid.UUID, viewer uuid.UUID) int64 {
	t.Helper()
	var v pgtype.UUID
	if viewer != uuid.Nil {
		v = pgtype.UUID{Bytes: viewer, Valid: true}
	}
	n, err := q.CountCommentsByVideo(context.Background(), sqlcgen.CountCommentsByVideoParams{VideoID: videoID, ViewerID: v})
	if err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}

// remoteVideoVisibleTo reports whether the remote video is in that viewer's feed.
func remoteVideoVisibleTo(t *testing.T, q *sqlcgen.Queries, remoteVideoID, viewer uuid.UUID) bool {
	t.Helper()
	var v pgtype.UUID
	if viewer != uuid.Nil {
		v = pgtype.UUID{Bytes: viewer, Valid: true}
	}
	rows, err := q.ListPublicVideosSorted(context.Background(), sqlcgen.ListPublicVideosSortedParams{
		ViewerID: v, IncludeRemote: true, Sort: "recent", ResultLimit: 500,
	})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	for _, r := range rows {
		if r.Remote && r.ID == remoteVideoID {
			return true
		}
	}
	return false
}

// TestViewerBlockOfAnAccountHidesItsChannelsExistingContent is the whole of the
// viewer scope: one block, taken against the ACCOUNT, hides the account's own
// comment, the comment from the CHANNEL it owns, and the channel's video — for
// the blocker only — and the unblock puts every row back.
func TestViewerBlockOfAnAccountHidesItsChannelsExistingContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	f, cleanup := seedReachFixture(t, st)
	defer cleanup()

	// Before: the viewer sees both comments and the remote video.
	if got := countCommentsFor(t, q, f.videoID, f.viewer); got != 2 {
		t.Fatalf("before the block the viewer must see both comments, got %d", got)
	}
	if !remoteVideoVisibleTo(t, q, f.remoteVideoID, f.viewer) {
		t.Fatal("before the block the remote video must be in the viewer's feed")
	}

	// One block, against the ACCOUNT.
	if err := q.BlockRemoteActor(ctx, sqlcgen.BlockRemoteActorParams{
		BlockerID: f.viewer, RemoteActorUrl: f.person,
	}); err != nil {
		t.Fatalf("block: %v", err)
	}

	if got := countCommentsFor(t, q, f.videoID, f.viewer); got != 0 {
		t.Fatalf("the blocked account's EXISTING comments must leave the blocker's read, %d remain", got)
	}
	if remoteVideoVisibleTo(t, q, f.remoteVideoID, f.viewer) {
		t.Fatal("a block of the account must reach the channel it owns: the video is still in the feed")
	}

	// One-directional and per-viewer, exactly like every other viewer control.
	if got := countCommentsFor(t, q, f.videoID, f.other); got != 2 {
		t.Errorf("another viewer must be unaffected, sees %d comments", got)
	}
	if got := countCommentsFor(t, q, f.videoID, uuid.Nil); got != 2 {
		t.Errorf("an anonymous reader must be unaffected, sees %d comments", got)
	}
	if !remoteVideoVisibleTo(t, q, f.remoteVideoID, f.other) {
		t.Error("another viewer's feed must be unaffected")
	}

	// And the unblock restores exactly, because nothing was deleted to hide it.
	if _, err := q.UnblockRemoteActor(ctx, sqlcgen.UnblockRemoteActorParams{
		BlockerID: f.viewer, RemoteActorUrl: f.person,
	}); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if got := countCommentsFor(t, q, f.videoID, f.viewer); got != 2 {
		t.Fatalf("unblock must restore both comments, got %d", got)
	}
	if !remoteVideoVisibleTo(t, q, f.remoteVideoID, f.viewer) {
		t.Fatal("unblock must restore the remote video")
	}
}

// TestAdminBlockOfAnAccountHidesItForEveryone is the instance scope: the same
// reach, applied to every reader including anonymous ones, and equally
// reversible. It is what an admin uses INSTEAD of defederating a whole server to
// remove one person.
func TestAdminBlockOfAnAccountHidesItForEveryone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	f, cleanup := seedReachFixture(t, st)
	defer cleanup()

	if err := q.BlockRemoteActorInstanceWide(ctx, sqlcgen.BlockRemoteActorInstanceWideParams{
		RemoteActorUrl: f.person, Reason: "spam",
	}); err != nil {
		t.Fatalf("admin block: %v", err)
	}

	for name, viewer := range map[string]uuid.UUID{
		"the blocker's neighbour": f.other,
		"an anonymous reader":     uuid.Nil,
		"any signed-in viewer":    f.viewer,
	} {
		if got := countCommentsFor(t, q, f.videoID, viewer); got != 0 {
			t.Errorf("%s still sees %d comments from an instance-blocked account", name, got)
		}
		if remoteVideoVisibleTo(t, q, f.remoteVideoID, viewer) {
			t.Errorf("%s still sees the instance-blocked account's channel video", name)
		}
	}

	if blocked, err := q.IsRemoteActorBlockedInstanceWide(ctx, f.group); err != nil || !blocked {
		t.Errorf("the inbound gate must refuse the CHANNEL of an instance-blocked account: %v %v", blocked, err)
	}

	if _, err := q.UnblockRemoteActorInstanceWide(ctx, f.person); err != nil {
		t.Fatalf("admin unblock: %v", err)
	}
	if got := countCommentsFor(t, q, f.videoID, uuid.Nil); got != 2 {
		t.Fatalf("the admin unblock must restore both comments for everyone, got %d", got)
	}
	if !remoteVideoVisibleTo(t, q, f.remoteVideoID, uuid.Nil) {
		t.Fatal("the admin unblock must restore the remote video for everyone")
	}
}

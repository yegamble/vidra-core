//go:build integration

// Integration test for remote actors' votes on local videos (migration 0178).
// Same harness contract as integration_test.go.
package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestRemoteVideoRatingsOnRealPG proves the vote rules where they live: one row
// per (actor, video) however often it is redelivered, a switch flips it in
// place, the public count includes it alongside local votes, a stale Undo
// leaves a newer vote alone, an Undo by bare activity id finds it, and the
// actor's cache-row deletion cascades it away.
func TestRemoteVideoRatingsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	owner, channelID := seedOwnerChannel(ctx, t, st, "rrate")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "rated video")
	actorA := "https://peertube.example/accounts/a-" + uuid.NewString()
	actorB := "https://peertube.example/accounts/b-" + uuid.NewString()
	for _, a := range []string{actorA, actorB} {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Person', 'pem')`, a); err != nil {
			t.Fatalf("seed actor: %v", err)
		}
	}
	upsert := func(actor, rating, id string) {
		t.Helper()
		if _, err := q.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
			VideoID: videoID, RemoteActorUrl: actor, Rating: rating, ActivityUrl: id,
			Domain: "peertube.example", CapDomain: "peertube.example", DomainCap: 100,
		}); err != nil {
			t.Fatalf("UpsertRemoteVideoRating: %v", err)
		}
	}
	counts := func(wantLikes, wantDislikes int64) {
		t.Helper()
		c, err := q.CountVideoRatings(ctx, videoID)
		if err != nil {
			t.Fatalf("CountVideoRatings: %v", err)
		}
		if c.Likes != wantLikes || c.Dislikes != wantDislikes {
			t.Fatalf("counts = %d/%d, want %d/%d", c.Likes, c.Dislikes, wantLikes, wantDislikes)
		}
	}

	// A local vote and two remote ones, one redelivered.
	if err := q.UpsertVideoRating(ctx, sqlcgen.UpsertVideoRatingParams{VideoID: videoID, UserID: owner, Rating: "like"}); err != nil {
		t.Fatalf("local vote: %v", err)
	}
	upsert(actorA, "like", actorA+"/likes/1")
	upsert(actorA, "like", actorA+"/likes/2")
	upsert(actorB, "like", actorB+"/likes/1")
	counts(3, 0)

	// A switches: the same row flips.
	upsert(actorA, "dislike", actorA+"/dislikes/3")
	counts(2, 1)

	// A stale Undo{Like} from A leaves the newer dislike alone.
	del := func(actor, rating, id string) {
		t.Helper()
		vid := videoID
		if rating == "" {
			vid = uuid.Nil // the bare-id Undo carries no video
		}
		if err := q.DeleteRemoteVideoRating(ctx, sqlcgen.DeleteRemoteVideoRatingParams{
			RemoteActorUrl: actor, VideoID: vid, Rating: rating, ActivityUrl: id,
		}); err != nil {
			t.Fatalf("DeleteRemoteVideoRating: %v", err)
		}
	}
	del(actorA, "like", actorA+"/likes/1")
	counts(2, 1)
	// B cannot undo A's vote, even naming A's activity id.
	del(actorB, "dislike", actorA+"/dislikes/3")
	counts(2, 1)
	// A's Undo naming the vote by bare activity id (no video, no kind) finds it.
	del(actorA, "", actorA+"/dislikes/3")
	counts(2, 0)
	// B's cache row going (an actor Delete) cascades B's vote.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM remote_actors WHERE actor_url = $1`, actorB); err != nil {
		t.Fatalf("delete actor: %v", err)
	}
	counts(1, 0)
}

// TestRemoteVideoRatingsFilteredByBlocksOnRealPG proves a block taken AFTER a
// vote stops it counting and an unblock restores it, for each of the three
// scopes: the instance blocklist, the admin's instance-wide actor block, and
// the video owner's own block. Votes on another owner's video are untouched by
// the owner's block.
func TestRemoteVideoRatingsFilteredByBlocksOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	owner, channelID := seedOwnerChannel(ctx, t, st, "rrblk")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "blocked votes")
	other, otherChannel := seedOwnerChannel(ctx, t, st, "rrblk2")
	_ = other
	otherVideo := seedPublishedVideo(ctx, t, st, otherChannel, "other votes")
	domain := "blk-" + uuid.NewString()[:8] + ".example"
	actor := "https://" + domain + "/accounts/a"
	if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actors (actor_url, actor_type, domain, public_key_pem) VALUES ($1, 'Person', $2, 'pem')`, actor, domain); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	for _, v := range []uuid.UUID{videoID, otherVideo} {
		if _, err := q.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
			VideoID: v, RemoteActorUrl: actor, Rating: "like", ActivityUrl: actor + "/likes/" + v.String(),
			Domain: domain, CapDomain: domain, DomainCap: 100,
		}); err != nil {
			t.Fatalf("vote: %v", err)
		}
	}
	likes := func(v uuid.UUID) int64 {
		t.Helper()
		c, err := q.CountVideoRatings(ctx, v)
		if err != nil {
			t.Fatalf("CountVideoRatings: %v", err)
		}
		return c.Likes
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	scopes := []struct {
		name, block, unblock string
		args                 []any
		otherCounts          bool
	}{
		{"instance blocklist", `INSERT INTO blocked_instances (domain) VALUES ($1)`, `DELETE FROM blocked_instances WHERE domain = $1`, []any{domain}, false},
		{"instance-wide actor block", `INSERT INTO blocked_remote_actors (remote_actor_url) VALUES ($1)`, `DELETE FROM blocked_remote_actors WHERE remote_actor_url = $1`, []any{actor}, false},
		{"video owner's block", `INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url) VALUES ($1, $2)`, `DELETE FROM remote_actor_blocks WHERE blocker_id = $1 AND remote_actor_url = $2`, []any{owner, actor}, true},
	}
	for _, sc := range scopes {
		if likes(videoID) != 1 {
			t.Fatalf("%s: precondition likes = %d", sc.name, likes(videoID))
		}
		exec(sc.block, sc.args...)
		if n := likes(videoID); n != 0 {
			t.Errorf("%s: blocked vote still counts (%d)", sc.name, n)
		}
		if n := likes(otherVideo); (n == 1) != sc.otherCounts {
			t.Errorf("%s: other owner's video likes = %d", sc.name, n)
		}
		exec(sc.unblock, sc.args...)
		if n := likes(videoID); n != 1 {
			t.Errorf("%s: unblock did not restore the vote (%d)", sc.name, n)
		}
	}
}

// TestRemoteVideoRatingDomainCapOnRealPG proves the per-domain cap on NEW votes:
// at the cap a new (actor, video) row is refused (0 rows), while an existing
// voter can still change their vote.
func TestRemoteVideoRatingDomainCapOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "rrcap")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "capped votes")
	domain := "cap-" + uuid.NewString()[:8] + ".example"
	vote := func(n int, rating string) int64 {
		t.Helper()
		actor := "https://" + domain + "/accounts/" + string(rune('a'+n))
		if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Person', 'pem') ON CONFLICT DO NOTHING`, actor); err != nil {
			t.Fatalf("seed actor: %v", err)
		}
		rows, err := q.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
			VideoID: videoID, RemoteActorUrl: actor, Rating: rating, ActivityUrl: actor + "/" + rating,
			Domain: domain, CapDomain: domain, DomainCap: 2,
		})
		if err != nil {
			t.Fatalf("vote: %v", err)
		}
		return rows
	}
	if vote(0, "like") != 1 || vote(1, "like") != 1 {
		t.Fatal("votes under the cap were refused")
	}
	if rows := vote(2, "like"); rows != 0 {
		t.Fatalf("a new vote past the cap wrote %d rows", rows)
	}
	if rows := vote(0, "dislike"); rows != 1 {
		t.Fatalf("an existing voter's switch past the cap wrote %d rows", rows)
	}
	c, _ := q.CountVideoRatings(ctx, videoID)
	if c.Likes != 1 || c.Dislikes != 1 {
		t.Fatalf("counts = %d/%d, want 1/1", c.Likes, c.Dislikes)
	}
}

// TestRemoteVideoUserRatingsOnRealPG proves a local user's vote on a remote
// video (0179): one row per (user, video), a switch flips it, the delete hands
// back the removed vote for the Undo, a second delete finds nothing, and the
// remote video's removal cascades the vote.
func TestRemoteVideoUserRatingsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	user, _ := seedOwnerChannel(ctx, t, st, "rvrate")
	actor := "https://peertube.example/video-channels/c-" + uuid.NewString()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Group', 'pem')`, actor); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	var rv uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO remote_videos (object_url, remote_actor_url) VALUES ($1, $2) RETURNING id`,
		actor+"/v/1", actor).Scan(&rv); err != nil {
		t.Fatalf("seed remote video: %v", err)
	}
	set := func(r string) {
		t.Helper()
		if err := q.UpsertRemoteVideoUserRating(ctx, sqlcgen.UpsertRemoteVideoUserRatingParams{RemoteVideoID: rv, UserID: user, Rating: r}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	get := sqlcgen.GetRemoteVideoUserRatingParams{UserID: user, RemoteVideoID: rv}
	del := sqlcgen.DeleteRemoteVideoUserRatingParams{UserID: user, RemoteVideoID: rv}
	set("like")
	set("dislike")
	if r, err := q.GetRemoteVideoUserRating(ctx, get); err != nil || r != "dislike" {
		t.Fatalf("get = %q, %v; want dislike", r, err)
	}
	if r, err := q.DeleteRemoteVideoUserRating(ctx, del); err != nil || r != "dislike" {
		t.Fatalf("delete = %q, %v; want the removed dislike", r, err)
	}
	if _, err := q.DeleteRemoteVideoUserRating(ctx, del); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second delete err = %v, want pgx.ErrNoRows", err)
	}
	set("like")
	if _, err := st.Pool.Exec(ctx, `DELETE FROM remote_videos WHERE id = $1`, rv); err != nil {
		t.Fatalf("delete remote video: %v", err)
	}
	if _, err := q.GetRemoteVideoUserRating(ctx, get); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("vote survived its remote video: %v", err)
	}
}

// TestRemoteVideoRatingCapKeyedOnRegistrableDomainOnRealPG proves 150 actors on
// sub1..sub150 of one registrable domain share ONE budget (cap_domain), while
// `domain` keeps the exact host: blocking one subdomain hides only its votes.
func TestRemoteVideoRatingCapKeyedOnRegistrableDomainOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "rrcapsub")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "subdomain votes")
	reg := "capsub-" + uuid.NewString()[:8] + ".example"
	var recorded int64
	for i := 1; i <= 150; i++ {
		host := fmt.Sprintf("sub%d.%s", i, reg)
		actor := "https://" + host + "/accounts/a"
		if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actors (actor_url, actor_type, public_key_pem) VALUES ($1, 'Person', 'pem')`, actor); err != nil {
			t.Fatalf("seed actor: %v", err)
		}
		rows, err := q.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
			VideoID: videoID, RemoteActorUrl: actor, Rating: "like", ActivityUrl: actor + "/likes/1",
			Domain: host, CapDomain: reg, DomainCap: 100,
		})
		if err != nil {
			t.Fatalf("vote: %v", err)
		}
		recorded += rows
	}
	if recorded != 100 {
		t.Fatalf("recorded %d new votes across subdomains of one registrable domain, want 100", recorded)
	}
	likes := func() int64 {
		c, err := q.CountVideoRatings(ctx, videoID)
		if err != nil {
			t.Fatalf("CountVideoRatings: %v", err)
		}
		return c.Likes
	}
	if n := likes(); n != 100 {
		t.Fatalf("likes = %d, want 100", n)
	}
	// The block filter still reads the exact host: one subdomain's block drops
	// only its own vote, the registrable domain is not blocked wholesale.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO blocked_instances (domain) VALUES ($1)`, "sub1."+reg); err != nil {
		t.Fatalf("block: %v", err)
	}
	if n := likes(); n != 99 {
		t.Fatalf("likes after blocking sub1 = %d, want 99", n)
	}
}

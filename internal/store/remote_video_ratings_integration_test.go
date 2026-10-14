//go:build integration

// Integration test for remote actors' votes on local videos (migration 0178).
// Same harness contract as integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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
		if err := q.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
			VideoID: videoID, RemoteActorUrl: actor, Rating: rating, ActivityUrl: id,
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

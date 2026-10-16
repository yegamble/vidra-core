package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (f fakeRepo) GetRemoteVideoUserRating(_ context.Context, arg sqlcgen.GetRemoteVideoUserRatingParams) (string, error) {
	if r, ok := f.userRemoteRatings[arg.UserID.String()+"|"+arg.RemoteVideoID.String()]; ok {
		return r, nil
	}
	return "", pgx.ErrNoRows
}

func (f fakeRepo) UpsertRemoteVideoUserRating(_ context.Context, arg sqlcgen.UpsertRemoteVideoUserRatingParams) error {
	f.userRemoteRatings[arg.UserID.String()+"|"+arg.RemoteVideoID.String()] = arg.Rating
	return nil
}

func (f fakeRepo) DeleteRemoteVideoUserRating(_ context.Context, arg sqlcgen.DeleteRemoteVideoUserRatingParams) (string, error) {
	k := arg.UserID.String() + "|" + arg.RemoteVideoID.String()
	r, ok := f.userRemoteRatings[k]
	if !ok {
		return "", pgx.ErrNoRows
	}
	delete(f.userRemoteRatings, k)
	return r, nil
}

type rateWire struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Actor  string          `json:"actor"`
	Object json.RawMessage `json:"object"`
}

// drainRateDeliveries returns the queued activities (map order is random, so
// they are returned as a set keyed by type) and empties the queue.
func drainRateDeliveries(t *testing.T, repo fakeRepo) map[string]rateWire {
	t.Helper()
	out := map[string]rateWire{}
	for id, d := range repo.deliveries {
		if d.row.InboxUrl != "https://peer.example/inbox" || d.row.SigningUsername != "ada" || !d.row.SigningUserID.Valid {
			t.Errorf("delivery = %+v, want ada-signed to the origin inbox", d.row)
		}
		var a rateWire
		if err := json.Unmarshal(d.row.Payload, &a); err != nil {
			t.Fatal(err)
		}
		out[a.Type] = a
		delete(repo.deliveries, id)
	}
	return out
}

func TestRateRemoteVideoFederatesToOrigin(t *testing.T) {
	ctx := context.Background()
	repo, c, userID := newAuthoredRepo("https://peer.example/inbox")
	repo.userRemoteRatings = map[string]string{}
	svc := NewService(repo, WithBaseURL(noteBase))
	actor := noteBase + "/accounts/ada"
	likePrefix := actor + "/likes/" + c.RemoteVideoID.String() + "/"

	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "like"); err != nil {
		t.Fatalf("RateRemoteVideo: %v", err)
	}
	got := drainRateDeliveries(t, repo)
	like, ok := got["Like"]
	if len(got) != 1 || !ok || !strings.HasPrefix(like.ID, likePrefix) || like.Actor != actor || objectID(like.Object) != authoredOriginVideoURL {
		t.Fatalf("after like: %+v", got)
	}
	if r, _ := svc.RemoteVideoRating(ctx, c.RemoteVideoID, userID); r != "like" {
		t.Fatalf("stored rating = %q", r)
	}

	// The same vote again sends nothing.
	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "like"); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.deliveries); n != 0 {
		t.Fatalf("repeat like queued %d deliveries", n)
	}

	// A switch is Undo{Like} + Dislike, as PeerTube sends it.
	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "dislike"); err != nil {
		t.Fatal(err)
	}
	got = drainRateDeliveries(t, repo)
	undo, dislike := got["Undo"], got["Dislike"]
	if len(got) != 2 || !strings.HasPrefix(dislike.ID, actor+"/dislikes/") || objectID(dislike.Object) != authoredOriginVideoURL {
		t.Fatalf("after switch: %+v", got)
	}
	var inner rateWire
	_ = json.Unmarshal(undo.Object, &inner)
	if inner.Type != "Like" || inner.Actor != actor || objectID(inner.Object) != authoredOriginVideoURL || undo.Actor != actor {
		t.Fatalf("undo object = %+v, want the original Like", inner)
	}

	// Clearing sends Undo{Dislike}; clearing again sends nothing.
	if err := svc.ClearRemoteVideoRating(ctx, c.RemoteVideoID, userID); err != nil {
		t.Fatal(err)
	}
	got = drainRateDeliveries(t, repo)
	_ = json.Unmarshal(got["Undo"].Object, &inner)
	if len(got) != 1 || inner.Type != "Dislike" {
		t.Fatalf("after clear: %+v", got)
	}
	if err := svc.ClearRemoteVideoRating(ctx, c.RemoteVideoID, userID); err != nil || len(repo.deliveries) != 0 {
		t.Fatalf("second clear: err=%v deliveries=%d", err, len(repo.deliveries))
	}
}

func TestRateRemoteVideoRefusals(t *testing.T) {
	ctx := context.Background()
	repo, c, userID := newAuthoredRepo("https://peer.example/inbox")
	repo.userRemoteRatings = map[string]string{}
	svc := NewService(repo, WithBaseURL(noteBase))

	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "love"); !errors.Is(err, ErrInvalidRating) {
		t.Errorf("invalid rating err = %v", err)
	}
	// Unknown (or origin-blocked: GetRemoteVideoByID excludes those) video.
	if err := svc.RateRemoteVideo(ctx, uuid.New(), userID, "like"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown video err = %v, want ErrNotFound", err)
	}
	if len(repo.userRemoteRatings) != 0 || len(repo.deliveries) != 0 {
		t.Fatalf("refusal stored/queued: %v %d", repo.userRemoteRatings, len(repo.deliveries))
	}
	// An origin with no inbox: the vote is kept, nothing is queued.
	repo.remoteActors[authoredOriginActor] = sqlcgen.RemoteActor{ActorUrl: authoredOriginActor}
	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "like"); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("queued %d deliveries to an inbox-less origin", len(repo.deliveries))
	}
}

// failingEnqueueRepo fails every delivery enqueue.
type failingEnqueueRepo struct{ fakeRepo }

func (failingEnqueueRepo) EnqueueDelivery(context.Context, sqlcgen.EnqueueDeliveryParams) error {
	return errors.New("queue down")
}

// A vote whose delivery cannot be queued is not recorded either way: the
// enqueue runs BEFORE the write, so a failure leaves the stored vote matching
// what the origin was told, and the user's retry sends it again.
func TestRateRemoteVideoFailedEnqueueStoresNothing(t *testing.T) {
	ctx := context.Background()
	repo, c, userID := newAuthoredRepo("https://peer.example/inbox")
	repo.userRemoteRatings = map[string]string{}
	svc := NewService(failingEnqueueRepo{repo}, WithBaseURL(noteBase))

	if err := svc.RateRemoteVideo(ctx, c.RemoteVideoID, userID, "like"); err == nil {
		t.Fatal("RateRemoteVideo succeeded with the queue down")
	}
	if len(repo.userRemoteRatings) != 0 {
		t.Fatalf("vote stored without its delivery: %v", repo.userRemoteRatings)
	}
	repo.userRemoteRatings[userID.String()+"|"+c.RemoteVideoID.String()] = "like"
	if err := svc.ClearRemoteVideoRating(ctx, c.RemoteVideoID, userID); err == nil {
		t.Fatal("ClearRemoteVideoRating succeeded with the queue down")
	}
	if r := repo.userRemoteRatings[userID.String()+"|"+c.RemoteVideoID.String()]; r != "like" {
		t.Fatalf("vote cleared without its Undo: %q", r)
	}
}

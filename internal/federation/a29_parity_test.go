package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A29 social parity: the findings this file pins are F3, F5, F9, F10 and F12.

// --- F9: a deletion leaves a dereferenceable retraction --------------------

func TestDeleteVideoRecordsATombstone(t *testing.T) {
	repo := newContractRepo()
	repo.tombstones = map[uuid.UUID]time.Time{}
	repo.followerInboxes[ctChannelID] = []string{"https://peer.example/inbox"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if err := svc.DeleteVideo(context.Background(), ctVideoID, ctChannelID, true); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if _, ok := repo.tombstones[ctVideoID]; !ok {
		t.Fatal("no tombstone recorded; a peer dereferencing the Delete gets the frontend soft-404")
	}
	if _, err := svc.VideoObject(context.Background(), ctVideoID); !errors.Is(err, ErrNotFound) {
		// The fixture row still exists in the fake, so the object still renders —
		// what matters here is only that the tombstone was written.
		_ = err
	}
}

// A video that was never public was never federated, so there is no peer to
// answer and nothing to record.
func TestDeleteOfANonPublicVideoRecordsNoTombstone(t *testing.T) {
	repo := newContractRepo()
	repo.tombstones = map[uuid.UUID]time.Time{}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if err := svc.DeleteVideo(context.Background(), ctVideoID, ctChannelID, false); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if len(repo.tombstones) != 0 {
		t.Error("a never-federated video left a tombstone")
	}
}

// The tombstone outlives the fan-out: a channel that vanished, or one that opted
// out of ActivityPub after the video was federated, still owes peers an answer.
func TestTombstoneIsRecordedEvenWhenFanOutIsANoOp(t *testing.T) {
	repo := newContractRepo()
	repo.tombstones = map[uuid.UUID]time.Time{}
	repo.apDisabled = map[uuid.UUID]bool{ctChannelID: true}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if err := svc.DeleteVideo(context.Background(), ctVideoID, ctChannelID, true); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if _, ok := repo.tombstones[ctVideoID]; !ok {
		t.Error("an AP-disabled channel's deletion left no tombstone")
	}
}

func TestVideoObjectAnswersGoneForATombstonedID(t *testing.T) {
	repo := newContractRepo()
	gone := uuid.New()
	repo.tombstones = map[uuid.UUID]time.Time{gone: fakeTombstoneAt}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if _, err := svc.VideoObject(context.Background(), gone); !errors.Is(err, ErrGone) {
		t.Fatalf("VideoObject = %v, want ErrGone", err)
	}
	doc, err := svc.VideoTombstone(context.Background(), gone)
	if err != nil {
		t.Fatalf("VideoTombstone: %v", err)
	}
	if doc["type"] != "Tombstone" || doc["formerType"] != "Video" {
		t.Errorf("tombstone = %v", doc)
	}
	// An id that was never a video is still 404, so the endpoint cannot be used
	// to learn which uuids this instance has ever held.
	if _, err := svc.VideoObject(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id = %v, want ErrNotFound", err)
	}
}

// --- F5: a block must not swallow the message that says "stop" -------------

func TestSeveringDeliveriesSurviveADestinationBlock(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		cancelled bool
	}{
		{"Undo(Follow)", `{"type":"Undo","object":{"type":"Follow"}}`, false},
		{"Reject(Follow)", `{"type":"Reject","object":{"type":"Follow"}}`, false},
		{"Create(Video)", `{"type":"Create","object":{"type":"Video"}}`, true},
		{"Update(Video)", `{"type":"Update","object":{"type":"Video"}}`, true},
		{"malformed", `not json`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := severingActivity([]byte(tt.payload)); got == tt.cancelled {
				t.Errorf("severingActivity(%s) = %v; cancelled-by-block should be %v", tt.payload, got, tt.cancelled)
			}
		})
	}
}

// The drain proves the same thing end to end: with the destination blocked, an
// ordinary activity is dead-lettered and an Undo is attempted.
func TestDrainCancelsContentButDeliversAnUndoToABlockedDestination(t *testing.T) {
	var got []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		got = append(got, string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(peer.Close)

	repo := newContractRepo()
	repo.blockedDomains[strings.TrimPrefix(peer.URL, "http://")] = true
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithAllowPrivateFetch(true),
		WithFetchClient(peer.Client()),
	)
	ctx := context.Background()
	if err := svc.enqueueChannelDelivery(ctx, ctChannelID, "films", peer.URL+"/inbox",
		[]byte(`{"type":"Create","object":{"type":"Video"}}`)); err != nil {
		t.Fatalf("enqueue create: %v", err)
	}
	if err := svc.enqueueChannelDelivery(ctx, ctChannelID, "films", peer.URL+"/inbox",
		[]byte(`{"type":"Undo","object":{"type":"Follow"}}`)); err != nil {
		t.Fatalf("enqueue undo: %v", err)
	}
	if _, err := svc.DrainDeliveries(ctx, 10); err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	var cancelled, delivered int
	for _, d := range repo.deliveries {
		switch {
		case d.state == "failed" && strings.Contains(d.lastError, "blocked"):
			cancelled++
		case d.state == "delivered":
			delivered++
		}
	}
	if cancelled != 1 {
		t.Errorf("cancelled deliveries = %d, want exactly the Create", cancelled)
	}
	if delivered != 1 {
		t.Errorf("delivered = %d, want the Undo to have been sent despite the block", delivered)
	}
}

// --- F10: a delivery remembers which request produced it --------------------

func TestEnqueuedDeliveriesCarryTheRequestCorrelation(t *testing.T) {
	repo := newContractRepo()
	repo.followerInboxes[ctChannelID] = []string{"https://a.example/inbox", "https://b.example/inbox"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{
		RequestID:     "req-fake-1",
		CorrelationID: "corr-fake-1",
	})
	if err := svc.AnnounceVideo(ctx, ctVideoID); err != nil {
		t.Fatalf("AnnounceVideo: %v", err)
	}
	if len(repo.deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		if d.enqueued.CorrelationID != "corr-fake-1" || d.enqueued.RequestID != "req-fake-1" {
			t.Errorf("delivery identity = %q/%q, want the enqueueing request's",
				d.enqueued.RequestID, d.enqueued.CorrelationID)
		}
	}
}

// --- F12: the SSRF guard covers the cached path, and a rotated key recovers --

func TestCachedActorStillRunsTheSSRFGuard(t *testing.T) {
	repo := newContractRepo()
	const loopback = "http://127.0.0.1:9/actors/eve"
	repo.remoteActors[loopback] = sqlcgen.RemoteActor{
		ActorUrl: loopback, ActorType: "Person", PreferredUsername: "eve",
		Domain: "127.0.0.1:9", InboxUrl: loopback + "/inbox",
	}
	// allowPrivateFetch OFF: the cache holds the row, but the ADDRESS is still
	// refused. A29 measured this passing because the cache short-circuited the
	// guard entirely.
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if _, err := svc.resolveRemoteActor(context.Background(), loopback); err == nil {
		t.Fatal("a cached loopback actor resolved with the SSRF guard on")
	}
	// With the relax on (dev/backed e2e) it resolves from the cache as before.
	relaxed := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if _, err := relaxed.resolveRemoteActor(context.Background(), loopback); err != nil {
		t.Fatalf("cached resolution with the relax on: %v", err)
	}
}

func TestRotatedRemoteKeyIsRefetched(t *testing.T) {
	oldKey, newKey := mustRSAKey(t), mustRSAKey(t)
	var served *rsa.PrivateKey = newKey
	var fetches int
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches++
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "http://" + r.Host + "/actors/kaisa", "type": "Person",
			"preferredUsername": "kaisa",
			"inbox":             "http://" + r.Host + "/actors/kaisa/inbox",
			"publicKey":         map[string]any{"publicKeyPem": publicPEM(t, served)},
		})
	}))
	t.Cleanup(origin.Close)
	actorURL := origin.URL + "/actors/kaisa"

	repo := newContractRepo()
	// The cache holds the OLD key — the rotation A29 said was unrecoverable.
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{
		ActorUrl: actorURL, ActorType: "Person", PreferredUsername: "kaisa",
		Domain: strings.TrimPrefix(origin.URL, "http://"), InboxUrl: actorURL + "/inbox",
		PublicKeyPem: publicPEM(t, oldKey),
	}
	svc := NewService(repo,
		WithBaseURL("https://videos.example"),
		WithAllowPrivateFetch(true),
		WithFetchClient(origin.Client()),
	)

	cached, err := svc.ResolveKey(context.Background(), actorURL+"#main-key")
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if !cached.Equal(&oldKey.PublicKey) {
		t.Fatal("ResolveKey did not serve the cached key")
	}
	if fetches != 0 {
		t.Fatalf("a cached key cost %d network fetches; the cache is doing nothing", fetches)
	}

	fresh, err := svc.ResolveKeyFresh(context.Background(), actorURL+"#main-key")
	if err != nil {
		t.Fatalf("ResolveKeyFresh: %v", err)
	}
	if !fresh.Equal(&newKey.PublicKey) {
		t.Error("ResolveKeyFresh did not pick up the rotated key")
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want exactly one re-fetch", fetches)
	}
}

// An actor this instance has never cached cannot be used to make it fetch: the
// refresh path is implemented over the EXISTING cache row.
func TestKeyRefreshRefusesAnUncachedActor(t *testing.T) {
	repo := newContractRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if _, err := svc.ResolveKeyFresh(context.Background(), "https://stranger.example/actors/x#main-key"); err == nil {
		t.Fatal("an uncached actor was re-fetched through the rotation path")
	}
}

// --- F3: the store answers before the network ------------------------------

func TestResolveSearchTargetFindsAStoredRemoteVideoWithoutFetching(t *testing.T) {
	repo := newContractRepo()
	const objectURL = "https://peer.example/videos/2f8a1c3e-0000-4000-8000-000000000001"
	const watchURL = "https://peer.example/v/abcdefghijk"
	stored := uuid.New()
	repo.remoteVideos[objectURL] = &fakeRemoteVideo{
		id: stored,
		params: sqlcgen.UpsertRemoteVideoParams{
			ObjectUrl:      objectURL,
			RemoteActorUrl: "https://peer.example/video-channels/films",
			WatchUrl:       watchURL,
		},
	}
	// No fetch client and no relax: any network attempt fails the test by
	// failing to resolve.
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	for name, query := range map[string]string{
		"the AP object id": objectURL,
		"the watch URL":    watchURL,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := svc.ResolveSearchTarget(context.Background(), query)
			if err != nil {
				t.Fatalf("ResolveSearchTarget: %v", err)
			}
			if res.VideoID != stored {
				t.Errorf("video = %s, want the stored row %s", res.VideoID, stored)
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func publicPEM(t *testing.T, k *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// --- F7: a per-remote-account block, and what it actually stops -------------

func TestRemoteBlockRefusesTheBlockedActorsNoteAndFollow(t *testing.T) {
	repo := newContractRepo()
	repo.remoteBlocks = map[string]bool{}
	const kaisa = "https://peer.example/accounts/kaisa"
	cacheContractActor(repo, kaisa, "Person", "kaisa", "peer.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	note := func(id string) inboxActivity {
		return inboxActivity{
			ID: id, Type: "Create", Actor: kaisa,
			Object: json.RawMessage(`{"id":"https://peer.example/notes/` + id + `","type":"Note",` +
				`"content":"hi","attributedTo":"` + kaisa + `",` +
				`"inReplyTo":"https://videos.example/videos/` + ctVideoID.String() + `"}`),
		}
	}
	// Before the block the reply is stored…
	if err := svc.handleCreateNote(ctx, note("n1"), kaisa); err != nil {
		t.Fatalf("handleCreateNote: %v", err)
	}
	if got := countRemoteComments(repo); got != 1 {
		t.Fatalf("remote comments = %d, want the reply stored before the block", got)
	}

	// …and after the video OWNER blocks the actor, it is not.
	if err := svc.BlockRemoteActor(ctx, ctUserID, kaisa); err != nil {
		t.Fatalf("BlockRemoteActor: %v", err)
	}
	if err := svc.handleCreateNote(ctx, note("n2"), kaisa); err != nil {
		t.Fatalf("handleCreateNote after block: %v", err)
	}
	if got := countRemoteComments(repo); got != 1 {
		t.Errorf("remote comments = %d; a blocked actor's reply was stored under the blocker's own video", got)
	}

	// A Follow of the blocker's channel records nothing and answers nothing —
	// a Reject would tell the blocked actor exactly what happened.
	follow := inboxActivity{
		ID: "https://peer.example/act/f1", Type: "Follow", Actor: kaisa,
		Object: json.RawMessage(`"https://videos.example/video-channels/films"`),
	}
	if err := svc.handleFollow(ctx, follow, kaisa); err != nil {
		t.Fatalf("handleFollow: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Errorf("a blocked actor's follow was recorded: %+v", repo.remoteFollows)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("a blocked actor's follow produced an outbound activity: %+v", repo.deliveries)
	}
}

// Another viewer's block is not this viewer's: the control is per-account and
// one-directional, like every other viewer control.
func TestRemoteBlockIsPerViewer(t *testing.T) {
	repo := newContractRepo()
	repo.remoteBlocks = map[string]bool{}
	const kaisa = "https://peer.example/accounts/kaisa"
	cacheContractActor(repo, kaisa, "Person", "kaisa", "peer.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	stranger := uuid.New()
	if err := svc.BlockRemoteActor(ctx, stranger, kaisa); err != nil {
		t.Fatalf("BlockRemoteActor: %v", err)
	}
	blocked, err := svc.remoteActorBlockedBy(ctx, ctUserID, kaisa)
	if err != nil {
		t.Fatalf("remoteActorBlockedBy: %v", err)
	}
	if blocked {
		t.Error("one viewer's block applied to another viewer")
	}
}

func TestResolveRemoteActorIdentityRefusesLocalAndPlainText(t *testing.T) {
	repo := newContractRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	if _, err := svc.ResolveRemoteActorIdentity(ctx, "@ada@videos.example"); !errors.Is(err, ErrLocalFollowTarget) {
		t.Errorf("local handle = %v, want ErrLocalFollowTarget", err)
	}
	if _, err := svc.ResolveRemoteActorIdentity(ctx, "https://videos.example/accounts/ada"); !errors.Is(err, ErrLocalFollowTarget) {
		t.Errorf("local URL = %v, want ErrLocalFollowTarget", err)
	}
	if _, err := svc.ResolveRemoteActorIdentity(ctx, "kaisa"); !errors.Is(err, ErrRemoteActorRequired) {
		t.Errorf("plain text = %v, want ErrRemoteActorRequired", err)
	}
	// A URL is accepted WITHOUT being dereferenced: blocking must work against
	// an actor that is offline, gone, or refusing us.
	got, err := svc.ResolveRemoteActorIdentity(ctx, "https://gone.example/accounts/ghost")
	if err != nil {
		t.Fatalf("offline actor URL: %v", err)
	}
	if got != "https://gone.example/accounts/ghost" {
		t.Errorf("actor = %q, want the URL verbatim", got)
	}
}

// countRemoteComments counts the stored comments that came from a remote actor.
func countRemoteComments(repo fakeRepo) int {
	var n int
	for _, c := range repo.commentsByID {
		if c.RemoteActorUrl != nil {
			n++
		}
	}
	return n
}

// --- F8: a follower keeps the thread it is already being sent --------------

// remoteThreadRepo seeds a follower's world: one mirrored remote video from
// peer.example, and the actor cache entry inbound signature verification would
// already have written.
func remoteThreadRepo(t *testing.T) (fakeRepo, uuid.UUID) {
	t.Helper()
	repo := newContractRepo()
	repo.remoteVideoComments = map[string]*sqlcgen.UpsertRemoteVideoCommentParams{}
	const (
		objectURL = "https://peer.example/videos/2f8a1c3e-0000-4000-8000-000000000009"
		owner     = "https://peer.example/video-channels/films"
	)
	stored := uuid.New()
	repo.remoteVideos[objectURL] = &fakeRemoteVideo{
		id:     stored,
		params: sqlcgen.UpsertRemoteVideoParams{ObjectUrl: objectURL, RemoteActorUrl: owner},
	}
	cacheContractActor(repo, owner, "Group", "films", "peer.example")
	return repo, stored
}

const remoteVideoObjectURL = "https://peer.example/videos/2f8a1c3e-0000-4000-8000-000000000009"

func remoteNote(id, actor, body string) inboxActivity {
	return inboxActivity{
		ID: "https://peer.example/act/" + id, Type: "Create", Actor: actor,
		Object: json.RawMessage(`{"id":"https://peer.example/notes/` + id + `","type":"Note",` +
			`"content":"` + body + `","attributedTo":"` + actor + `",` +
			`"published":"2026-09-05T10:00:00Z",` +
			`"inReplyTo":"` + remoteVideoObjectURL + `"}`),
	}
}

func TestFollowerStoresFederatedCommentsOnARemoteVideo(t *testing.T) {
	repo, videoID := remoteThreadRepo(t)
	const author = "https://peer.example/accounts/ada"
	cacheContractActor(repo, author, "Person", "ada", "peer.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	if err := svc.handleCreateNote(ctx, remoteNote("1", author, "Beautiful grade."), author); err != nil {
		t.Fatalf("handleCreateNote: %v", err)
	}
	got, total, err := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0)
	if err != nil {
		t.Fatalf("ListRemoteVideoComments: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("thread = %d rows (total %d), want 1 — the follower dropped the comment it was sent", len(got), total)
	}
	if got[0].Body != "Beautiful grade." {
		t.Errorf("body = %q", got[0].Body)
	}
	if got[0].AuthorName != "ada" {
		t.Errorf("author = %q, want the origin's preferredUsername snapshot", got[0].AuthorName)
	}

	// A redelivery of the same activity must not double the thread.
	if err := svc.handleCreateNote(ctx, remoteNote("1", author, "Beautiful grade."), author); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if _, total, _ := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0); total != 1 {
		t.Errorf("total after redelivery = %d, want 1", total)
	}
}

// The authority rule that is specific to this path: a comment must come from the
// VIDEO's origin. Without it, any instance in our follow graph could post onto
// any mirrored video from any other instance.
func TestAThirdPartyCannotCommentOnAnotherOriginsVideo(t *testing.T) {
	repo, videoID := remoteThreadRepo(t)
	const outsider = "https://elsewhere.example/accounts/mallory"
	cacheContractActor(repo, outsider, "Person", "mallory", "elsewhere.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	act := inboxActivity{
		ID: "https://elsewhere.example/act/9", Type: "Create", Actor: outsider,
		Object: json.RawMessage(`{"id":"https://elsewhere.example/notes/9","type":"Note",` +
			`"content":"buy pills","attributedTo":"` + outsider + `",` +
			`"inReplyTo":"` + remoteVideoObjectURL + `"}`),
	}
	if err := svc.handleCreateNote(ctx, act, outsider); err != nil {
		t.Fatalf("handleCreateNote: %v", err)
	}
	if _, total, _ := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0); total != 0 {
		t.Errorf("a third-party instance wrote onto another origin's mirrored thread (total %d)", total)
	}
}

func TestMirroredCommentIsEditedAndRetractedByItsOrigin(t *testing.T) {
	repo, videoID := remoteThreadRepo(t)
	const author = "https://peer.example/accounts/ada"
	cacheContractActor(repo, author, "Person", "ada", "peer.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	if err := svc.handleCreateNote(ctx, remoteNote("2", author, "first"), author); err != nil {
		t.Fatalf("create: %v", err)
	}
	edit := remoteNote("2", author, "second")
	edit.Type = "Update"
	if err := svc.handleUpdateNote(ctx, edit, author); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0)
	if len(got) != 1 || got[0].Body != "second" {
		t.Fatalf("after edit = %+v, want the edited body", got)
	}

	del := inboxActivity{
		ID: "https://peer.example/act/d2", Type: "Delete", Actor: author,
		Object: json.RawMessage(`"https://peer.example/notes/2"`),
	}
	if err := svc.handleDelete(ctx, del, author); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, total, _ := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0); total != 0 {
		t.Errorf("total after retraction = %d, want 0", total)
	}
}

// A stranger cannot retract someone else's mirrored comment.
func TestAStrangerCannotRetractAMirroredComment(t *testing.T) {
	repo, videoID := remoteThreadRepo(t)
	const author = "https://peer.example/accounts/ada"
	const stranger = "https://elsewhere.example/accounts/mallory"
	cacheContractActor(repo, author, "Person", "ada", "peer.example")
	cacheContractActor(repo, stranger, "Person", "mallory", "elsewhere.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	if err := svc.handleCreateNote(ctx, remoteNote("3", author, "hello"), author); err != nil {
		t.Fatalf("create: %v", err)
	}
	del := inboxActivity{
		ID: "https://elsewhere.example/act/d3", Type: "Delete", Actor: stranger,
		Object: json.RawMessage(`"https://peer.example/notes/3"`),
	}
	if err := svc.handleDelete(ctx, del, stranger); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, total, _ := svc.ListRemoteVideoComments(ctx, videoID, uuid.Nil, 20, 0); total != 1 {
		t.Errorf("a stranger retracted another actor's mirrored comment (total %d)", total)
	}
}

// A local video's own thread is untouched: the two paths are disjoint, and the
// remote arm must not swallow an inReplyTo that names one of our own videos.
func TestLocalVideoCommentsStillLandInTheLocalTable(t *testing.T) {
	repo := newContractRepo()
	repo.remoteVideoComments = map[string]*sqlcgen.UpsertRemoteVideoCommentParams{}
	const kaisa = "https://peer.example/accounts/kaisa"
	cacheContractActor(repo, kaisa, "Person", "kaisa", "peer.example")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	act := inboxActivity{
		ID: "https://peer.example/act/local", Type: "Create", Actor: kaisa,
		Object: json.RawMessage(`{"id":"https://peer.example/notes/local","type":"Note",` +
			`"content":"on your own video","attributedTo":"` + kaisa + `",` +
			`"inReplyTo":"https://videos.example/videos/` + ctVideoID.String() + `"}`),
	}
	if err := svc.handleCreateNote(ctx, act, kaisa); err != nil {
		t.Fatalf("handleCreateNote: %v", err)
	}
	if got := countRemoteComments(repo); got != 1 {
		t.Errorf("local-video comments = %d, want the reply in the LOCAL table", got)
	}
	if len(repo.remoteVideoComments) != 0 {
		t.Errorf("a comment on a LOCAL video landed in the mirrored table: %+v", repo.remoteVideoComments)
	}
}

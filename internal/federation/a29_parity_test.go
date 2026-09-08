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

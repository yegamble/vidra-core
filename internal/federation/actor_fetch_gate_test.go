package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The negative cache belongs to the inbound-signature path only: one failed
// resolution there must not stop a local follow (or search, delivery,
// instance-follow) from fetching the same actor.
func TestInboxPathFailureDoesNotBlockALocalFollowFetch(t *testing.T) {
	pemStr, _ := testActorPEM(t)
	var up atomic.Bool
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"id":"http://` + r.Host + `/accounts/bob","type":"Group",` +
			`"inbox":"http://` + r.Host + `/accounts/bob/inbox","publicKey":{"publicKeyPem":` + quote(pemStr) + `}}`))
	}))
	t.Cleanup(srv.Close)
	svc, _, _ := newFetchBoundsService(newRemoteRepo())
	actorURL := srv.URL + "/accounts/bob"

	if _, err := svc.ResolveKey(context.Background(), actorURL+"#main-key"); err == nil {
		t.Fatal("ResolveKey resolved an actor whose origin is down")
	}
	up.Store(true)
	if _, err := svc.resolveFollowActor(context.Background(), []string{actorURL}); err != nil {
		t.Fatalf("a local follow was refused after an inbox-path failure: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2 (the follow must fetch)", got)
	}
}

// lockedActorRepo serialises the actor-cache methods of the map-backed fake so
// concurrent resolutions are race-free.
type lockedActorRepo struct {
	fakeRepo
	mu *sync.Mutex
}

func (r lockedActorRepo) GetRemoteActor(ctx context.Context, u string) (sqlcgen.RemoteActor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fakeRepo.GetRemoteActor(ctx, u)
}

func (r lockedActorRepo) UpsertRemoteActor(ctx context.Context, arg sqlcgen.UpsertRemoteActorParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fakeRepo.UpsertRemoteActor(ctx, arg)
}

// Concurrent misses on one actor URL share one fetch.
func TestConcurrentMissesShareOneFetch(t *testing.T) {
	pemStr, _ := testActorPEM(t)
	var hits atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"id":"http://` + r.Host + `/accounts/bob","type":"Person",` +
			`"inbox":"http://` + r.Host + `/accounts/bob/inbox","publicKey":{"publicKeyPem":` + quote(pemStr) + `}}`))
	}))
	t.Cleanup(srv.Close)
	svc := NewService(lockedActorRepo{fakeRepo: newRemoteRepo(), mu: &sync.Mutex{}}, WithAllowPrivateFetch(true))

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/bob#main-key")
			errs <- err
		}()
	}
	time.Sleep(200 * time.Millisecond) // let every caller reach the miss
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ResolveKey: %v", err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("20 concurrent misses caused %d fetches, want 1", got)
	}
}

// A full map is trimmed in one batch, not one entry per insert.
func TestActorFetchGateEvictsInBatches(t *testing.T) {
	g := newActorFetchGate()
	now := time.Now()
	g.now = func() time.Time { return now }
	for i := 0; i < maxActorFetchEntries; i++ {
		g.recordFailure("https://peer.example.test/a/" + string(rune('a'+i%26)) + time.Duration(i).String())
	}
	g.recordFailure("https://peer.example.test/one-more")
	if n := len(g.failed); n > maxActorFetchEntries*9/10+1 {
		t.Fatalf("after one insert into a full map it holds %d entries, want <= %d", n, maxActorFetchEntries*9/10+1)
	}
	if !g.recentlyFailed("https://peer.example.test/one-more") {
		t.Fatal("the inserted entry was evicted")
	}
}

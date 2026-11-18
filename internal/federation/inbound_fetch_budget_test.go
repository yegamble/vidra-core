package federation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// perHostOrigin answers every actor fetch with a 404, counting per host. A host
// in hold blocks until release is closed, to keep fetches in flight.
type perHostOrigin struct {
	mu      sync.Mutex
	hits    map[string]int
	hold    map[string]bool
	started chan string
	release chan struct{}
}

func newPerHostOrigin() *perHostOrigin {
	return &perHostOrigin{hits: map[string]int{}, hold: map[string]bool{}, started: make(chan string, 64), release: make(chan struct{})}
}

func (o *perHostOrigin) RoundTrip(r *http.Request) (*http.Response, error) {
	o.mu.Lock()
	o.hits[r.URL.Hostname()]++
	held := o.hold[r.URL.Hostname()]
	o.mu.Unlock()
	if held {
		o.started <- r.URL.Hostname()
		<-o.release
	}
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func (o *perHostOrigin) count(host string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hits[host]
}

func newBudgetService(o *perHostOrigin, opts ...Option) *Service {
	svc := NewService(newRemoteRepo(), append([]Option{WithFetchClient(&http.Client{Transport: o})}, opts...)...)
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.actorFetches.now = func() time.Time { return frozen }
	return svc
}

// 500 distinct keyIds on one host buy inboundHostBurst fetches while the
// clock stands still (the bound over a window W is burst + W/refill), and a
// second host is unaffected. Port and case variants are the same host.
func TestInboundActorFetchesAreBudgetedPerHost(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	ctx := context.Background()
	for i := 0; i < 500; i++ {
		host := "flood.example.test"
		switch i % 3 {
		case 1:
			host = "FLOOD.example.test:443"
		case 2:
			host = "flood.example.test."
		}
		_, _ = svc.ResolveKey(ctx, fmt.Sprintf("https://%s/accounts/a%d#main-key", host, i))
	}
	if got := o.count("flood.example.test") + o.count("FLOOD.example.test") + o.count("flood.example.test."); got != inboundHostBurst {
		t.Fatalf("500 distinct keyIds on one host caused %d fetches, want %d", got, inboundHostBurst)
	}
	_, _ = svc.ResolveKey(ctx, "https://quiet.example.test/accounts/bob#main-key")
	if got := o.count("quiet.example.test"); got != 1 {
		t.Fatalf("a second host was fetched %d times, want 1", got)
	}
}

// The global in-flight cap holds inbound fetches across hosts, but a host this
// instance follows is not starved by it.
func TestInboundFetchCapDoesNotStarveAFollowedHost(t *testing.T) {
	o := newPerHostOrigin()
	followed := "https://followed.example.test/accounts/chan"
	svc := newBudgetService(o, WithFollowEdgeChecker(edgesFor(followed)))
	svc.actorFetches.maxInflight = 2
	o.hold["slow1.example.test"], o.hold["slow2.example.test"] = true, true
	ctx := context.Background()

	var wg sync.WaitGroup
	for _, h := range []string{"slow1.example.test", "slow2.example.test"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.ResolveKey(ctx, "https://"+h+"/accounts/x#main-key")
		}()
		<-o.started
	}
	_, _ = svc.ResolveKey(ctx, "https://other.example.test/accounts/x#main-key")
	_, _ = svc.ResolveKey(ctx, followed+"#main-key")
	close(o.release)
	wg.Wait()

	if got := o.count("other.example.test"); got != 0 {
		t.Errorf("an unfollowed host was fetched %d times past the in-flight cap", got)
	}
	if got := o.count("followed.example.test"); got != 1 {
		t.Errorf("a followed host was fetched %d times, want 1 (not starved)", got)
	}
}

// holdInflight fills the global in-flight cap with fetches held at origin; the
// returned func releases them and waits.
func holdInflight(t *testing.T, svc *Service, o *perHostOrigin, hosts ...string) func() {
	t.Helper()
	var wg sync.WaitGroup
	for _, h := range hosts {
		o.mu.Lock()
		o.hold[h] = true
		o.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.ResolveKey(context.Background(), "https://"+h+"/accounts/x#main-key")
		}()
		<-o.started
	}
	return func() { close(o.release); wg.Wait() }
}

// A key refresh takes a slot from the same global cap. Refused, it fetches
// nothing and leaves the actor's refresh cooldown unspent; a followed actor's
// refresh still runs.
func TestKeyRefreshTakesAnInflightSlot(t *testing.T) {
	o := newPerHostOrigin()
	followed := "https://followed.example.test/accounts/chan"
	svc := newBudgetService(o, WithFollowEdgeChecker(edgesFor(followed)))
	svc.actorFetches.maxInflight = 2
	pemStr, _ := testActorPEM(t)
	cached := "https://cached.example.test/accounts/bob"
	repo := svc.repo.(fakeRepo)
	repo.remoteActors[cached] = sqlcgen.RemoteActor{ActorUrl: cached, PublicKeyPem: pemStr}
	repo.remoteActors[followed] = sqlcgen.RemoteActor{ActorUrl: followed, PublicKeyPem: pemStr}
	done := holdInflight(t, svc, o, "slow1.example.test", "slow2.example.test")

	for i := 0; i < 5; i++ {
		_, _ = svc.ResolveKeyFresh(context.Background(), cached+"#main-key")
	}
	_, _ = svc.ResolveKeyFresh(context.Background(), followed+"#main-key")
	done()

	if got := o.count("cached.example.test"); got != 0 {
		t.Errorf("refreshes past the cap fetched %d times, want 0", got)
	}
	if !svc.actorFetches.allowRefresh(cached) {
		t.Error("a refused refresh spent the actor's cooldown")
	}
	if got := o.count("followed.example.test"); got != 1 {
		t.Errorf("a followed actor's refresh fetched %d times, want 1", got)
	}
}

// A followed actor is fetched even when its host's bucket is empty.
func TestFollowedActorIsNotRefusedByAnEmptyHostBucket(t *testing.T) {
	o := newPerHostOrigin()
	followed := "https://flood.example.test/accounts/chan"
	svc := newBudgetService(o, WithFollowEdgeChecker(edgesFor(followed)))
	for i := 0; i < 50; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://flood.example.test/accounts/a%d#main-key", i))
	}
	before := o.count("flood.example.test")
	_, _ = svc.ResolveKey(context.Background(), followed+"#main-key")
	if got := o.count("flood.example.test") - before; got != 1 {
		t.Fatalf("a followed actor on an exhausted host was fetched %d times, want 1", got)
	}
}

// Concurrent admissions never overspend a bucket, including across the
// unlocked "is it followed?" lookup.
func TestAdmitInboundNeverOverspendsUnderConcurrency(t *testing.T) {
	g := newActorFetchGate()
	g.maxInflight = 1
	var wg sync.WaitGroup
	var mu sync.Mutex
	unfollowedAdmitted := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			isFollowed := i%2 == 0
			release, err := g.admitInbound("h.example.test", func() bool { return isFollowed })
			if err != nil {
				return
			}
			if !isFollowed {
				mu.Lock()
				unfollowedAdmitted++
				mu.Unlock()
			}
			release()
		}()
	}
	wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	if b := g.hosts["h.example.test"]; b.tokens < 0 {
		t.Fatalf("bucket overspent: tokens = %v", b.tokens)
	}
	if unfollowedAdmitted > inboundHostBurst {
		t.Fatalf("%d unfollowed admissions, want <= %d", unfollowedAdmitted, inboundHostBurst)
	}
}

// slowEdges never answers before its context ends.
type slowEdges struct{}

func (slowEdges) HasAcceptedFollow(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return true, ctx.Err()
}

// The "is it followed?" lookup is bounded; a timeout counts as not followed.
func TestFollowedLookupIsBounded(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o, WithFollowEdgeChecker(slowEdges{}))
	svc.actorFetches.maxInflight = 0
	start := time.Now()
	_, _ = svc.ResolveKey(context.Background(), "https://peer.example.test/accounts/bob#main-key")
	if d := time.Since(start); d > followedLookupTimeout+2*time.Second {
		t.Fatalf("the lookup took %v, want about %v", d, followedLookupTimeout)
	}
	if got := o.count("peer.example.test"); got != 0 {
		t.Fatalf("a timed-out lookup counted as followed: %d fetches", got)
	}
}

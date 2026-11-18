package federation

import (
	"context"
	"errors"
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

// An admission waiting on the "is it followed?" lookup re-reads the bucket
// once it has an answer: a token spent meanwhile is not spent twice.
func TestAdmitInboundRereadsTheBucketAfterTheLookup(t *testing.T) {
	g := newActorFetchGate()
	frozen := time.Now()
	g.now = func() time.Time { return frozen }
	g.maxInflight = 1
	const host = "h.example.test"
	g.mu.Lock()
	g.bucketLocked(host).tokens = 1
	g.mu.Unlock()
	hold, err := g.admitInflight(func() bool { return false }) // fills the cap
	if err != nil {
		t.Fatal(err)
	}
	asked, answer := make(chan struct{}), make(chan bool)
	done := make(chan error)
	go func() {
		release, err := g.admitInbound(host, func() bool { close(asked); return <-answer })
		if release != nil {
			release()
		}
		done <- err
	}()
	<-asked
	hold()
	release, err := g.admitInbound(host, func() bool { return false }) // takes the last token
	if err != nil {
		t.Fatalf("the unfollowed admission was refused: %v", err)
	}
	release()
	answer <- true
	if err := <-done; err != nil {
		t.Fatalf("the followed admission was refused: %v", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if tok := g.hosts[host].tokens; tok < 0 {
		t.Fatalf("bucket overspent: tokens = %v", tok)
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
	done := make(chan struct{})
	go func() {
		_, _ = svc.ResolveKey(context.Background(), "https://peer.example.test/accounts/bob#main-key")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(followedLookupTimeout + 3*time.Second):
		t.Fatalf("the lookup was still running after %v, want about %v", time.Since(start), followedLookupTimeout)
	}
	if got := o.count("peer.example.test"); got != 0 {
		t.Fatalf("a timed-out lookup counted as followed: %d fetches", got)
	}
}

// hostFollowRepo reports every host as instance-followed.
type hostFollowRepo struct{ fakeRepo }

func (hostFollowRepo) HasAcceptedInstanceFollowingForHost(context.Context, string) (bool, error) {
	return true, nil
}

// A follow broader than the exact actor (an instance follow of its host) does
// not lift the inbound limits on a miss.
func TestHostLevelFollowDoesNotLiftInboundLimits(t *testing.T) {
	o := newPerHostOrigin()
	svc := NewService(hostFollowRepo{newRemoteRepo()}, WithFetchClient(&http.Client{Transport: o}))
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.actorFetches.now = func() time.Time { return frozen }
	for i := 0; i < 500; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://followed-host.example.test/accounts/a%d#main-key", i))
	}
	if got := o.count("followed-host.example.test"); got != inboundHostBurst {
		t.Fatalf("500 new keyIds on an instance-followed host caused %d fetches, want %d", got, inboundHostBurst)
	}
}

// Followed actors get past the ordinary limits, but under a ceiling of their own.
func TestFollowedBypassHasItsOwnCeiling(t *testing.T) {
	o := newPerHostOrigin()
	urls := make([]string, 500)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://many.example.test/accounts/a%d", i)
	}
	svc := newBudgetService(o, WithFollowEdgeChecker(edgesFor(urls...)))
	for _, u := range urls {
		_, _ = svc.ResolveKey(context.Background(), u+"#main-key")
	}
	if got := o.count("many.example.test"); got != inboundHostBurst+inboundBypassBurst {
		t.Fatalf("500 followed actors caused %d fetches, want %d (host burst + bypass ceiling)", got, inboundHostBurst+inboundBypassBurst)
	}
}

// A blocked host is refused before admission: it spends no token, and keeps
// getting the blocked-host answer however often it is named.
func TestBlockedHostSpendsNoInboundBudget(t *testing.T) {
	o := newPerHostOrigin()
	repo := newRemoteRepo()
	repo.blockedDomains = map[string]bool{"blocked.example.test": true}
	svc := NewService(repo, WithFetchClient(&http.Client{Transport: o}))
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.actorFetches.now = func() time.Time { return frozen }
	for i := 0; i < 3*inboundHostBurst; i++ {
		_, err := svc.ResolveKey(context.Background(), fmt.Sprintf("https://blocked.example.test/accounts/a%d#main-key", i))
		if !errors.Is(err, ErrActorHostBlocked) {
			t.Fatalf("request %d: %v, want ErrActorHostBlocked", i, err)
		}
	}
	svc.actorFetches.mu.Lock()
	defer svc.actorFetches.mu.Unlock()
	if b, ok := svc.actorFetches.hosts["blocked.example.test"]; ok && b.tokens < inboundHostBurst {
		t.Fatalf("a blocked host spent %v tokens", inboundHostBurst-b.tokens)
	}
}

// Every inbound-path refusal is counted by reason, with a fixed label set.
func TestInboundFetchRefusalsAreCountedByReason(t *testing.T) {
	o := newPerHostOrigin()
	var mu sync.Mutex
	got := map[string]int{}
	count := func(reason string) { mu.Lock(); got[reason]++; mu.Unlock() }
	repo := newRemoteRepo()
	repo.blockedDomains = map[string]bool{"blocked.example.test": true}
	svc := NewService(repo, WithFetchClient(&http.Client{Transport: o}), WithInboundFetchRefusedCounter(count))
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.actorFetches.now = func() time.Time { return frozen }
	ctx := context.Background()

	for i := 0; i < inboundHostBurst+3; i++ {
		_, _ = svc.ResolveKey(ctx, fmt.Sprintf("https://flood.example.test/accounts/a%d#main-key", i))
	}
	// A blocked host is counted as blocked however often it is named, never
	// as a spent budget.
	for i := 0; i < inboundHostBurst+5; i++ {
		_, _ = svc.ResolveKey(ctx, fmt.Sprintf("https://blocked.example.test/accounts/b%d#main-key", i))
	}
	svc.actorFetches.maxInflight = 0
	_, _ = svc.ResolveKey(ctx, "https://quiet.example.test/accounts/bob#main-key")

	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{"host_budget": 3, "blocked_host": inboundHostBurst + 5, "global_cap": 1}
	for reason, n := range want {
		if got[reason] != n {
			t.Errorf("%s refusals = %d, want %d (all: %v)", reason, got[reason], n, got)
		}
	}
}

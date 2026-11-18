package federation

import (
	"context"
	"fmt"
	"testing"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (o *perHostOrigin) total() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, c := range o.hits {
		n += c
	}
	return n
}

// Distinct subdomains of one registrable domain share one burst.
func TestSubdomainsShareTheRegistrableDomainBudget(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	for i := 0; i < 100; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://s%d.shared-a.test/accounts/x#main-key", i))
	}
	if got := o.total(); got != inboundDomainBurst {
		t.Fatalf("100 subdomains of one domain caused %d fetches, want %d", got, inboundDomainBurst)
	}
	_, _ = svc.ResolveKey(context.Background(), "https://www.other-b.test/accounts/x#main-key")
	if got := o.count("www.other-b.test"); got != 1 {
		t.Fatalf("another registrable domain was fetched %d times, want 1", got)
	}
}

// Many unrelated domains are held to the global rate.
func TestUnrelatedDomainsAreHeldToTheGlobalRate(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	for i := 0; i < 200; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://d%d.test/accounts/x#main-key", i))
	}
	if got := o.total(); got != inboundGlobalBurst {
		t.Fatalf("200 unrelated domains caused %d fetches, want %d", got, inboundGlobalBurst)
	}
}

// A followed actor is fetched with its domain's budget and the global rate
// both spent.
func TestFollowedActorBypassesDomainAndGlobalBudgets(t *testing.T) {
	o := newPerHostOrigin()
	followed := "https://chan.shared-a.test/accounts/chan"
	svc := newBudgetService(o, WithFollowEdgeChecker(edgesFor(followed)))
	for i := 0; i < 100; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://s%d.shared-a.test/accounts/x#main-key", i))
	}
	for i := 0; i < 200; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://d%d.test/accounts/x#main-key", i))
	}
	before := o.total()
	_, _ = svc.ResolveKey(context.Background(), followed+"#main-key")
	if got := o.total() - before; got != 1 {
		t.Fatalf("a followed actor was fetched %d times with both budgets spent, want 1", got)
	}
}

// At most inboundDomainMaxInflight fetches run at once per registrable domain.
func TestRegistrableDomainInflightCap(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	hosts := make([]string, inboundDomainMaxInflight)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("h%d.slow-c.test", i)
	}
	done := holdInflight(t, svc, o, hosts...)
	_, _ = svc.ResolveKey(context.Background(), "https://extra.slow-c.test/accounts/x#main-key")
	_, _ = svc.ResolveKey(context.Background(), "https://www.fast-d.test/accounts/x#main-key")
	done()
	if got := o.count("extra.slow-c.test"); got != 0 {
		t.Errorf("a fetch past the domain's in-flight cap ran %d times", got)
	}
	if got := o.count("www.fast-d.test"); got != 1 {
		t.Errorf("another domain was fetched %d times, want 1", got)
	}
}

// IP literals are grouped by range for the registrable-domain limits.
func TestInboundDomainKeyGroupsIPLiteralsByRange(t *testing.T) {
	for in, want := range map[string]string{
		"https://192.0.2.7/accounts/x":          "192.0.2.0/24",
		"https://192.0.2.200:8443/accounts/x":   "192.0.2.0/24",
		"https://[2001:db8:1:2::1]/accounts/x":  "2001:db8:1::/48",
		"https://a.b.shared-a.test/accounts/x":  "shared-a.test",
		"https://Peer.Example.Test./accounts/x": "example.test",
	} {
		if got := inboundDomainKey(in); got != want {
			t.Errorf("inboundDomainKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A key refresh draws on its host's bucket: with the bucket spent, an
// unfollowed actor's refresh fetches nothing and leaves its cooldown unspent;
// and refreshes inside the cooldown spend no token.
func TestKeyRefreshDrawsOnTheHostBucket(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	pemStr, _ := testActorPEM(t)
	repo := svc.repo.(fakeRepo)
	a := "https://busy.example.test/accounts/cached"
	b := "https://calm.example.test/accounts/cached"
	repo.remoteActors[a] = sqlcgen.RemoteActor{ActorUrl: a, PublicKeyPem: pemStr}
	repo.remoteActors[b] = sqlcgen.RemoteActor{ActorUrl: b, PublicKeyPem: pemStr}

	for i := 0; i < inboundHostBurst; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://busy.example.test/accounts/n%d#main-key", i))
	}
	_, _ = svc.ResolveKeyFresh(context.Background(), a+"#main-key")
	if got := o.count("busy.example.test"); got != inboundHostBurst {
		t.Errorf("a refresh on a spent host fetched: %d fetches, want %d", got, inboundHostBurst)
	}
	if !svc.actorFetches.refreshDue(a) {
		t.Error("a refused refresh spent the cooldown")
	}

	for i := 0; i < 50; i++ {
		_, _ = svc.ResolveKeyFresh(context.Background(), b+"#main-key")
	}
	if got := o.count("calm.example.test"); got != 1 {
		t.Errorf("refreshes fetched %d times, want 1", got)
	}
	svc.actorFetches.mu.Lock()
	defer svc.actorFetches.mu.Unlock()
	if tok := svc.actorFetches.hosts["calm.example.test"].tokens; tok != inboundHostBurst-1 {
		t.Errorf("host tokens = %v after one refresh and 49 in cooldown, want %d", tok, inboundHostBurst-1)
	}
}

// Refused requests create no bucket entries.
func TestRefusedMissesCreateNoBuckets(t *testing.T) {
	o := newPerHostOrigin()
	svc := newBudgetService(o)
	for i := 0; i < inboundGlobalBurst; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://d%d.test/accounts/x#main-key", i))
	}
	for i := 0; i < 100; i++ {
		_, _ = svc.ResolveKey(context.Background(), fmt.Sprintf("https://refused%d.test/accounts/x#main-key", i))
	}
	svc.actorFetches.mu.Lock()
	defer svc.actorFetches.mu.Unlock()
	if n := len(svc.actorFetches.hosts); n != inboundGlobalBurst {
		t.Fatalf("host buckets = %d after %d admissions and 100 refusals, want %d", n, inboundGlobalBurst, inboundGlobalBurst)
	}
}

package federation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// blockTable answers BlockReader from sets, the way the SQL does.
type blockTable struct {
	domains, admin, anyone map[string]bool
	owners                 map[uuid.UUID]map[string]bool
}

func (b blockTable) IsInstanceBlocked(_ context.Context, d string) (bool, error) {
	return b.domains[d], nil
}
func (b blockTable) IsRemoteActorBlockedInstanceWide(_ context.Context, a string) (bool, error) {
	return b.admin[a], nil
}
func (b blockTable) IsRemoteActorBlockedByAnyone(_ context.Context, a string) (bool, error) {
	return b.anyone[a], nil
}
func (b blockTable) IsRemoteActorBlockedBy(_ context.Context, p sqlcgen.IsRemoteActorBlockedByParams) (bool, error) {
	return b.owners[p.BlockerID][p.ActorUrl], nil
}

// A follower that arrives by import is refused by exactly what refuses a live
// Follow: its instance, an admin block, or the followed channel's owner — and
// never by somebody else's personal block.
func TestRemoteFollowerRefused(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	blocks := blockTable{
		domains: map[string]bool{"blocked.example": true},
		admin:   map[string]bool{"https://ok.example/users/banned": true},
		anyone:  map[string]bool{"https://ok.example/users/pest": true, "https://ok.example/users/fan": true},
		owners: map[uuid.UUID]map[string]bool{
			owner: {"https://ok.example/users/pest": true},
			other: {"https://ok.example/users/fan": true},
		},
	}
	for actor, want := range map[string]bool{
		"https://Blocked.example/users/anyone": true,
		"https://ok.example/users/banned":      true,
		"https://ok.example/users/pest":        true,
		"https://ok.example/users/fan":         false,
		"https://ok.example/users/stranger":    false,
	} {
		got, err := RemoteFollowerRefused(context.Background(), blocks, owner, actor)
		if err != nil || got != want {
			t.Errorf("RemoteFollowerRefused(%s) = %v, %v; want %v", actor, got, err, want)
		}
	}
}

// The importer's resolver is federation's own: it fetches an uncached actor
// through the SSRF guard (refusing a loopback origin unless allowed), caches the
// document's inbox and key, and answers a second call from the cache.
func TestResolveRemoteActorFetchesThroughTheGuard(t *testing.T) {
	srv, hits := actorServer(t, mustPEM(t))
	actor := srv.URL + "/accounts/bob"
	if _, err := NewService(newRemoteRepo()).ResolveRemoteActor(context.Background(), actor); err == nil || *hits != 0 {
		t.Fatalf("guarded resolve of a loopback actor: err %v, %d fetches; want refused unfetched", err, *hits)
	}
	repo := newRemoteRepo()
	svc := NewService(repo, WithAllowPrivateFetch(true))
	for i := 0; i < 2; i++ {
		ra, err := svc.ResolveRemoteActor(context.Background(), actor)
		if err != nil || ra.InboxUrl != actor+"/inbox" || ra.PublicKeyPem == "" {
			t.Fatalf("resolve %d = %+v, %v", i, ra, err)
		}
	}
	if *hits != 1 {
		t.Errorf("origin fetched %d times, want 1 (then cached)", *hits)
	}
}

func TestInboxesOnActorHost(t *testing.T) {
	str := func(s string) *string { return &s }
	for name, tc := range map[string]struct {
		ra   sqlcgen.RemoteActor
		want bool
	}{
		"both on the actor's host": {sqlcgen.RemoteActor{ActorUrl: "https://a.example/u/x", InboxUrl: "https://a.example/u/x/inbox", SharedInboxUrl: str("https://A.example/inbox")}, true},
		"no shared inbox":          {sqlcgen.RemoteActor{ActorUrl: "https://a.example/u/x", InboxUrl: "https://a.example/u/x/inbox"}, true},
		"inbox elsewhere":          {sqlcgen.RemoteActor{ActorUrl: "https://a.example/u/x", InboxUrl: "https://other.example/inbox"}, false},
		"shared inbox elsewhere":   {sqlcgen.RemoteActor{ActorUrl: "https://a.example/u/x", InboxUrl: "https://a.example/i", SharedInboxUrl: str("https://other.example/inbox")}, false},
		"no inbox":                 {sqlcgen.RemoteActor{ActorUrl: "https://a.example/u/x"}, false},
	} {
		if got := InboxesOnActorHost(tc.ra); got != tc.want {
			t.Errorf("%s: InboxesOnActorHost = %v, want %v", name, got, tc.want)
		}
	}
}

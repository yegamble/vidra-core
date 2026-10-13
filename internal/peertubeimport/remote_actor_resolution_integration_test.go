//go:build integration

package peertubeimport

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeActorResolver stands in for federation's resolver: it "fetches" fixed
// actor documents and caches them, as federation does.
type fakeActorResolver struct {
	dest  *pgxpool.Pool
	docs  map[string]sqlcgen.UpsertRemoteActorParams
	calls []string
}

func (f *fakeActorResolver) resolve(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	f.calls = append(f.calls, actorURL)
	doc, ok := f.docs[actorURL]
	if !ok {
		return sqlcgen.RemoteActor{}, errors.New("actor fetch returned status 410")
	}
	q := sqlcgen.New(f.dest)
	if err := q.UpsertRemoteActor(ctx, doc); err != nil {
		return sqlcgen.RemoteActor{}, err
	}
	return q.GetRemoteActor(ctx, actorURL)
}

// A follower's actor comes from this instance's cache or from the resolver,
// within one run's fetch budget, and a host that failed once is not fetched
// again in the same run.
func TestImporterResolvesFollowerActorsThroughFederation(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	mustExec(t, ctx, dest, `INSERT INTO remote_actors (actor_url, actor_type, domain, inbox_url, public_key_pem)
		VALUES ('https://cached.example/u/a','Person','cached.example','https://cached.example/u/a/inbox','CACHED')`)
	resolver := &fakeActorResolver{dest: dest, docs: map[string]sqlcgen.UpsertRemoteActorParams{
		"https://live.example/u/a": {ActorUrl: "https://live.example/u/a", ActorType: "Person", Domain: "live.example",
			InboxUrl: "https://live.example/u/a/inbox", PublicKeyPem: "FETCHED"},
	}}
	imp := NewImporter(dest, nil, Options{ResolveRemoteActor: resolver.resolve})
	budget := &actorFetches{left: 3, dead: map[string]bool{}}

	for _, tc := range []struct{ actor, wantKey, wantSkip string }{
		{"https://cached.example/u/a", "CACHED", ""},
		{"https://live.example/u/a", "FETCHED", ""},
		{"https://live.example/u/a", "FETCHED", ""}, // now cached: no second fetch
		{"https://dead.example/u/a", "", followerSkipUnresolved},
		{"https://dead.example/u/b", "", followerSkipUnresolved}, // same dead host: not fetched
		{"https://other.example/u/a", "", followerSkipUnresolved},
		{"https://more.example/u/a", "", followerSkipDeferred}, // budget of 3 spent
	} {
		ra, skip, err := imp.followerActor(ctx, tc.actor, budget)
		if err != nil || skip != tc.wantSkip || ra.PublicKeyPem != tc.wantKey {
			t.Errorf("followerActor(%s) = key %q, skip %q, err %v; want key %q, skip %q", tc.actor, ra.PublicKeyPem, skip, err, tc.wantKey, tc.wantSkip)
		}
	}
	if got := strings.Join(resolver.calls, " "); got != "https://live.example/u/a https://dead.example/u/a https://other.example/u/a" {
		t.Errorf("fetches = %s", got)
	}
}

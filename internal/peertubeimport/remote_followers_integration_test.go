//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A migrated instance keeps its fediverse audience: a remote follow of a local
// channel arrives accepted, with the follower's inbox and key taken from its
// OWN actor (never the source's copy), every block honoured before anything is
// fetched, an account follow reported, and a re-run a no-op.
func TestPeerTubeImportCarriesRemoteFollowers(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
	for _, s := range []string{
		`UPDATE actor SET url='https://pt.example/video-channels/alice_channel' WHERE id=2`,
		`UPDATE actor SET url='https://old.example/video-channels/bob_channel' WHERE id=4`, // followed on ANOTHER domain
		// The source also holds a key for "remote" (actor 5's publicKey); only the
		// resolved actor's key may be cached here.
		`UPDATE actor SET url='https://remote.example/accounts/remote' WHERE id=5`,
		`INSERT INTO actor (id,type,"preferredUsername",url,"publicKey","serverId") VALUES
			(10,'Person','troll','https://blocked.example/users/troll','K',2),
			(11,'Person','fan','https://mastodon.example/users/fan','K',3),
			(13,'Person','gone','https://dead.example/users/gone','K',4),
			(14,'Person','pest','https://mastodon.example/users/pest','K',3),
			(15,'Person','gone2','https://dead.example/users/gone2','K',4),
			(16,'Person','split','https://split.example/users/split','K',5)`,
		`INSERT INTO "actorFollow" (id,state,"actorId","targetActorId",url) VALUES
			(20,'accepted',5,2,'https://remote.example/follows/20'),
			(21,'accepted',11,2,'https://mastodon.example/follows/21'),
			(22,'accepted',11,1,NULL),
			(23,'accepted',10,2,NULL),
			(25,'accepted',13,2,NULL),
			(26,'accepted',14,2,NULL),
			(27,'accepted',5,4,NULL),
			(28,'accepted',15,2,NULL),
			(29,'accepted',16,2,NULL)`,
	} {
		mustExec(t, ctx, src, s)
	}
	mustExec(t, ctx, dest, `INSERT INTO blocked_instances (domain) VALUES ('blocked.example')`)
	// fan is already cached here (federation fetched it itself): no fetch needed.
	mustExec(t, ctx, dest, `INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, inbox_url, public_key_pem)
		VALUES ('https://mastodon.example/users/fan','Person','fan','mastodon.example','https://mastodon.example/users/fan/inbox','FAN-KEY')`)
	shared := "https://remote.example/inbox"
	resolver := &fakeActorResolver{dest: dest, docs: map[string]sqlcgen.UpsertRemoteActorParams{
		"https://remote.example/accounts/remote": {ActorUrl: "https://remote.example/accounts/remote", ActorType: "Person", Domain: "remote.example",
			InboxUrl: "https://remote.example/accounts/remote/inbox", SharedInboxUrl: &shared, PublicKeyPem: "FETCHED-KEY"},
		"https://split.example/users/split": {ActorUrl: "https://split.example/users/split", ActorType: "Person", Domain: "split.example",
			InboxUrl: "https://other.example/inbox", PublicKeyPem: "SPLIT-KEY"},
	}}

	run := func(baseURL string, budget int) *Report {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone,
			PublicBaseURL: baseURL, ResolveRemoteActor: resolver.resolve})
		imp.actorFetchBudget = budget
		version, err := imp.Preflight(ctx)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		r, err := imp.Run(ctx, version, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return r
	}

	// Without this instance's origin nothing is carried or fetched.
	if first := run("", maxActorFetchesPerRun); countRows(t, ctx, dest, "remote_follows") != 0 || len(resolver.calls) != 0 {
		t.Fatalf("a run with no base URL carried or fetched followers: %v", first.Deferred)
	}
	mustExec(t, ctx, dest, `INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url)
		SELECT id, 'https://mastodon.example/users/pest' FROM users WHERE username='alice'`)

	second := run("https://pt.example/", maxActorFetchesPerRun)
	if got := second.Entities[KindRemoteFollower]; got.Imported != 2 || got.Skipped != 6 || got.Unsupported != 1 || got.Failed != 0 {
		t.Errorf("remote_follower counts = %+v, want imported 2 (remote, fan), skipped 6 (blocked instance, owner block, 2 unresolvable, foreign inbox, other domain), unsupported 1 (account)", *got)
	}
	// Blocked followers and the moved channel's follower were never fetched, and
	// the dead host cost one fetch for its two followers.
	if got := strings.Join(resolver.calls, " "); got != "https://remote.example/accounts/remote https://dead.example/users/gone https://split.example/users/split" {
		t.Errorf("fetches = %s", got)
	}
	conflicts := strings.Join(second.Conflicts, "\n")
	for _, want := range []string{"1 remote follower(s) not carried: the channel's actor id", "2 remote follower(s) not carried: the follower is blocked",
		"2 remote follower(s) not carried: the follower's actor could not be fetched", "1 remote follower(s) not carried: the follower's actor names an inbox on another server",
		"1 remote follower(s) of ACCOUNTS"} {
		if !strings.Contains(conflicts, want) {
			t.Errorf("conflicts lack %q:\n%s", want, conflicts)
		}
	}

	followers := scanStrings(t, ctx, dest, `SELECT remote_actor_url || ' ' || state || ' ' || follow_activity_url FROM remote_follows ORDER BY 1`)
	if strings.Join(followers, "|") != "https://mastodon.example/users/fan accepted https://mastodon.example/follows/21|"+
		"https://remote.example/accounts/remote accepted https://remote.example/follows/20" {
		t.Errorf("remote followers = %v, want fan + remote on alice_channel only", followers)
	}
	// The key and endpoints this instance holds are the fetched ones; the
	// source's "PUBKEY-R" for remote is nowhere.
	if keys := scanStrings(t, ctx, dest, `SELECT actor_url || ' ' || public_key_pem FROM remote_actors WHERE public_key_pem = 'PUBKEY-R' OR actor_url LIKE '%remote.example%'`); strings.Join(keys, "|") != "https://remote.example/accounts/remote FETCHED-KEY" {
		t.Errorf("cached keys = %v, want only the fetched one", keys)
	}

	// The next publish fans out to them.
	inboxes, err := sqlcgen.New(dest).ListRemoteFollowerInboxes(ctx, readChannelIDByHandle(t, ctx, dest, "alice_channel"))
	sort.Strings(inboxes)
	if err != nil || strings.Join(inboxes, "|") != "https://mastodon.example/users/fan/inbox|https://remote.example/inbox" {
		t.Errorf("delivery fan-out = %v (%v)", inboxes, err)
	}

	// A re-run is a sync, not a second copy; with the fetch budget spent the
	// uncached followers wait for the next run instead of being fetched.
	resolver.calls = nil
	third := run("https://pt.example", 0)
	if got := third.Entities[KindRemoteFollower]; got.Imported != 0 || got.Skipped != 8 || got.Failed != 0 || len(resolver.calls) != 0 {
		t.Errorf("re-run remote_follower counts = %+v after %d fetches, want imported 0, skipped 8, no fetch", *got, len(resolver.calls))
	}
	if !strings.Contains(strings.Join(third.Conflicts, "\n"), "2 remote follower(s) not carried: this run's actor-fetch budget was spent") {
		t.Errorf("the spent budget is not reported: %v", third.Conflicts)
	}
}

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
// channel arrives accepted with a cached actor to deliver to, every block here
// is honoured, an account follow is reported, and a re-run is a no-op.
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
		`INSERT INTO server (id,host) VALUES (1,'remote.example'),(2,'blocked.example'),(3,'Mastodon.example')`,
		`UPDATE actor SET url='https://pt.example/video-channels/alice_channel' WHERE id=2`,
		`UPDATE actor SET url='https://old.example/video-channels/bob_channel' WHERE id=4`, // followed on ANOTHER domain
		`UPDATE actor SET url='https://remote.example/accounts/remote', "inboxUrl"='https://remote.example/accounts/remote/inbox',
			"sharedInboxUrl"='https://remote.example/inbox', "followersUrl"='https://remote.example/accounts/remote/followers' WHERE id=5`,
		`INSERT INTO actor (id,type,"preferredUsername",url,"publicKey","serverId","inboxUrl","sharedInboxUrl") VALUES
			(10,'Person','troll','https://blocked.example/users/troll','PUBKEY-T',2,'https://blocked.example/users/troll/inbox',NULL),
			(11,'Person','fan','https://mastodon.example/users/fan','PUBKEY-F',3,'https://mastodon.example/users/fan/inbox','https://mastodon.example/inbox'),
			(13,'Person','nokey','https://mastodon.example/users/nokey',NULL,3,'https://mastodon.example/users/nokey/inbox',NULL),
			(14,'Person','pest','https://mastodon.example/users/pest','PUBKEY-P',3,'https://mastodon.example/users/pest/inbox',NULL)`,
		`INSERT INTO "actorFollow" (id,state,"actorId","targetActorId",url) VALUES
			(20,'accepted',5,2,'https://remote.example/follows/20'),
			(21,'accepted',11,2,'https://mastodon.example/follows/21'),
			(22,'accepted',11,1,'https://mastodon.example/follows/22'),
			(23,'accepted',10,2,NULL),
			(25,'accepted',13,2,NULL),
			(26,'accepted',14,2,NULL),
			(27,'accepted',5,4,NULL)`,
	} {
		mustExec(t, ctx, src, s)
	}
	mustExec(t, ctx, dest, `INSERT INTO blocked_instances (domain) VALUES ('blocked.example')`)
	// fan is already cached here (fetched by federation itself).
	mustExec(t, ctx, dest, `INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, inbox_url, public_key_pem)
		VALUES ('https://mastodon.example/users/fan','Person','fan','mastodon.example','https://mastodon.example/users/fan/inbox-FRESH','PUBKEY-FRESH')`)

	run := func(baseURL string) *Report {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone, PublicBaseURL: baseURL})
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

	// Without this instance's origin nothing can be checked, so nothing is carried.
	if first := run(""); countRows(t, ctx, dest, "remote_follows") != 0 ||
		!strings.Contains(strings.Join(first.Deferred, "\n"), "remote (fediverse) followers") {
		t.Fatalf("a run with no base URL carried followers or did not say so: %v", first.Deferred)
	}

	// alice blocks pest on this instance between the runs.
	mustExec(t, ctx, dest, `INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url)
		SELECT id, 'https://mastodon.example/users/pest' FROM users WHERE username='alice'`)

	second := run("https://pt.example/")
	if got := second.Entities[KindRemoteFollower]; got.Imported != 2 || got.Skipped != 4 || got.Unsupported != 1 || got.Failed != 0 {
		t.Errorf("remote_follower counts = %+v, want imported 2 (remote, fan), skipped 4 (blocked instance, owner block, no key, other domain), unsupported 1 (account)", *got)
	}
	conflicts := strings.Join(second.Conflicts, "\n")
	for _, want := range []string{"1 remote follower(s) not carried: the channel's actor id", "2 remote follower(s) not carried: the follower is blocked", "1 remote follower(s) not carried: the source holds no usable", "1 remote follower(s) of ACCOUNTS"} {
		if !strings.Contains(conflicts, want) {
			t.Errorf("conflicts lack %q:\n%s", want, conflicts)
		}
	}

	alice := readChannelIDByHandle(t, ctx, dest, "alice_channel")
	followers := scanStrings(t, ctx, dest, `SELECT remote_actor_url || ' ' || state || ' ' || follow_activity_url FROM remote_follows ORDER BY 1`)
	if strings.Join(followers, "|") != "https://mastodon.example/users/fan accepted https://mastodon.example/follows/21|"+
		"https://remote.example/accounts/remote accepted https://remote.example/follows/20" {
		t.Errorf("remote followers = %v, want fan + remote on alice_channel only", followers)
	}

	// Only the carried followers are cached, and fan keeps the copy this
	// instance fetched itself rather than the source's older one.
	actors := scanStrings(t, ctx, dest, `SELECT actor_url || ' ' || domain || ' ' || inbox_url || ' ' ||
		COALESCE(shared_inbox_url, '-') || ' ' || public_key_pem FROM remote_actors ORDER BY 1`)
	if strings.Join(actors, "|") != "https://mastodon.example/users/fan mastodon.example https://mastodon.example/users/fan/inbox-FRESH - PUBKEY-FRESH|"+
		"https://remote.example/accounts/remote remote.example https://remote.example/accounts/remote/inbox https://remote.example/inbox PUBKEY-R" {
		t.Errorf("remote_actors = %v", actors)
	}

	// What makes the follow worth carrying: the next publish fans out to them,
	// and the creator sees them counted.
	q := sqlcgen.New(dest)
	inboxes, err := q.ListRemoteFollowerInboxes(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(inboxes)
	if strings.Join(inboxes, "|") != "https://mastodon.example/users/fan/inbox-FRESH|https://remote.example/inbox" {
		t.Errorf("delivery fan-out = %v", inboxes)
	}
	if n, err := q.CountChannelFollowers(ctx, alice); err != nil || n != 3 {
		t.Errorf("alice_channel followers = %d (%v), want 3 (bob locally + 2 remote)", n, err)
	}

	// A re-run is a sync, not a second copy.
	third := run("https://pt.example")
	if got := third.Entities[KindRemoteFollower]; got.Imported != 0 || got.Skipped != 6 || got.Failed != 0 {
		t.Errorf("re-run remote_follower counts = %+v, want imported 0, skipped 6", *got)
	}
	if n := countRows(t, ctx, dest, "remote_follows"); n != 2 {
		t.Errorf("remote_follows after a re-run = %d, want 2", n)
	}
}

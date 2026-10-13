//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"
)

// The remote-follower read returns accepted follows of local channels AND
// accounts by remote actors, and nothing else: not a local subscription, not a
// pending follow, not another instance following the source's server actor.
// It carries IDs only — never the source's copy of the follower's endpoints or
// key (the fixture's actor 5 has a publicKey; nothing returns it).
// Both actor-link layouts (account/channel → actor, and the 1040 actor → owner
// columns) read the same rows.
func TestPeerTubeSourceRemoteFollowers(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	seedPeerTube(t, ctx, src, "x", secretPrivKeyAlice)
	for _, s := range []string{
		`UPDATE actor SET url='https://pt.example/video-channels/alice_channel' WHERE id=2`,
		`UPDATE actor SET url='https://remote.example/accounts/remote' WHERE id=5`,
		// The source's own server actor: an Application whose account has no user.
		`INSERT INTO actor (id,type,"preferredUsername",url,"serverId") VALUES (30,'Application','peertube','https://pt.example/accounts/peertube',NULL)`,
		`INSERT INTO account (id,name,"userId","actorId") VALUES (30,'peertube',NULL,30)`,
		`INSERT INTO "actorFollow" (id,state,"actorId","targetActorId",url) VALUES
			(20,'accepted',5,2,'https://remote.example/follows/20'),
			(21,'accepted',5,1,NULL),
			(22,'pending',5,4,NULL),
			(23,'accepted',5,30,NULL)`,
	} {
		mustExec(t, ctx, src, s)
	}
	check := func(layout string) {
		t.Helper()
		got, err := NewSourceFromPool(src).RemoteFollowers(ctx)
		if err != nil {
			t.Fatalf("%s: %v", layout, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: read %d follows, want 2 (channel + account): %+v", layout, len(got), got)
		}
		want := SourceRemoteFollower{ChannelID: 1, TargetURL: "https://pt.example/video-channels/alice_channel",
			FollowURL: "https://remote.example/follows/20", ActorURL: "https://remote.example/accounts/remote"}
		if got[0] != want {
			t.Errorf("%s: channel follow = %+v, want %+v", layout, got[0], want)
		}
		if got[1].ChannelID != 0 || got[1].ActorURL != want.ActorURL || got[1].FollowURL != "" {
			t.Errorf("%s: account follow = %+v, want ChannelID 0", layout, got[1])
		}
	}
	check("actor ids on account/channel")
	for _, s := range []string{
		`ALTER TABLE actor ADD COLUMN "accountId" integer, ADD COLUMN "videoChannelId" integer`,
		`UPDATE actor SET "accountId"=a.id FROM account a WHERE a."actorId"=actor.id`,
		`UPDATE actor SET "videoChannelId"=c.id FROM "videoChannel" c WHERE c."actorId"=actor.id`,
		`ALTER TABLE account DROP COLUMN "actorId"`,
		`ALTER TABLE "videoChannel" DROP COLUMN "actorId"`,
	} {
		mustExec(t, ctx, src, s)
	}
	check("owner ids on actor")
}

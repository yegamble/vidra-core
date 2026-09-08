package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// One handle namespace, as the fediverse sees it (A29 parity, migration 0142).
//
// The rehearsal's finding: `federation.WebFinger` resolved a name to the USER
// first and stopped, so on an instance where an account and a channel shared a
// handle every channel-scoped feature keyed on it silently addressed the Person
// — a remote Follow was dropped with no Reject, and a viewer's block of
// `@name@domain` stored the Person url while the videos are attributed to the
// Group, hiding nothing.

// collidingRepo is the pre-0142 world the aliases exist for: an account `ownera`
// and the channel that used to share its name, now renamed and still answering
// under the old one.
func collidingRepo() fakeRepo {
	uid, cid := uuid.New(), uuid.New()
	return fakeRepo{
		usersByName: map[string]sqlcgen.GetUserActorByUsernameRow{
			"ownera": {ID: uid, Username: "ownera", DisplayName: "Owner A"},
		},
		usersByID: map[uuid.UUID]sqlcgen.GetUserActorByIDRow{
			uid: {ID: uid, Username: "ownera", DisplayName: "Owner A"},
		},
		channels: map[string]sqlcgen.Channel{
			"ownera-channel": {ID: cid, OwnerID: uid, Handle: "ownera-channel", DisplayName: "Owner A's channel"},
		},
		channelsByID: map[uuid.UUID]sqlcgen.Channel{
			cid: {ID: cid, OwnerID: uid, Handle: "ownera-channel", DisplayName: "Owner A's channel"},
		},
		channelAliases: map[string]uuid.UUID{"ownera": cid},
		actorAliases:   map[uuid.UUID]string{cid: "ownera"},
		acctKeys:       map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		chanKeys:       map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
	}
}

// TestWebFingerAnswersBothActorsForOneName is SC2. A peer asking for the
// contested name gets the Person AND the Group, each with the media type that
// lets it pick the one it wants: a Mastodon-shaped client following a person,
// and a PeerTube-shaped client following a channel, both get the right actor
// from the same answer.
func TestWebFingerAnswersBothActorsForOneName(t *testing.T) {
	svc := NewService(collidingRepo(), WithBaseURL("https://videos.example"))

	jrd, err := svc.WebFinger(context.Background(), "acct:ownera@videos.example")
	if err != nil {
		t.Fatalf("WebFinger: %v", err)
	}
	if len(jrd.Links) != 2 {
		t.Fatalf("a name held by an account AND aliased to a channel must answer with both links, got %+v", jrd.Links)
	}
	self, alternate := jrd.Links[0], jrd.Links[1]
	if self.Rel != "self" || self.Href != "https://videos.example/accounts/ownera" {
		t.Errorf("the Person must be the self link: %+v", self)
	}
	if alternate.Rel != "alternate" || alternate.Href != "https://videos.example/video-channels/ownera" {
		t.Errorf("the Group must be an alternate link at its FROZEN id: %+v", alternate)
	}
	for _, l := range jrd.Links {
		if l.Type != "application/activity+json" {
			t.Errorf("every actor link carries the AP media type, got %q on %+v", l.Type, l)
		}
	}
}

// TestWebFingerKeepsTheSingleLinkShapeWhenOnlyOneActorHoldsTheName is the other
// half of the same promise: nothing changes for the overwhelmingly common case,
// and a channel-only name still answers as `self` so a peer that understands
// only `self` is unaffected.
func TestWebFingerKeepsTheSingleLinkShapeWhenOnlyOneActorHoldsTheName(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))

	acct, err := svc.WebFinger(context.Background(), "acct:ada@videos.example")
	if err != nil {
		t.Fatalf("WebFinger account: %v", err)
	}
	if len(acct.Links) != 1 || acct.Links[0].Rel != "self" ||
		acct.Links[0].Href != "https://videos.example/accounts/ada" {
		t.Fatalf("account-only name: %+v", acct.Links)
	}

	ch, err := svc.WebFinger(context.Background(), "acct:films@videos.example")
	if err != nil {
		t.Fatalf("WebFinger channel: %v", err)
	}
	if len(ch.Links) != 1 || ch.Links[0].Rel != "self" ||
		ch.Links[0].Href != "https://videos.example/video-channels/films" {
		t.Fatalf("channel-only name: %+v", ch.Links)
	}
}

// TestRenamedChannelKeepsItsActivityPubIdentity is the rule the rename turns on.
// Peers hold `…/video-channels/ownera` in their follow rows, in their cached
// actors and in every activity's attributedTo; if a rename moved the id, every
// one of those references would point at an actor that no longer exists. So the
// id stays and only preferredUsername moves — and the OLD name still resolves,
// because that is the address the existing followers use.
func TestRenamedChannelKeepsItsActivityPubIdentity(t *testing.T) {
	svc := NewService(collidingRepo(), WithBaseURL("https://videos.example"))

	for _, name := range []string{"ownera", "ownera-channel"} {
		actor, err := svc.ChannelActor(context.Background(), name)
		if err != nil {
			t.Fatalf("ChannelActor(%q): %v", name, err)
		}
		if actor.ID != "https://videos.example/video-channels/ownera" {
			t.Errorf("ChannelActor(%q).id = %q, want the frozen id", name, actor.ID)
		}
		if actor.Inbox != "https://videos.example/video-channels/ownera/inbox" ||
			actor.Followers != "https://videos.example/video-channels/ownera/followers" ||
			actor.PublicKey.ID != "https://videos.example/video-channels/ownera#main-key" {
			t.Errorf("ChannelActor(%q): every id-derived field must follow the frozen id, got inbox=%q followers=%q key=%q",
				name, actor.Inbox, actor.Followers, actor.PublicKey.ID)
		}
		if actor.PreferredUsername != "ownera-channel" {
			t.Errorf("ChannelActor(%q).preferredUsername = %q, want the CURRENT handle", name, actor.PreferredUsername)
		}
		// The owning account is still named, and it is the account — the Group's
		// attributedTo is what a block of an account travels down.
		if len(actor.AttributedTo) != 1 || actor.AttributedTo[0].ID != "https://videos.example/accounts/ownera" {
			t.Errorf("ChannelActor(%q).attributedTo = %+v", name, actor.AttributedTo)
		}
	}

	if _, err := svc.ChannelActor(context.Background(), "never-existed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown handle must still be ErrNotFound, got %v", err)
	}
}

// A29 rehearsal 3, SC1. The frozen actor id is 0142's promise to other servers:
// after the backfill renames a colliding channel, `…/video-channels/<old>` is
// the ONLY address existing peers hold for it, and it is what this instance
// keeps serving as the actor's id, inbox, outbox, followers and key id.
//
// The lab followed that promise one step further than the parity slice's tests
// did — B resolved the Group, delivered a Follow to the frozen inbox, got its
// 202, and A recorded nothing and Accepted nothing. Every inbound arm except the
// actor document itself resolved the handle with a plain GetChannelByHandle, so
// the id this instance advertises addressed a channel none of them could find.
func TestInboundActivityResolvesTheFrozenActorID(t *testing.T) {
	const (
		follower = "https://remote.example/accounts/kaisa"
		frozen   = "https://videos.example/video-channels/ownera"
		followAt = "https://remote.example/activities/follow/9"
	)
	newRepo := func() fakeRepo {
		r := collidingRepo()
		r.processed = map[string]bool{}
		r.remoteFollows = map[string]*fakeRemoteFollow{}
		r.followBacks = map[string]*fakeFollowBack{}
		r.rcFollows = map[uuid.UUID]*sqlcgen.RemoteChannelFollow{}
		r.deliveries = map[uuid.UUID]*fakeDelivery{}
		r.remoteActors = map[string]sqlcgen.RemoteActor{
			follower: {ActorUrl: follower, InboxUrl: follower + "/inbox"},
		}
		return r
	}
	channelID := func(r fakeRepo) uuid.UUID { return r.channels["ownera-channel"].ID }

	t.Run("a Follow addressed at the frozen id is recorded and Accepted", func(t *testing.T) {
		repo := newRepo()
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		body := `{"id":"` + followAt + `","type":"Follow","actor":"` + follower + `","object":"` + frozen + `"}`
		if err := svc.HandleInbox(context.Background(), follower, []byte(body)); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		if _, ok := repo.remoteFollows[channelID(repo).String()+"|"+follower]; !ok {
			t.Fatalf("no remote follow recorded for the frozen id: %+v", repo.remoteFollows)
		}
		if len(repo.deliveries) != 1 {
			t.Fatalf("deliveries = %d, want the Accept", len(repo.deliveries))
		}
		// The Accept is signed as the actor the peer addressed — the frozen id
		// — not as the channel's current handle, whose key id no peer holds.
		for _, d := range repo.deliveries {
			if d.row.SigningChannelHandle != "ownera" {
				t.Errorf("Accept signed as %q, want the frozen handle ownera", d.row.SigningChannelHandle)
			}
		}
	})

	t.Run("an Undo{Follow} at the frozen id removes the row", func(t *testing.T) {
		repo := newRepo()
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		body := `{"id":"` + followAt + `","type":"Follow","actor":"` + follower + `","object":"` + frozen + `"}`
		if err := svc.HandleInbox(context.Background(), follower, []byte(body)); err != nil {
			t.Fatalf("HandleInbox follow: %v", err)
		}
		undo := `{"id":"https://remote.example/activities/undo/9","type":"Undo","actor":"` + follower +
			`","object":{"type":"Follow","actor":"` + follower + `","object":"` + frozen + `"}}`
		if err := svc.HandleInbox(context.Background(), follower, []byte(undo)); err != nil {
			t.Fatalf("HandleInbox undo: %v", err)
		}
		if _, ok := repo.remoteFollows[channelID(repo).String()+"|"+follower]; ok {
			t.Error("the follow survived an Undo addressed at the frozen id")
		}
	})

	t.Run("the collections answer at the frozen id, and name it", func(t *testing.T) {
		repo := newRepo()
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		col, err := svc.ChannelCollection(context.Background(), "ownera", "followers")
		if err != nil {
			t.Fatalf("ChannelCollection(ownera, followers): %v", err)
		}
		if col.ID != frozen+"/followers" {
			t.Errorf("collection id = %q, want %q — it must match the id the actor document advertises", col.ID, frozen+"/followers")
		}
		page, err := svc.ChannelOutboxPage(context.Background(), "ownera", 1)
		if err != nil {
			t.Fatalf("ChannelOutboxPage(ownera): %v", err)
		}
		if page.PartOf != frozen+"/outbox" {
			t.Errorf("outbox page partOf = %q, want %q", page.PartOf, frozen+"/outbox")
		}
	})
}

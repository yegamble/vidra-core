package federation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// backfillRepo is the inbox fixture plus n videos on the films channel and a
// cached follower actor. followerInbox lets a test point the inbox elsewhere.
func backfillRepo(t *testing.T, n int, followerInbox string, shared *string) fakeRepo {
	t.Helper()
	repo := newInboxRepo()
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	repo.videosByID = map[uuid.UUID]sqlcgen.GetVideoByIDRow{}
	repo.backfillIDs = map[uuid.UUID][]uuid.UUID{}
	repo.backfillClaims = map[string]bool{}
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		remoteBob: {ActorUrl: remoteBob, InboxUrl: followerInbox, SharedInboxUrl: shared},
	}
	cid := repo.channels["films"].ID
	for i := 0; i < n; i++ { // newest first, as the query returns them
		id := uuid.New()
		repo.videosByID[id] = sqlcgen.GetVideoByIDRow{ID: id, ChannelID: cid, Title: "v", Privacy: "public", State: "published"}
		repo.backfillIDs[cid] = append(repo.backfillIDs[cid], id)
	}
	return repo
}

func deliveriesOfType(repo fakeRepo, typ string) []*fakeDelivery {
	var out []*fakeDelivery
	for _, d := range repo.deliveries {
		var env struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(d.row.Payload, &env) == nil && env.Type == typ {
			out = append(out, d)
		}
	}
	return out
}

func follow(t *testing.T, repo fakeRepo, activityID string) {
	t.Helper()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	body := `{"id":"` + activityID + `","type":"Follow","actor":"` + remoteBob + `","object":"` + filmsActor + `"}`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
}

func TestChannelBackfillSendsAtMostTwentyToTheFollower(t *testing.T) {
	repo := backfillRepo(t, 30, "https://remote.example/accounts/bob/inbox", nil)
	follow(t, repo, followID)
	creates := deliveriesOfType(repo, "Create")
	if len(creates) != channelBackfillLimit {
		t.Fatalf("Create deliveries = %d, want %d", len(creates), channelBackfillLimit)
	}
	for _, d := range creates {
		if d.row.InboxUrl != "https://remote.example/accounts/bob/inbox" {
			t.Errorf("inbox = %q", d.row.InboxUrl)
		}
		if !d.row.SigningChannelID.Valid || d.row.SigningChannelHandle != "films" {
			t.Errorf("not signed as the channel: %+v", d.row)
		}
	}
	if got := len(deliveriesOfType(repo, "Accept")); got != 1 {
		t.Errorf("Accept deliveries = %d, want 1", got)
	}
}

func TestChannelBackfillCooldownHoldsPerHost(t *testing.T) {
	repo := backfillRepo(t, 3, "https://remote.example/accounts/bob/inbox", nil)
	follow(t, repo, followID)
	if got := len(deliveriesOfType(repo, "Create")); got != 3 {
		t.Fatalf("first follow Creates = %d, want 3", got)
	}
	// Undo-and-refollow (a fresh Follow id) inside the window: accepted, answered, not backfilled.
	follow(t, repo, followID+"-2")
	if got := len(deliveriesOfType(repo, "Create")); got != 3 {
		t.Errorf("Creates after a re-follow = %d, want still 3 (cooldown)", got)
	}
	if got := len(deliveriesOfType(repo, "Accept")); got != 2 {
		t.Errorf("Accepts = %d, want 2 (the re-follow is still answered)", got)
	}
}

func TestChannelBackfillNothingToSendKeepsTheCooldown(t *testing.T) {
	repo := backfillRepo(t, 0, "https://remote.example/accounts/bob/inbox", nil)
	follow(t, repo, followID)
	if len(repo.backfillClaims) != 0 {
		t.Errorf("an empty backfill burned the host's cooldown: %v", repo.backfillClaims)
	}
}

func TestChannelBackfillRefusesAnInboxOnAnotherHost(t *testing.T) {
	for name, tc := range map[string]struct {
		inbox  string
		shared *string
	}{
		"inbox elsewhere":        {"https://evil.example/inbox", nil},
		"shared inbox elsewhere": {"https://remote.example/inbox", ptr("https://evil.example/shared")},
		"userinfo trick":         {"https://remote.example@evil.example/inbox", nil},
	} {
		t.Run(name, func(t *testing.T) {
			repo := backfillRepo(t, 5, tc.inbox, tc.shared)
			follow(t, repo, followID)
			if got := len(deliveriesOfType(repo, "Create")); got != 0 {
				t.Errorf("Creates = %d, want 0 for a follower whose inbox is on another host", got)
			}
			if len(repo.backfillClaims) != 0 {
				t.Errorf("a refused backfill burned the cooldown: %v", repo.backfillClaims)
			}
		})
	}
}

func TestChannelBackfillSkipsABlockedInstance(t *testing.T) {
	repo := backfillRepo(t, 5, "https://remote.example/accounts/bob/inbox", nil)
	repo.blockedDomains = map[string]bool{"remote.example": true}
	// A blocked instance's Follow is dropped before the Accept; call the
	// backfill directly to prove it is gated on its own too.
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	cid := repo.channels["films"].ID
	if err := svc.backfillChannelFollower(context.Background(), cid, "films", remoteBob); err != nil {
		t.Fatal(err)
	}
	if got := len(deliveriesOfType(repo, "Create")); got != 0 {
		t.Errorf("Creates to a blocked instance = %d, want 0", got)
	}
}

func ptr(s string) *string { return &s }

// A host following a SECOND channel gets that channel's backfill (its own
// cooldown), but the 11th channel from one host inside the day gets none.
func TestChannelBackfillIsPerChannelWithAPerHostDailyCap(t *testing.T) {
	repo := backfillRepo(t, 2, "https://remote.example/accounts/bob/inbox", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()
	films := repo.channels["films"].ID
	if err := svc.backfillChannelFollower(ctx, films, "films", remoteBob); err != nil {
		t.Fatal(err)
	}
	if got := len(deliveriesOfType(repo, "Create")); got != 2 {
		t.Fatalf("films Creates = %d, want 2", got)
	}
	for i := 1; i < channelBackfillHostChannelCap; i++ { // channels 2..10
		cid := uuid.New()
		repo.channelsByID[cid] = sqlcgen.Channel{ID: cid, Handle: "c" + cid.String()[:6]}
		id := uuid.New()
		repo.videosByID[id] = sqlcgen.GetVideoByIDRow{ID: id, ChannelID: cid, Title: "v", Privacy: "public", State: "published"}
		repo.backfillIDs[cid] = []uuid.UUID{id}
		before := len(deliveriesOfType(repo, "Create"))
		if err := svc.backfillChannelFollower(ctx, cid, repo.channelsByID[cid].Handle, remoteBob); err != nil {
			t.Fatal(err)
		}
		if got := len(deliveriesOfType(repo, "Create")); got != before+1 {
			t.Fatalf("channel %d: Creates %d -> %d, want +1 (its own backfill)", i+1, before, got)
		}
	}
	// Same channel again: its own cooldown holds.
	before := len(deliveriesOfType(repo, "Create"))
	_ = svc.backfillChannelFollower(ctx, films, "films", remoteBob)
	if got := len(deliveriesOfType(repo, "Create")); got != before {
		t.Errorf("same channel re-backfilled inside the cooldown")
	}
	// The 11th channel: over the per-host daily cap.
	cid := uuid.New()
	repo.channelsByID[cid] = sqlcgen.Channel{ID: cid, Handle: "eleventh"}
	id := uuid.New()
	repo.videosByID[id] = sqlcgen.GetVideoByIDRow{ID: id, ChannelID: cid, Title: "v", Privacy: "public", State: "published"}
	repo.backfillIDs[cid] = []uuid.UUID{id}
	_ = svc.backfillChannelFollower(ctx, cid, "eleventh", remoteBob)
	if got := len(deliveriesOfType(repo, "Create")); got != before {
		t.Errorf("11th channel from one host got a backfill (%d -> %d)", before, got)
	}
}

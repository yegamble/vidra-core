package federation

// Enforcement tests for the W12 federation policy gates (config-parity):
// federation_accept_remote_comments, federation_allow_channel_followers,
// federation_follower_approval (+ the admin approve/reject flow incl. the
// Accept/Reject deliveries), and federation_auto_follow_back (+ its loop
// guards). Fixtures come from the contract corpus so the gates are proven
// against real-shaped PeerTube/Mastodon payloads.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// gateOn / gateOff are provider funcs for the boolean gates.
func gateOn() bool  { return true }
func gateOff() bool { return false }

// deliveredPayloads decodes every queued delivery payload's type, keyed by
// activity type ("Accept", "Reject", "Follow", ...).
func deliveredPayloads(t *testing.T, repo fakeRepo) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, d := range repo.deliveries {
		var doc map[string]any
		if err := json.Unmarshal(d.row.Payload, &doc); err != nil {
			t.Fatalf("delivery payload is not JSON: %v\n%s", err, d.row.Payload)
		}
		typ, _ := doc["type"].(string)
		out[typ] = doc
	}
	return out
}

func TestCommentGateDropsInboundNotes(t *testing.T) {
	// Gate off: the real-shaped Mastodon Create{Note} is dropped — accepted,
	// marked processed, never stored. Existing comments are untouched.
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	existing := seedKaisaNote(repo)
	svc := NewService(repo, WithBaseURL(contractBase), WithAcceptRemoteCommentsFunc(gateOff))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_create_note.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.commentsByID) != 1 {
		t.Errorf("comments = %d, want only the pre-existing one (gate off must drop, not store)", len(repo.commentsByID))
	}
	if !repo.processed["https://mastodon.example/users/kaisa/statuses/114522110099887766/activity"] {
		t.Error("dropped activity was not marked processed")
	}
	// The gate also covers edits: an Update{Note} of the existing comment is
	// dropped while off.
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_update_note.json")); err != nil {
		t.Fatalf("HandleInbox(update): %v", err)
	}
	if got := repo.commentsByID[existing].Body; got != "The drone shots are stunning!" {
		t.Errorf("body = %q, want unchanged while the comment gate is off", got)
	}
}

func TestCommentGateDefaultOnStores(t *testing.T) {
	// No provider func wired (the shipped default): comments ingest as before.
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase))
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_create_note.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.commentsByID) != 1 {
		t.Errorf("comments = %d, want 1 (default gate must keep ingesting)", len(repo.commentsByID))
	}
}

func TestCommentGateDoesNotBlockDeletes(t *testing.T) {
	// Removals are always honoured: a Delete{Note} lands while the gate is off.
	repo := newContractRepo()
	seedKaisaNote(repo)
	svc := NewService(repo, WithBaseURL(contractBase), WithAcceptRemoteCommentsFunc(gateOff))
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_delete_note.json")); err != nil {
		t.Fatalf("HandleInbox(delete): %v", err)
	}
	if len(repo.commentsByID) != 0 {
		t.Error("Delete{Note} should still apply while the comment gate is off")
	}
}

func TestChannelFollowersGateRejects(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithAllowChannelFollowersFunc(gateOff))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Errorf("follow recorded despite the gate: %+v", repo.remoteFollows)
	}
	docs := deliveredPayloads(t, repo)
	reject, ok := docs["Reject"]
	if !ok || len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d %v, want exactly one Reject", len(repo.deliveries), docs)
	}
	inner, _ := reject["object"].(map[string]any)
	if inner["type"] != "Follow" || inner["id"] != ctMastoFollow || inner["actor"] != ctMastoUser {
		t.Errorf("Reject embeds %v, want the original Follow", inner)
	}
}

func TestChannelFollowersGateLeavesExistingFollowers(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	key := ctChannelID.String() + "|" + ctMastoUser
	repo.remoteFollows[key] = &fakeRemoteFollow{ID: uuid.New(), ChannelID: ctChannelID, RemoteActorUrl: ctMastoUser, State: "accepted"}
	svc := NewService(repo, WithBaseURL(contractBase), WithAllowChannelFollowersFunc(gateOff))

	// A foreign actor's fresh Follow is rejected…
	cacheContractActor(repo, ctPTAccount, "Person", "marta", "peertube.example")
	if err := svc.HandleInbox(context.Background(), ctPTAccount, readFixture(t, "inbound/peertube_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	// …while the existing follower's edge stays (not retroactive).
	if got, ok := repo.remoteFollows[key]; !ok || got.State != "accepted" {
		t.Errorf("existing follower disturbed by the gate: %+v", repo.remoteFollows)
	}
}

func TestFollowerApprovalRecordsPendingWithoutAccept(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithFollowerApprovalFunc(gateOn))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	row, ok := repo.remoteFollows[ctChannelID.String()+"|"+ctMastoUser]
	if !ok || row.State != "pending" {
		t.Fatalf("follow = %+v, want a pending row", repo.remoteFollows)
	}
	if row.FollowActivityUrl != ctMastoFollow {
		t.Errorf("follow_activity_url = %q, want %q", row.FollowActivityUrl, ctMastoFollow)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("deliveries = %d, want 0 (the Accept waits for approval)", len(repo.deliveries))
	}
	// The queue lists it.
	reqs, _, err := svc.ListFollowerRequests(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("ListFollowerRequests: %v", err)
	}
	if len(reqs) != 1 || reqs[0].ActorURL != ctMastoUser || reqs[0].ChannelHandle != "films" || reqs[0].Handle != "kaisa@mastodon.example" {
		t.Errorf("queue = %+v, want kaisa's pending follow of films", reqs)
	}
}

func TestFollowerApprovalDoesNotDowngradeAcceptedFollower(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	key := ctChannelID.String() + "|" + ctMastoUser
	repo.remoteFollows[key] = &fakeRemoteFollow{ID: uuid.New(), ChannelID: ctChannelID, RemoteActorUrl: ctMastoUser, State: "accepted"}
	svc := NewService(repo, WithBaseURL(contractBase), WithFollowerApprovalFunc(gateOn))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if got := repo.remoteFollows[key]; got.State != "accepted" {
		t.Errorf("state = %q, want accepted (a re-Follow must not downgrade)", got.State)
	}
}

func TestApproveFollowerRequestDeliversAccept(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithFollowerApprovalFunc(gateOn))
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	row := repo.remoteFollows[ctChannelID.String()+"|"+ctMastoUser]

	if err := svc.ApproveFollowerRequest(context.Background(), row.ID); err != nil {
		t.Fatalf("ApproveFollowerRequest: %v", err)
	}
	if row.State != "accepted" {
		t.Errorf("state = %q, want accepted", row.State)
	}
	// The queued Accept matches the contract golden byte-for-byte.
	assertGolden(t, "accept_follow.json", takeSingleDelivery(t, repo))

	// Approving again → 404-shaped sentinel (already resolved).
	if err := svc.ApproveFollowerRequest(context.Background(), row.ID); !errors.Is(err, ErrFollowerRequestNotFound) {
		t.Errorf("second approve err = %v, want ErrFollowerRequestNotFound", err)
	}
}

func TestRejectFollowerRequestDeliversReject(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithFollowerApprovalFunc(gateOn))
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	row := repo.remoteFollows[ctChannelID.String()+"|"+ctMastoUser]

	if err := svc.RejectFollowerRequest(context.Background(), row.ID); err != nil {
		t.Fatalf("RejectFollowerRequest: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Errorf("pending follow survived the rejection: %+v", repo.remoteFollows)
	}
	// The queued Reject matches the contract golden byte-for-byte.
	assertGolden(t, "reject_follow.json", takeSingleDelivery(t, repo))

	if err := svc.RejectFollowerRequest(context.Background(), row.ID); !errors.Is(err, ErrFollowerRequestNotFound) {
		t.Errorf("second reject err = %v, want ErrFollowerRequestNotFound", err)
	}
}

func TestAutoFollowBackQueuesChannelFollow(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithAutoFollowBackFunc(gateOn))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	docs := deliveredPayloads(t, repo)
	if len(repo.deliveries) != 2 {
		t.Fatalf("deliveries = %d %v, want Accept + follow-back Follow", len(repo.deliveries), docs)
	}
	follow, ok := docs["Follow"]
	if !ok {
		t.Fatalf("no Follow queued: %v", docs)
	}
	// Signed by the FOLLOWED CHANNEL's actor (vidra has no instance actor).
	if follow["actor"] != contractBase+"/video-channels/films" || follow["object"] != ctMastoUser {
		t.Errorf("follow-back = %v, want films → kaisa", follow)
	}
	for _, d := range repo.deliveries {
		var doc map[string]any
		_ = json.Unmarshal(d.row.Payload, &doc)
		if doc["type"] == "Follow" && (!d.row.SigningChannelID.Valid || uuid.UUID(d.row.SigningChannelID.Bytes) != ctChannelID) {
			t.Errorf("follow-back signer = %+v, want the films channel actor", d.row.SigningChannelID)
		}
	}
	fb, ok := repo.followBacks[ctChannelID.String()+"|"+ctMastoUser]
	if !ok || fb.State != "pending" {
		t.Fatalf("follow-back edge = %+v, want a pending row", repo.followBacks)
	}

	// The remote's Accept flips the follow-back edge to accepted, and the edge
	// then gates ingestion (HasAcceptedRemoteChannelFollow).
	accept := `{"id":"https://mastodon.example/act/accept/1","type":"Accept","actor":"` + ctMastoUser +
		`","object":{"id":"` + fb.FollowActivityUrl + `","type":"Follow","actor":"` + contractBase + `/video-channels/films","object":"` + ctMastoUser + `"}}`
	if err := svc.HandleInbox(context.Background(), ctMastoUser, []byte(accept)); err != nil {
		t.Fatalf("HandleInbox(Accept): %v", err)
	}
	if fb.State != "accepted" {
		t.Errorf("follow-back state = %q, want accepted after the remote Accept", fb.State)
	}
	if has, _ := repo.HasAcceptedRemoteChannelFollow(context.Background(), ctMastoUser); !has {
		t.Error("an accepted follow-back should count as an ingestion edge")
	}
}

func TestAutoFollowBackRejectRemovesEdge(t *testing.T) {
	repo := newContractRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithAutoFollowBackFunc(gateOn))
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	fb := repo.followBacks[ctChannelID.String()+"|"+ctMastoUser]

	reject := `{"id":"https://mastodon.example/act/reject/1","type":"Reject","actor":"` + ctMastoUser +
		`","object":{"id":"` + fb.FollowActivityUrl + `","type":"Follow","actor":"` + contractBase + `/video-channels/films","object":"` + ctMastoUser + `"}}`
	if err := svc.HandleInbox(context.Background(), ctMastoUser, []byte(reject)); err != nil {
		t.Fatalf("HandleInbox(Reject): %v", err)
	}
	if len(repo.followBacks) != 0 {
		t.Errorf("follow-back survived the remote Reject: %+v", repo.followBacks)
	}
}

func TestAutoFollowBackLoopGuards(t *testing.T) {
	cases := []struct {
		name string
		seed func(r fakeRepo)
	}{
		{
			name: "existing follow-back edge (any channel) skips",
			seed: func(r fakeRepo) {
				r.followBacks["other|"+ctMastoUser] = &fakeFollowBack{
					ChannelID: uuid.New(), RemoteActorUrl: ctMastoUser, State: "pending",
				}
			},
		},
		{
			name: "existing local user follow (pending or accepted) skips",
			seed: func(r fakeRepo) {
				id := uuid.New()
				r.rcFollows[id] = &sqlcgen.RemoteChannelFollow{
					ID: id, UserID: ctUserID, RemoteActorUrl: ctMastoUser, State: "pending",
				}
			},
		},
		{
			name: "blocked follower domain skips",
			seed: func(r fakeRepo) { r.blockedDomains["mastodon.example"] = true },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newContractRepo()
			cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
			svc := NewService(repo, WithBaseURL(contractBase), WithAutoFollowBackFunc(gateOn))
			tc.seed(repo)
			seededFollowBacks := len(repo.followBacks)

			// The blocked-domain case never reaches handleFollow through
			// HandleInbox (the inbox drops it), so drive the approval path
			// where the blocklist check inside maybeAutoFollowBack matters.
			if repo.blockedDomains["mastodon.example"] {
				if err := svc.maybeAutoFollowBack(context.Background(), ctChannelID, "films", ctMastoUser); err != nil {
					t.Fatalf("maybeAutoFollowBack: %v", err)
				}
				if len(repo.deliveries) != 0 || len(repo.followBacks) != 0 {
					t.Errorf("follow-back sent to a blocked domain: deliveries=%d edges=%d", len(repo.deliveries), len(repo.followBacks))
				}
				return
			}
			if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			docs := deliveredPayloads(t, repo)
			if _, ok := docs["Follow"]; ok {
				t.Errorf("a follow-back was queued despite the guard: %v", docs)
			}
			if len(repo.deliveries) != 1 {
				t.Errorf("deliveries = %d, want only the Accept", len(repo.deliveries))
			}
			if len(repo.followBacks) != seededFollowBacks {
				t.Errorf("a new follow-back edge appeared despite the guard: %+v", repo.followBacks)
			}
		})
	}
}

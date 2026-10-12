package federation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const ptSharedInbox = "https://pt.example/inbox"

// newAnnounceRepo is one channel with one video, followed by the channel
// follower inbox given, on an instance PeerTube's instance actor follows.
func newAnnounceRepo(channelID, videoID uuid.UUID, privacy string, channelInboxes ...string) fakeRepo {
	repo := newOutboxRepo(channelID, videoID, privacy, "published", channelInboxes)
	shared := ptSharedInbox
	repo.inst = &fakeInstance{reserved: true, followers: map[string]*sqlcgen.GetInstanceFollowerByActorRow{
		ptInstance: {ID: uuid.New(), State: "accepted"},
	}}
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		ptInstance: {ActorUrl: ptInstance, InboxUrl: ptInbox, SharedInboxUrl: &shared},
	}
	return repo
}

// sent groups the queued deliveries by activity type.
func sent(t *testing.T, repo fakeRepo) map[string][]sqlcgen.EnqueueDeliveryParams {
	t.Helper()
	out := map[string][]sqlcgen.EnqueueDeliveryParams{}
	for _, d := range repo.deliveries {
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(d.enqueued.Payload, &env); err != nil {
			t.Fatal(err)
		}
		out[env.Type] = append(out[env.Type], d.enqueued)
	}
	return out
}

func TestPublishedVideoIsAnnouncedToInstanceFollowers(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newAnnounceRepo(channelID, videoID, "public", "https://a.example/inbox")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
		t.Fatal(err)
	}
	got := sent(t, repo)
	if len(got["Create"]) != 1 || got["Create"][0].InboxUrl != "https://a.example/inbox" {
		t.Errorf("Create = %+v, want the channel follower only", got["Create"])
	}
	if len(got["Announce"]) != 1 {
		t.Fatalf("Announce = %+v, want one to the instance follower", got["Announce"])
	}
	a := got["Announce"][0]
	var body map[string]any
	_ = json.Unmarshal(a.Payload, &body)
	if !a.SigningInstanceActor || a.InboxUrl != ptSharedInbox || body["actor"] != ourInstance ||
		body["object"] != "https://videos.example/videos/"+videoID.String() {
		t.Errorf("announce = %+v %v", a, body)
	}
}

// Only what a stranger may see is ever announced to another instance.
func TestOnlyFederableVideosAreAnnouncedToInstances(t *testing.T) {
	for _, privacy := range []string{"private", "unlisted", "internal"} {
		channelID, videoID := uuid.New(), uuid.New()
		repo := newAnnounceRepo(channelID, videoID, privacy)
		if err := NewService(repo, WithBaseURL("https://videos.example")).AnnounceVideo(context.Background(), videoID); err != nil {
			t.Fatal(err)
		}
		if len(repo.deliveries) != 0 {
			t.Errorf("%s video: %d deliveries, want none", privacy, len(repo.deliveries))
		}
	}
	channelID, videoID := uuid.New(), uuid.New()
	blocked := newAnnounceRepo(channelID, videoID, "public")
	v := blocked.videosByID[videoID]
	v.Blocked = true
	blocked.videosByID[videoID] = v
	optedOut := newAnnounceRepo(channelID, videoID, "public")
	optedOut.apDisabled = map[uuid.UUID]bool{channelID: true}
	pending := newAnnounceRepo(channelID, videoID, "public")
	pending.inst.followers[ptInstance].State = "pending"
	for name, repo := range map[string]fakeRepo{"blocked": blocked, "AP opted out": optedOut, "pending follower": pending} {
		if err := NewService(repo, WithBaseURL("https://videos.example")).AnnounceVideo(context.Background(), videoID); err != nil {
			t.Fatal(err)
		}
		if n := len(sent(t, repo)["Announce"]); n != 0 {
			t.Errorf("%s: %d Announces, want none", name, n)
		}
	}
}

// The instances that were announced a video get its edits and retractions,
// once per inbox even when that inbox also follows the channel.
func TestEditsAndRetractionsReachInstanceFollowers(t *testing.T) {
	for want, channelInboxes := range map[int][]string{
		2: {"https://a.example/inbox"}, // the channel follower + the instance
		1: {ptSharedInbox},             // the same server both ways: once
	} {
		channelID, videoID := uuid.New(), uuid.New()
		repo := newAnnounceRepo(channelID, videoID, "public", channelInboxes...)
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		if err := svc.UpdateVideo(context.Background(), videoID, true); err != nil {
			t.Fatal(err)
		}
		if err := svc.DeleteVideo(context.Background(), videoID, channelID, true); err != nil {
			t.Fatal(err)
		}
		got := sent(t, repo)
		for _, typ := range []string{"Update", "Delete"} {
			reached := map[string]bool{}
			for _, d := range got[typ] {
				reached[d.InboxUrl] = true
				if !d.SigningChannelID.Valid || d.SigningInstanceActor {
					t.Errorf("%s must be signed by the channel that owns the video", typ)
				}
			}
			if len(got[typ]) != want || !reached[ptSharedInbox] {
				t.Errorf("%s reached %v, want %d inboxes including the instance follower", typ, reached, want)
			}
		}
	}
}

func TestAcceptedInstanceFollowerIsBackfilled(t *testing.T) {
	repo := newInstanceInboxRepo()
	shared := ptSharedInbox
	ra := repo.remoteActors[ptInstance]
	ra.SharedInboxUrl = &shared
	repo.remoteActors[ptInstance] = ra
	newest, older := uuid.New(), uuid.New()
	repo.inst.outbox = []uuid.UUID{newest, older}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatal(err)
	}
	got := sent(t, repo)
	if len(got["Accept"]) != 1 || len(got["Announce"]) != 2 {
		t.Fatalf("sent = Accept %d, Announce %d; want 1 and 2", len(got["Accept"]), len(got["Announce"]))
	}
	objects := map[string]bool{}
	for _, d := range got["Announce"] {
		var body map[string]any
		_ = json.Unmarshal(d.Payload, &body)
		objects[body["object"].(string)] = d.InboxUrl == ptSharedInbox && d.SigningInstanceActor
	}
	for _, id := range []uuid.UUID{newest, older} {
		if !objects["https://videos.example/videos/"+id.String()] {
			t.Errorf("video %s was not announced to the new follower's inbox", id)
		}
	}
	// A re-Follow is not a new follower: no second backfill.
	if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID+"-again")); err != nil {
		t.Fatal(err)
	}
	if n := len(sent(t, repo)["Announce"]); n != 2 {
		t.Errorf("Announces after a re-Follow = %d, want still 2", n)
	}
}

// The backfill goes only to the follower's own host: an inbox pointed at a
// third party must not turn one Follow into a burst of POSTs at it.
func TestBackfillIsNotReflectedToAnotherHost(t *testing.T) {
	repo := newInstanceInboxRepo()
	elsewhere := "https://victim.example/inbox"
	ra := repo.remoteActors[ptInstance]
	ra.SharedInboxUrl = &elsewhere
	repo.remoteActors[ptInstance] = ra
	repo.inst.outbox = []uuid.UUID{uuid.New(), uuid.New()}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatal(err)
	}
	got := sent(t, repo)
	if len(got["Accept"]) != 1 || len(got["Announce"]) != 0 {
		t.Errorf("sent Accept %d, Announce %d; want the Accept only", len(got["Accept"]), len(got["Announce"]))
	}
}

// A Follow/Undo loop gets one backfill per cooldown window, not one per loop:
// the re-Follow is accepted and answered, and sends no videos.
func TestBackfillHasAPerActorCooldown(t *testing.T) {
	repo := newInstanceInboxRepo()
	repo.inst.outbox = []uuid.UUID{uuid.New(), uuid.New()}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()
	undo := func(id string) []byte {
		return []byte(`{"id":"https://pt.example/undo/` + id + `","type":"Undo","actor":"` + ptInstance +
			`","object":{"type":"Follow","actor":"` + ptInstance + `","object":"` + ourInstance + `"}}`)
	}
	if err := svc.HandleInbox(ctx, ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbox(ctx, ptInstance, undo("1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbox(ctx, ptInstance, instanceFollow(instanceFollowID+"-again")); err != nil {
		t.Fatal(err)
	}
	got := sent(t, repo)
	if repo.inst.followers[ptInstance] == nil || repo.inst.followers[ptInstance].State != "accepted" {
		t.Fatal("the re-Follow inside the window must still be accepted")
	}
	if len(got["Accept"]) != 2 || len(got["Announce"]) != 2 {
		t.Fatalf("sent Accept %d, Announce %d; want 2 Accepts and ONE backfill of 2", len(got["Accept"]), len(got["Announce"]))
	}
	// Once the window has passed, a new follow is backfilled again.
	repo.inst.backfilledAt[ptInstance] = time.Now().Add(-instanceBackfillCooldown - time.Minute)
	if err := svc.HandleInbox(ctx, ptInstance, undo("2")); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbox(ctx, ptInstance, instanceFollow(instanceFollowID+"-later")); err != nil {
		t.Fatal(err)
	}
	if n := len(sent(t, repo)["Announce"]); n != 4 {
		t.Errorf("Announces after the window = %d, want a second backfill (4)", n)
	}
}

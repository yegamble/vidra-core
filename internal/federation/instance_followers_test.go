package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	ptInstance       = "https://pt.example/accounts/peertube"
	ptInbox          = "https://pt.example/accounts/peertube/inbox"
	ourInstance      = "https://videos.example/accounts/peertube"
	instanceFollowID = "https://pt.example/accounts/peertube/follows/7"
)

// newInstanceInboxRepo is an instance whose actor is served, with PeerTube's
// instance actor cached the way signature verification leaves it.
func newInstanceInboxRepo() fakeRepo {
	repo := newInstanceRepo()
	repo.processed = map[string]bool{}
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		ptInstance: {ActorUrl: ptInstance, ActorType: "Application", InboxUrl: ptInbox, Domain: "pt.example"},
	}
	repo.adminActorBlocks = map[string]string{}
	return repo
}

func instanceFollow(id string) []byte {
	return []byte(`{"id":"` + id + `","type":"Follow","actor":"` + ptInstance + `","object":"` + ourInstance + `"}`)
}

// onlyDelivery returns the single queued delivery and its decoded payload.
func onlyDelivery(t *testing.T, repo fakeRepo) (sqlcgen.EnqueueDeliveryParams, map[string]any) {
	t.Helper()
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var body map[string]any
		if err := json.Unmarshal(d.enqueued.Payload, &body); err != nil {
			t.Fatal(err)
		}
		return d.enqueued, body
	}
	return sqlcgen.EnqueueDeliveryParams{}, nil
}

func TestInstanceFollowIsAcceptedByTheInstanceActor(t *testing.T) {
	repo := newInstanceInboxRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if f := repo.inst.followers[ptInstance]; f == nil || f.State != "accepted" || f.FollowActivityUrl != instanceFollowID {
		t.Fatalf("follower = %+v, want accepted", f)
	}
	d, body := onlyDelivery(t, repo)
	// PeerTube accepts only on the follower's own inbox, signed by the actor it followed.
	if !d.SigningInstanceActor || d.InboxUrl != ptInbox || d.SigningChannelID.Valid || d.SigningUserID.Valid {
		t.Errorf("delivery = %+v, want the instance actor signing to %s", d, ptInbox)
	}
	obj, _ := body["object"].(map[string]any)
	if body["type"] != "Accept" || body["actor"] != ourInstance || obj["id"] != instanceFollowID || obj["object"] != ourInstance {
		t.Errorf("accept = %v", body)
	}
}

func TestInstanceFollowWaitsForApproval(t *testing.T) {
	repo := newInstanceInboxRepo()
	var pinged []uuid.UUID
	svc := NewService(repo, WithBaseURL("https://videos.example"),
		WithFollowerApprovalFunc(func() bool { return true }),
		WithInstanceFollowerRequestHook(func(_ context.Context, id uuid.UUID) { pinged = append(pinged, id) }))
	for _, id := range []string{instanceFollowID, instanceFollowID + "-retry"} {
		if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(id)); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
	}
	f := repo.inst.followers[ptInstance]
	if f == nil || f.State != "pending" {
		t.Fatalf("follower = %+v, want pending", f)
	}
	if len(pinged) != 1 || pinged[0] != f.ID {
		t.Errorf("staff pinged %v, want once for %s", pinged, f.ID)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("a pending follow must not be answered yet: %d deliveries", len(repo.deliveries))
	}
}

func TestRejectedInstanceStaysRejected(t *testing.T) {
	repo := newInstanceInboxRepo()
	repo.inst.followers[ptInstance] = &sqlcgen.GetInstanceFollowerByActorRow{ID: uuid.New(), State: "rejected"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	undo := `{"id":"https://pt.example/undo/1","type":"Undo","actor":"` + ptInstance + `","object":{"type":"Follow","actor":"` + ptInstance + `","object":"` + ourInstance + `"}}`
	if err := svc.HandleInbox(context.Background(), ptInstance, []byte(undo)); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatal(err)
	}
	if repo.inst.followers[ptInstance].State != "rejected" {
		t.Fatal("an Undo then Follow walked past the admin's rejection")
	}
	if _, body := onlyDelivery(t, repo); body["type"] != "Reject" || body["actor"] != ourInstance {
		t.Errorf("answer = %v, want the instance actor's Reject", body)
	}
}

func TestUndoInstanceFollowRemovesTheFollower(t *testing.T) {
	repo := newInstanceInboxRepo()
	repo.inst.followers[ptInstance] = &sqlcgen.GetInstanceFollowerByActorRow{ID: uuid.New(), State: "accepted"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	undo := `{"id":"https://pt.example/undo/2","type":"Undo","actor":"` + ptInstance + `","object":{"type":"Follow","actor":"` + ptInstance + `","object":"` + ourInstance + `"}}`
	// A third party cannot undo someone else's follow.
	if err := svc.HandleInbox(context.Background(), "https://pt.example/accounts/eve", []byte(undo)); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("spoofed Undo: err = %v, want ErrActorMismatch", err)
	}
	if err := svc.HandleInbox(context.Background(), ptInstance, []byte(undo)); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.inst.followers[ptInstance]; ok {
		t.Error("Undo{Follow} did not remove the instance follower")
	}
}

func TestInstanceFollowRefusals(t *testing.T) {
	t.Run("spoofed actor", func(t *testing.T) {
		repo := newInstanceInboxRepo()
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		err := svc.HandleInbox(context.Background(), "https://pt.example/accounts/eve", instanceFollow(instanceFollowID))
		if !errors.Is(err, ErrActorMismatch) || len(repo.inst.followers) != 0 {
			t.Errorf("err = %v, followers = %d; want ErrActorMismatch and nothing recorded", err, len(repo.inst.followers))
		}
	})
	t.Run("actor blocked by an admin", func(t *testing.T) {
		repo := newInstanceInboxRepo()
		repo.adminActorBlocks[ptInstance] = ""
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
			t.Fatal(err)
		}
		if len(repo.inst.followers) != 0 || len(repo.deliveries) != 0 {
			t.Error("a blocked actor's follow must be dropped unanswered")
		}
	})
	t.Run("instance blocked", func(t *testing.T) {
		repo := newInstanceInboxRepo()
		repo.blockedDomains = map[string]bool{"pt.example": true}
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); !errors.Is(err, ErrSenderBlocked) {
			t.Fatalf("err = %v, want ErrSenderBlocked", err)
		}
		if len(repo.inst.followers) != 0 {
			t.Error("a blocked instance must not be recorded as a follower")
		}
		if d, body := onlyDelivery(t, repo); body["type"] != "Reject" || !d.SigningInstanceActor {
			t.Errorf("answer = %v, want the instance actor's Reject", body)
		}
	})
	t.Run("instance actor not served", func(t *testing.T) {
		repo := newInstanceInboxRepo()
		repo.inst.reserved = false
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
			t.Fatal(err)
		}
		if len(repo.inst.followers) != 0 || len(repo.deliveries) != 0 {
			t.Error("a follow of an actor this instance does not serve must change nothing")
		}
	})
}

func TestDrainDeliveriesSignsAsInstanceActor(t *testing.T) {
	repo := newInstanceRepo()
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	var verifiedKey string
	var verifyErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.inst.key.PublicKeyPem)
		if err != nil {
			t.Errorf("parse instance key: %v", err)
		}
		verifiedKey, verifyErr = httpsigVerifier(pub)(r, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if err := svc.enqueueInstanceDelivery(context.Background(), srv.URL, []byte(`{"type":"Accept"}`)); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.DrainDeliveries(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDeliveries = %d, %v", n, err)
	}
	if verifyErr != nil || verifiedKey != ourInstance+"#main-key" {
		t.Errorf("signed by %q (%v), want the instance actor", verifiedKey, verifyErr)
	}
}

// An instance is followed by an instance actor, from its own server.
func TestInstanceFollowFromTheWrongKindOfActor(t *testing.T) {
	other := "https://elsewhere.example/inbox"
	for name, tc := range map[string]struct {
		mutate     func(*sqlcgen.RemoteActor)
		wantReject bool
	}{
		"a Person":                 {func(ra *sqlcgen.RemoteActor) { ra.ActorType = "Person" }, true},
		"an inbox on another host": {func(ra *sqlcgen.RemoteActor) { ra.InboxUrl = other }, false},
		"a shared inbox elsewhere": {func(ra *sqlcgen.RemoteActor) { ra.SharedInboxUrl = &other }, false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newInstanceInboxRepo()
			ra := repo.remoteActors[ptInstance]
			tc.mutate(&ra)
			repo.remoteActors[ptInstance] = ra
			svc := NewService(repo, WithBaseURL("https://videos.example"))
			if err := svc.HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
				t.Fatal(err)
			}
			if len(repo.inst.followers) != 0 {
				t.Error("the follow must not be recorded")
			}
			if tc.wantReject {
				if d, body := onlyDelivery(t, repo); body["type"] != "Reject" || d.InboxUrl != ptInbox {
					t.Errorf("answer = %v to %s, want a Reject to its own inbox", body, d.InboxUrl)
				}
			} else if len(repo.deliveries) != 0 {
				t.Errorf("%d deliveries; nothing may be sent toward another server", len(repo.deliveries))
			}
		})
	}
	// A Service is an instance actor too.
	repo := newInstanceInboxRepo()
	ra := repo.remoteActors[ptInstance]
	ra.ActorType = "Service"
	repo.remoteActors[ptInstance] = ra
	if err := NewService(repo, WithBaseURL("https://videos.example")).HandleInbox(context.Background(), ptInstance, instanceFollow(instanceFollowID)); err != nil {
		t.Fatal(err)
	}
	if f := repo.inst.followers[ptInstance]; f == nil || f.State != "accepted" {
		t.Errorf("a Service actor's follow = %+v, want accepted", f)
	}
}

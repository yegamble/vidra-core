package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// newFollowRepo builds a fakeRepo with everything the outbound-follow flow
// touches: the caller's account row, the actor cache, minted keys, follows,
// and the delivery queue.
func newFollowRepo(userID uuid.UUID) fakeRepo {
	return fakeRepo{
		usersByID: map[uuid.UUID]sqlcgen.GetUserActorByIDRow{
			userID: {ID: userID, Username: "ada", DisplayName: "Ada"},
		},
		acctKeys:     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		remoteActors: map[string]sqlcgen.RemoteActor{},
		rcFollows:    map[uuid.UUID]*sqlcgen.RemoteChannelFollow{},
		deliveries:   map[uuid.UUID]*fakeDelivery{},
		processed:    map[string]bool{},
		remoteVideos: map[string]*fakeRemoteVideo{},
	}
}

// cacheRemoteChannel pre-caches a remote channel actor (as signature
// verification would) so the follow flow needs no network.
func cacheRemoteChannel(repo fakeRepo, actorURL, domain string, sharedInbox *string) {
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{
		ActorUrl:          actorURL,
		ActorType:         "Group",
		PreferredUsername: "movies",
		Domain:            domain,
		InboxUrl:          "https://" + domain + "/channel-inbox",
		SharedInboxUrl:    sharedInbox,
		PublicKeyPem:      "PEM",
	}
}

func TestFollowRemoteChannelPendingAndQueued(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	shared := "https://remote.example/inbox"
	cacheRemoteChannel(repo, remoteChan, "remote.example", &shared)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("FollowRemoteChannel: %v", err)
	}
	if follow.State != "pending" {
		t.Errorf("state = %q, want pending", follow.State)
	}
	if follow.Handle != "movies@remote.example" || follow.Domain != "remote.example" {
		t.Errorf("identity = %q / %q", follow.Handle, follow.Domain)
	}
	if _, ok := repo.acctKeys[userID]; !ok {
		t.Error("account keypair was not minted before enqueue")
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		if d.row.InboxUrl != shared {
			t.Errorf("inbox = %q, want the shared inbox %q", d.row.InboxUrl, shared)
		}
		if !d.row.SigningUserID.Valid || uuid.UUID(d.row.SigningUserID.Bytes) != userID || d.row.SigningUsername != "ada" {
			t.Errorf("delivery signer = %+v/%q, want the user's ACCOUNT actor", d.row.SigningUserID, d.row.SigningUsername)
		}
		if d.row.SigningChannelID.Valid {
			t.Error("delivery must not be channel-signed")
		}
		var act struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Actor  string `json:"actor"`
			Object string `json:"object"`
		}
		if err := json.Unmarshal(d.row.Payload, &act); err != nil {
			t.Fatalf("unmarshal Follow: %v", err)
		}
		if act.Type != "Follow" || act.Actor != "https://videos.example/accounts/ada" || act.Object != remoteChan {
			t.Errorf("follow activity = %+v", act)
		}
		if !strings.HasPrefix(act.ID, "https://videos.example/accounts/ada/activities/follow/") {
			t.Errorf("follow id = %q, want minted on our account actor", act.ID)
		}
	}
}

func TestFollowRemoteChannelIdempotent(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	first, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("first follow: %v", err)
	}
	second, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("second follow: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("re-follow minted a new row (%s vs %s)", first.ID, second.ID)
	}
	if len(repo.deliveries) != 1 {
		t.Errorf("deliveries = %d, want 1 (no second Follow sent)", len(repo.deliveries))
	}
}

func TestFollowRemoteChannelRejectsLocalAndBadTargets(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if _, err := svc.FollowRemoteChannel(context.Background(), userID, "https://videos.example/video-channels/films"); !errors.Is(err, ErrLocalFollowTarget) {
		t.Errorf("local actor URL err = %v, want ErrLocalFollowTarget", err)
	}
	for _, target := range []string{"", "garbage", "name@", "@domain.example", "a@b@c d"} {
		if _, err := svc.FollowRemoteChannel(context.Background(), userID, target); !errors.Is(err, ErrBadFollowTarget) {
			t.Errorf("target %q err = %v, want ErrBadFollowTarget", target, err)
		}
	}
	if len(repo.deliveries) != 0 || len(repo.rcFollows) != 0 {
		t.Error("rejected targets must not enqueue or store anything")
	}
}

func TestFollowRemoteChannelViaWebFinger(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)

	// The httptest "remote instance" serves WebFinger + the actor document.
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("resource"); !strings.HasPrefix(got, "acct:movies@") {
			t.Errorf("webfinger resource = %q", got)
		}
		_ = json.NewEncoder(w).Encode(JRD{
			Subject: "acct:movies@remote.example",
			Links: []JRDLink{
				{Rel: "http://webfinger.net/rel/profile-page", Type: "text/html", Href: srv.URL + "/@movies"},
				{Rel: "self", Type: "application/activity+json", Href: srv.URL + "/video-channels/movies"},
			},
		})
	})
	mux.HandleFunc("/video-channels/movies", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": srv.URL + "/video-channels/movies", "type": "Group",
			"preferredUsername": "movies",
			"inbox":             srv.URL + "/inbox",
			"publicKey":         map[string]string{"publicKeyPem": "PEM"},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	// allowPrivateFetch lets the https-first WebFinger fall back to the plain-
	// http loopback origin (dev/e2e posture; production stays https-only).
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, "movies@"+host)
	if err != nil {
		t.Fatalf("FollowRemoteChannel via webfinger: %v", err)
	}
	if follow.ActorURL != srv.URL+"/video-channels/movies" {
		t.Errorf("resolved actor = %q", follow.ActorURL)
	}
	if _, ok := repo.remoteActors[follow.ActorURL]; !ok {
		t.Error("resolved actor was not cached")
	}
	if len(repo.deliveries) != 1 {
		t.Errorf("deliveries = %d, want the queued Follow", len(repo.deliveries))
	}
}

func TestUnfollowRemoteChannelDeletesAndQueuesUndo(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	if err := svc.UnfollowRemoteChannel(context.Background(), userID, follow.ID); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	if len(repo.rcFollows) != 0 {
		t.Error("row not deleted (local intent wins)")
	}
	if len(repo.deliveries) != 2 {
		t.Fatalf("deliveries = %d, want Follow + Undo", len(repo.deliveries))
	}
	sawUndo := false
	for _, d := range repo.deliveries {
		var act struct {
			Type   string `json:"type"`
			Actor  string `json:"actor"`
			Object struct {
				Type   string `json:"type"`
				Object string `json:"object"`
			} `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &act)
		if act.Type != "Undo" {
			continue
		}
		sawUndo = true
		if act.Actor != "https://videos.example/accounts/ada" || act.Object.Type != "Follow" || act.Object.Object != remoteChan {
			t.Errorf("undo = %+v", act)
		}
		if !d.row.SigningUserID.Valid {
			t.Error("Undo must be account-signed")
		}
	}
	if !sawUndo {
		t.Error("no Undo{Follow} was queued")
	}

	// Unknown / already-removed id → ErrFollowNotFound.
	if err := svc.UnfollowRemoteChannel(context.Background(), userID, follow.ID); !errors.Is(err, ErrFollowNotFound) {
		t.Errorf("second unfollow err = %v, want ErrFollowNotFound", err)
	}
}

// acceptActivity renders an Accept (or Reject) wrapping our Follow.
func acceptActivity(typ, id, actor, followURL, followedActor string) string {
	return `{"id":"` + id + `","type":"` + typ + `","actor":"` + actor + `","object":{` +
		`"id":"` + followURL + `","type":"Follow","actor":"https://videos.example/accounts/ada","object":"` + followedActor + `"}}`
}

func TestInboxAcceptFlipsFollowAccepted(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	followURL := repo.rcFollows[follow.ID].FollowActivityUrl

	// An Accept signed by someone OTHER than the followed actor matches nothing.
	stranger := "https://evil.example/accounts/mallory"
	wrongSigner := acceptActivity("Accept", "https://evil.example/act/1", stranger, followURL, stranger)
	if err := svc.HandleInbox(context.Background(), stranger, []byte(wrongSigner)); err != nil {
		t.Fatalf("HandleInbox wrong signer: %v", err)
	}
	if got := repo.rcFollows[follow.ID].State; got != "pending" {
		t.Fatalf("state after foreign Accept = %q, want pending", got)
	}

	// The real Accept (signer == followed actor, matching follow id) flips it.
	accept := acceptActivity("Accept", "https://remote.example/act/2", remoteChan, followURL, remoteChan)
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(accept)); err != nil {
		t.Fatalf("HandleInbox Accept: %v", err)
	}
	if got := repo.rcFollows[follow.ID].State; got != "accepted" {
		t.Errorf("state after Accept = %q, want accepted", got)
	}

	// The accepted edge now feeds the ingestion gate via the repo (no
	// WithFollowEdgeChecker wired).
	if ok, err := svc.hasAcceptedFollowEdge(context.Background(), remoteChan); err != nil || !ok {
		t.Errorf("hasAcceptedFollowEdge = %v, %v; want true via repo", ok, err)
	}
}

// A Reject marks the row REJECTED rather than deleting it, so the person who
// asked can see that their request was refused rather than watching it vanish.
// Re-following the same actor re-arms it to 'pending' with a fresh Follow — the
// one deliberate retry, and the only path back for a follow refused during an
// instance block, which gets no Reject at all.
func TestInboxRejectMarksTheFollowRejected(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	followURL := repo.rcFollows[follow.ID].FollowActivityUrl
	reject := acceptActivity("Reject", "https://remote.example/act/3", remoteChan, followURL, remoteChan)
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(reject)); err != nil {
		t.Fatalf("HandleInbox Reject: %v", err)
	}
	if len(repo.rcFollows) != 1 {
		t.Fatalf("the refused follow must stay visible, got %+v", repo.rcFollows)
	}
	if got := repo.rcFollows[follow.ID].State; got != "rejected" {
		t.Fatalf("state after Reject = %q, want rejected", got)
	}

	// Asking again re-arms the row and mints a new Follow to send.
	again, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("re-follow: %v", err)
	}
	if got := repo.rcFollows[again.ID].State; got != "pending" {
		t.Fatalf("state after re-follow = %q, want pending", got)
	}
	if repo.rcFollows[again.ID].FollowActivityUrl == followURL {
		t.Error("a retry must mint a NEW Follow activity id; the old one was already answered")
	}
}

func TestIngestGateConsultsRepoEdges(t *testing.T) {
	// No WithFollowEdgeChecker: the gate must consult remote_channel_follows
	// through the repository (the production wiring).
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	body := createActivity("https://remote.example/act/g1", remoteChan, remoteVideo, remoteChan, "Gated clip")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Fatal("video ingested without an accepted follow edge")
	}

	// Follow + accept, then a redelivery (new activity id) ingests.
	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	repo.rcFollows[follow.ID].State = "accepted"
	body2 := createActivity("https://remote.example/act/g2", remoteChan, remoteVideo, remoteChan, "Gated clip")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body2)); err != nil {
		t.Fatalf("HandleInbox after accept: %v", err)
	}
	if _, ok := repo.remoteVideos[remoteVideo]; !ok {
		t.Error("video not ingested despite an accepted follow edge")
	}
}

// A29 rehearsal 3, SC1. Migration 0142 made accounts and channels share one
// handle namespace, and WebFinger answers a name that BOTH kinds have held with
// two links: the Person at rel=self and the Group at rel=alternate, "so a peer
// picks by type instead of taking whichever this server happened to try first".
//
// The serving half shipped; the CLIENT half did not. The third two-instance run
// followed `creatora@A` — a username whose channel the backfill had renamed —
// and this instance took rel=self, queued a Follow at the PERSON, and the row
// sat `pending` forever because a Person's inbox has no arm that accepts a
// Follow of a channel. Picking by type is what closes it.
func TestFollowRemoteChannelPrefersTheGroupOnACollidingHandle(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)

	mux := http.NewServeMux()
	var srv *httptest.Server
	actorFetches := 0
	mux.HandleFunc("/.well-known/webfinger", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(JRD{
			Subject: "acct:ownera@remote.example",
			Links: []JRDLink{
				{Rel: "self", Type: "application/activity+json", Href: srv.URL + "/accounts/ownera"},
				{Rel: "alternate", Type: "application/activity+json", Href: srv.URL + "/video-channels/ownera"},
			},
		})
	})
	mux.HandleFunc("/accounts/ownera", func(w http.ResponseWriter, _ *http.Request) {
		actorFetches++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": srv.URL + "/accounts/ownera", "type": "Person",
			"preferredUsername": "ownera",
			"inbox":             srv.URL + "/accounts/ownera/inbox",
			"publicKey":         map[string]string{"publicKeyPem": "PEM"},
		})
	})
	mux.HandleFunc("/video-channels/ownera", func(w http.ResponseWriter, _ *http.Request) {
		actorFetches++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": srv.URL + "/video-channels/ownera", "type": "Group",
			"preferredUsername": "ownera-channel-2",
			"inbox":             srv.URL + "/video-channels/ownera/inbox",
			"publicKey":         map[string]string{"publicKeyPem": "PEM"},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	follow, err := svc.FollowRemoteChannel(context.Background(), userID, "ownera@"+host)
	if err != nil {
		t.Fatalf("FollowRemoteChannel: %v", err)
	}
	if want := srv.URL + "/video-channels/ownera"; follow.ActorURL != want {
		t.Fatalf("followed %q, want the GROUP %q", follow.ActorURL, want)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want one queued Follow", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		if got := d.row.InboxUrl; got != srv.URL+"/video-channels/ownera/inbox" {
			t.Errorf("Follow queued to %q, want the group inbox", got)
		}
	}
	// Two candidates, at most two fetches — both to the host that just answered
	// WebFinger, so this costs nothing a caller could not already make us do.
	if actorFetches > 2 {
		t.Errorf("actor fetches = %d, want at most one per candidate", actorFetches)
	}
}

// A name only ONE kind holds still answers with a single rel=self link, and
// nothing about that path may change — including when the single link is a
// Person, which is what following an account-only handle has always resolved to.
func TestFollowRemoteChannelKeepsTheSingleSelfLink(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/webfinger", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(JRD{
			Subject: "acct:solo@remote.example",
			Links: []JRDLink{
				{Rel: "self", Type: "application/activity+json", Href: srv.URL + "/accounts/solo"},
			},
		})
	})
	mux.HandleFunc("/accounts/solo", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": srv.URL + "/accounts/solo", "type": "Person",
			"preferredUsername": "solo",
			"inbox":             srv.URL + "/accounts/solo/inbox",
			"publicKey":         map[string]string{"publicKeyPem": "PEM"},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	follow, err := svc.FollowRemoteChannel(context.Background(), userID, "solo@"+host)
	if err != nil {
		t.Fatalf("FollowRemoteChannel: %v", err)
	}
	if want := srv.URL + "/accounts/solo"; follow.ActorURL != want {
		t.Fatalf("followed %q, want %q", follow.ActorURL, want)
	}
}

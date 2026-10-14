package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (f fakeRepo) UpsertInstanceFollowing(_ context.Context, a sqlcgen.UpsertInstanceFollowingParams) (sqlcgen.InstanceFollowing, error) {
	if r, ok := f.inst.following[a.RemoteActorUrl]; ok {
		if r.State == "rejected" { // the re-arm the SQL performs
			r.State, r.FollowActivityUrl = "pending", a.FollowActivityUrl
		}
		return *r, nil
	}
	r := &sqlcgen.InstanceFollowing{ID: uuid.New(), RemoteActorUrl: a.RemoteActorUrl, Host: a.Host,
		State: "pending", FollowActivityUrl: a.FollowActivityUrl, CreatedAt: time.Now()}
	f.inst.following[a.RemoteActorUrl] = r
	return *r, nil
}

func (f fakeRepo) ListInstanceFollowing(_ context.Context, a sqlcgen.ListInstanceFollowingParams) ([]sqlcgen.ListInstanceFollowingRow, error) {
	var out []sqlcgen.ListInstanceFollowingRow
	for _, r := range f.inst.following {
		if a.State == nil || *a.State == r.State {
			out = append(out, sqlcgen.ListInstanceFollowingRow{ID: r.ID, RemoteActorUrl: r.RemoteActorUrl, Host: r.Host, State: r.State})
		}
	}
	return out, nil
}

func (f fakeRepo) CountInstanceFollowing(ctx context.Context, state *string) (int64, error) {
	rows, err := f.ListInstanceFollowing(ctx, sqlcgen.ListInstanceFollowingParams{State: state})
	return int64(len(rows)), err
}

func (f fakeRepo) DeleteInstanceFollowingByID(_ context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowingByIDRow, error) {
	for actor, r := range f.inst.following {
		if r.ID == id {
			delete(f.inst.following, actor)
			return sqlcgen.DeleteInstanceFollowingByIDRow{RemoteActorUrl: actor, FollowActivityUrl: r.FollowActivityUrl, State: r.State}, nil
		}
	}
	return sqlcgen.DeleteInstanceFollowingByIDRow{}, pgx.ErrNoRows
}

// setFollowing mirrors the Accept/Reject UPDATEs: matched by our activity id
// AND the signer being the followed actor.
func (f fakeRepo) setFollowing(followURL, signer, to string, from ...string) int64 {
	if f.inst == nil {
		return 0
	}
	r, ok := f.inst.following[signer]
	if !ok || r.FollowActivityUrl != followURL {
		return 0
	}
	for _, s := range from {
		if r.State == s {
			r.State = to
			return 1
		}
	}
	return 0
}

func (f fakeRepo) AcceptInstanceFollowingByActivity(_ context.Context, a sqlcgen.AcceptInstanceFollowingByActivityParams) (int64, error) {
	return f.setFollowing(a.FollowActivityUrl, a.RemoteActorUrl, "accepted", "pending"), nil
}

func (f fakeRepo) RejectInstanceFollowingByActivity(_ context.Context, a sqlcgen.RejectInstanceFollowingByActivityParams) (int64, error) {
	return f.setFollowing(a.FollowActivityUrl, a.RemoteActorUrl, "rejected", "pending", "accepted"), nil
}

func (f fakeRepo) HasAcceptedInstanceFollowingForHost(_ context.Context, host string) (bool, error) {
	if f.inst == nil {
		return false, nil
	}
	for _, r := range f.inst.following {
		if strings.EqualFold(r.Host, host) && r.State == "accepted" {
			return true, nil
		}
	}
	return false, nil
}

// peerInstance is a remote PeerTube-shaped instance: NodeInfo naming its
// Application actor (unless nodeinfo is false — then only WebFinger does), the
// actor, a channel and one video.
func peerInstance(t *testing.T, nodeinfo bool, webfingerTo ...string) *httptest.Server {
	t.Helper()
	pub, _, err := generateActorKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := srv.URL
		actor := func(id, typ string) string {
			return `{"id":"` + id + `","type":"` + typ + `","preferredUsername":"peertube","inbox":"` + id +
				`/inbox","endpoints":{"sharedInbox":"` + o + `/inbox"},"publicKey":{"publicKeyPem":` + quote(pub) + `}}`
		}
		switch r.URL.Path {
		case "/.well-known/nodeinfo":
			links := `[{"rel":"http://nodeinfo.diaspora.software/ns/schema/2.0","href":"` + o + `/nodeinfo/2.0.json"}`
			if nodeinfo {
				links += `,{"rel":"https://www.w3.org/ns/activitystreams#Application","href":"` + o + `/accounts/peertube"}`
			}
			_, _ = w.Write([]byte(`{"links":` + links + `]}`))
		case "/.well-known/webfinger":
			target := "/accounts/peertube"
			if len(webfingerTo) > 0 {
				target = webfingerTo[0]
			}
			_, _ = w.Write([]byte(`{"subject":"acct:peertube@x","links":[{"rel":"self","type":"application/activity+json","href":"` + o + target + `"}]}`))
		case "/accounts/someone":
			_, _ = w.Write([]byte(actor(o+"/accounts/someone", "Person")))
		case "/videos/watch/foreign":
			_, _ = w.Write([]byte(`{"id":"` + o + `/videos/watch/foreign","type":"Video","name":"Not theirs","attributedTo":"https://third.example/video-channels/movies","url":[{"type":"Link","mediaType":"video/mp4","href":"` + o + `/dl/2.mp4"}]}`))
		case "/accounts/peertube":
			_, _ = w.Write([]byte(actor(o+"/accounts/peertube", "Application")))
		case "/video-channels/movies":
			_, _ = w.Write([]byte(actor(o+"/video-channels/movies", "Group")))
		case "/videos/watch/1":
			_, _ = w.Write([]byte(`{"id":"` + o + `/videos/watch/1","type":"Video","name":"Announced clip","attributedTo":"` +
				o + `/video-channels/movies","url":[{"type":"Link","mediaType":"video/mp4","href":"` + o + `/dl/1.mp4"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFollowingRepo() fakeRepo {
	repo := newInstanceInboxRepo()
	repo.remoteActors = map[string]sqlcgen.RemoteActor{}
	repo.remoteVideos = map[string]*fakeRemoteVideo{}
	repo.blockedDomains = map[string]bool{}
	return repo
}

func TestFollowInstanceSendsFollowFromTheInstanceActor(t *testing.T) {
	for name, nodeinfo := range map[string]bool{"nodeinfo": true, "webfinger fallback": false} {
		t.Run(name, func(t *testing.T) {
			peer := peerInstance(t, nodeinfo)
			host := strings.TrimPrefix(peer.URL, "http://")
			repo := newFollowingRepo()
			svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
			f, err := svc.FollowInstance(context.Background(), " "+strings.ToUpper(host)+" ")
			if err != nil {
				t.Fatalf("FollowInstance: %v", err)
			}
			theirs := peer.URL + "/accounts/peertube"
			if f.ActorURL != theirs || f.State != "pending" || f.Host != host || f.Handle != "peertube@"+host {
				t.Errorf("follow = %+v", f)
			}
			d, body := onlyDelivery(t, repo)
			if !d.SigningInstanceActor || d.InboxUrl != theirs+"/inbox" || body["type"] != "Follow" ||
				body["actor"] != ourInstance || body["object"] != theirs {
				t.Errorf("follow delivery = %+v %v", d, body)
			}
			// Idempotent: no second Follow for an instance already followed.
			if _, err := svc.FollowInstance(context.Background(), host); err != nil || len(repo.deliveries) != 1 {
				t.Errorf("re-follow: %v, %d deliveries", err, len(repo.deliveries))
			}
		})
	}
}

func TestFollowInstanceRefusals(t *testing.T) {
	peer := peerInstance(t, true)
	host := strings.TrimPrefix(peer.URL, "http://")
	cases := map[string]struct {
		host  string
		setup func(*fakeRepo)
		want  error
	}{
		"malformed":        {"https://" + host + "/x", nil, ErrBadFollowTarget},
		"handle not host":  {"peertube@" + host, nil, ErrBadFollowTarget},
		"this instance":    {"videos.example", nil, ErrLocalFollowTarget},
		"instance blocked": {host, func(r *fakeRepo) { r.blockedDomains[strings.Split(host, ":")[0]] = true; r.blockedDomains[host] = true }, ErrInstanceBlocked},
		"actor blocked":    {host, func(r *fakeRepo) { r.adminActorBlocks[peer.URL+"/accounts/peertube"] = "" }, ErrInstanceBlocked},
		"actor not served": {host, func(r *fakeRepo) { r.inst.reserved = false }, ErrInstanceActorUnavailable},
		"nothing there":    {"127.0.0.1:1", nil, ErrRemoteUnresolvable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newFollowingRepo()
			if tc.setup != nil {
				tc.setup(&repo)
			}
			svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
			if _, err := svc.FollowInstance(context.Background(), tc.host); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(repo.deliveries) != 0 || len(repo.inst.following) != 0 {
				t.Error("a refused follow must record and send nothing")
			}
		})
	}
}

func TestInstanceFollowingLifecycle(t *testing.T) {
	peer := peerInstance(t, true)
	host := strings.TrimPrefix(peer.URL, "http://")
	theirs := peer.URL + "/accounts/peertube"
	repo := newFollowingRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	ctx := context.Background()
	f, err := svc.FollowInstance(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	follow := repo.inst.following[theirs].FollowActivityUrl
	accept := func(id, signer string) []byte {
		return []byte(`{"id":"` + id + `","type":"Accept","actor":"` + signer + `","object":{"id":"` + follow +
			`","type":"Follow","actor":"` + ourInstance + `","object":"` + theirs + `"}}`)
	}
	// Only the followed actor can accept our follow.
	evil := "https://evil.example/accounts/peertube"
	if err := svc.HandleInbox(ctx, evil, []byte(strings.ReplaceAll(string(accept(evil+"/a/1", evil)), theirs, evil))); err != nil {
		t.Fatal(err)
	}
	if repo.inst.following[theirs].State != "pending" {
		t.Fatal("an Accept signed by another actor accepted our follow")
	}
	if err := svc.HandleInbox(ctx, theirs, accept(peer.URL+"/accepts/1", theirs)); err != nil {
		t.Fatal(err)
	}
	if repo.inst.following[theirs].State != "accepted" {
		t.Fatalf("state = %s, want accepted", repo.inst.following[theirs].State)
	}
	if list, total, err := svc.ListInstanceFollowing(ctx, "accepted", 20, 0); err != nil || total != 1 || list[0].ID != f.ID {
		t.Errorf("list = %+v %d %v", list, total, err)
	}
	// Unfollow: gone at once, and an Undo goes to their inbox as the instance actor.
	if err := svc.UnfollowInstance(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.inst.following[theirs]; ok {
		t.Error("the follow survived an unfollow")
	}
	undos := sent(t, repo)["Undo"]
	if len(undos) != 1 || !undos[0].SigningInstanceActor || undos[0].InboxUrl != theirs+"/inbox" {
		t.Errorf("undo = %+v", undos)
	}
	if err := svc.UnfollowInstance(ctx, f.ID); !errors.Is(err, ErrInstanceFollowNotFound) {
		t.Errorf("second unfollow: %v", err)
	}
}

// A followed instance's Announce is ingested like a followed channel's — and
// only while the follow stands and the announcer is not blocked.
func TestAnnounceFromAFollowedInstanceIsIngested(t *testing.T) {
	peer := peerInstance(t, true)
	host := strings.TrimPrefix(peer.URL, "http://")
	theirs := peer.URL + "/accounts/peertube"
	video := peer.URL + "/videos/watch/1"
	announce := []byte(`{"id":"` + peer.URL + `/announces/1","type":"Announce","actor":"` + theirs + `","object":"` + video + `"}`)
	for name, tc := range map[string]struct {
		state   string
		blocked bool
		want    bool
	}{
		"accepted":          {"accepted", false, true},
		"pending":           {"pending", false, false},
		"announcer blocked": {"accepted", true, false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFollowingRepo()
			repo.inst.following[theirs] = &sqlcgen.InstanceFollowing{ID: uuid.New(), RemoteActorUrl: theirs, Host: host, State: tc.state}
			if tc.blocked {
				repo.adminActorBlocks[theirs] = ""
			}
			svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
			if err := svc.HandleInbox(context.Background(), theirs, announce); err != nil {
				t.Fatal(err)
			}
			if _, got := repo.remoteVideos[video]; got != tc.want {
				t.Errorf("ingested = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRejectedInstanceFollowCanBeRearmed(t *testing.T) {
	peer := peerInstance(t, true)
	host := strings.TrimPrefix(peer.URL, "http://")
	theirs := peer.URL + "/accounts/peertube"
	repo := newFollowingRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if _, err := svc.FollowInstance(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	first := repo.inst.following[theirs].FollowActivityUrl
	reject, _ := json.Marshal(map[string]any{"id": peer.URL + "/rejects/1", "type": "Reject", "actor": theirs,
		"object": map[string]any{"id": first, "type": "Follow", "actor": ourInstance, "object": theirs}})
	if err := svc.HandleInbox(context.Background(), theirs, reject); err != nil {
		t.Fatal(err)
	}
	if repo.inst.following[theirs].State != "rejected" {
		t.Fatal("a Reject must leave the follow visible as rejected")
	}
	if f, err := svc.FollowInstance(context.Background(), host); err != nil || f.State != "pending" || len(sent(t, repo)["Follow"]) != 2 {
		t.Errorf("re-follow after a Reject = %+v, %v; want pending with a fresh Follow", f, err)
	}
}

// WebFinger acct:peertube@host can name anything; only an instance actor is
// followed as an instance.
func TestFollowInstanceRefusesANonInstanceActor(t *testing.T) {
	peer := peerInstance(t, false, "/accounts/someone")
	repo := newFollowingRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if _, err := svc.FollowInstance(context.Background(), strings.TrimPrefix(peer.URL, "http://")); !errors.Is(err, ErrRemoteUnresolvable) {
		t.Errorf("err = %v, want ErrRemoteUnresolvable", err)
	}
	if len(repo.inst.following) != 0 || len(repo.deliveries) != 0 {
		t.Error("a refused follow must record and send nothing")
	}
}

// A followed instance cannot announce a video attributed to a THIRD host: the
// follow is consent to its own content, not to whatever it relays.
func TestAnnounceOfAThirdHostsVideoIsRejected(t *testing.T) {
	peer := peerInstance(t, true)
	host := strings.TrimPrefix(peer.URL, "http://")
	theirs := peer.URL + "/accounts/peertube"
	repo := newFollowingRepo()
	repo.inst.following[theirs] = &sqlcgen.InstanceFollowing{ID: uuid.New(), RemoteActorUrl: theirs, Host: host, State: "accepted"}
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	video := peer.URL + "/videos/watch/foreign"
	announce := []byte(`{"id":"` + peer.URL + `/announces/9","type":"Announce","actor":"` + theirs + `","object":"` + video + `"}`)
	if err := svc.HandleInbox(context.Background(), theirs, announce); err != nil {
		t.Fatal(err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Errorf("ingested %v; a third host's video must be refused", repo.remoteVideos)
	}
}

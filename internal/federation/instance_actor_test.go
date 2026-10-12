package federation

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeInstance is the in-memory 0166 state behind the instance actor. A nil
// fakeRepo.inst reads as an instance where an account held `peertube` before
// 0166: the reservation is not the instance actor's, so it is not served.
type fakeInstance struct {
	reserved          bool
	key               *sqlcgen.GetInstanceActorKeyRow
	acceptedFollowers int64
	acceptedFollowing int64
	// followers are instance_followers rows, keyed by remote actor url.
	followers map[string]*sqlcgen.GetInstanceFollowerByActorRow
	// outbox is ListInstanceOutboxVideoIDs' answer, newest first.
	outbox []uuid.UUID
	// backfilledAt is instance_follower_backfills, keyed by remote actor url.
	backfilledAt map[string]time.Time
}

// ClaimInstanceFollowerBackfill mirrors the SQL claim: absent or older than the
// cooldown → claimed (and stamped now); inside the window → 0.
func (f fakeRepo) ClaimInstanceFollowerBackfill(_ context.Context, a sqlcgen.ClaimInstanceFollowerBackfillParams) (int64, error) {
	if f.inst.backfilledAt == nil {
		f.inst.backfilledAt = map[string]time.Time{}
	}
	if at, ok := f.inst.backfilledAt[a.RemoteActorUrl]; ok && time.Since(at) < time.Duration(a.CooldownSeconds)*time.Second {
		return 0, nil
	}
	f.inst.backfilledAt[a.RemoteActorUrl] = time.Now()
	return 1, nil
}

// ListAcceptedInstanceFollowerInboxes mirrors the SQL: accepted followers'
// cached inboxes, shared inbox preferred, distinct, ordered.
func (f fakeRepo) ListAcceptedInstanceFollowerInboxes(context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for actor, r := range f.inst.followers {
		ra, ok := f.remoteActors[actor]
		if !ok || r.State != "accepted" {
			continue
		}
		in := ra.InboxUrl
		if ra.SharedInboxUrl != nil {
			in = *ra.SharedInboxUrl
		}
		if in != "" && !seen[in] {
			seen[in] = true
			out = append(out, in)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f fakeRepo) ListInstanceOutboxVideoIDs(_ context.Context, a sqlcgen.ListInstanceOutboxVideoIDsParams) ([]uuid.UUID, error) {
	lo := min(int(a.ResultOffset), len(f.inst.outbox))
	hi := min(lo+int(a.ResultLimit), len(f.inst.outbox))
	return f.inst.outbox[lo:hi], nil
}

func (f fakeRepo) CountInstanceOutboxVideos(context.Context) (int64, error) {
	return int64(len(f.inst.outbox)), nil
}

func (f fakeRepo) InstanceActorNameReserved(_ context.Context, name string) (bool, error) {
	return f.inst != nil && f.inst.reserved && strings.EqualFold(name, InstanceActorName), nil
}

func (f fakeRepo) GetInstanceActorKey(context.Context) (sqlcgen.GetInstanceActorKeyRow, error) {
	if f.inst == nil || f.inst.key == nil {
		return sqlcgen.GetInstanceActorKeyRow{}, pgx.ErrNoRows
	}
	return *f.inst.key, nil
}

func (f fakeRepo) InsertInstanceActorKeyIfAbsent(_ context.Context, a sqlcgen.InsertInstanceActorKeyIfAbsentParams) (int64, error) {
	if f.inst.key != nil {
		return 0, nil // ON CONFLICT (singleton) DO NOTHING
	}
	f.inst.key = &sqlcgen.GetInstanceActorKeyRow{PublicKeyPem: a.PublicKeyPem, PrivateKeyPem: a.PrivateKeyPem}
	return 1, nil
}

func (f fakeRepo) CountAcceptedInstanceFollowers(context.Context) (int64, error) {
	if f.inst == nil {
		return 0, nil
	}
	return f.inst.acceptedFollowers, nil
}

func (f fakeRepo) CountAcceptedInstanceFollowing(context.Context) (int64, error) {
	if f.inst == nil {
		return 0, nil
	}
	return f.inst.acceptedFollowing, nil
}

func (f fakeRepo) InsertInstanceFollowerIfAbsent(_ context.Context, a sqlcgen.InsertInstanceFollowerIfAbsentParams) (int64, error) {
	if _, ok := f.inst.followers[a.RemoteActorUrl]; ok {
		return 0, nil
	}
	f.inst.followers[a.RemoteActorUrl] = &sqlcgen.GetInstanceFollowerByActorRow{ID: uuid.New(), State: a.State, FollowActivityUrl: a.FollowActivityUrl}
	return 1, nil
}

func (f fakeRepo) GetInstanceFollowerByActor(_ context.Context, actor string) (sqlcgen.GetInstanceFollowerByActorRow, error) {
	if r, ok := f.inst.followers[actor]; ok {
		return *r, nil
	}
	return sqlcgen.GetInstanceFollowerByActorRow{}, pgx.ErrNoRows
}

func (f fakeRepo) DeleteInstanceFollowerByActor(_ context.Context, actor string) (int64, error) {
	if r, ok := f.inst.followers[actor]; !ok || r.State == "rejected" {
		return 0, nil // the SQL keeps a rejected follower rejected
	}
	delete(f.inst.followers, actor)
	return 1, nil
}

func newInstanceRepo() fakeRepo {
	repo := newActorRepo()
	repo.inst = &fakeInstance{reserved: true, followers: map[string]*sqlcgen.GetInstanceFollowerByActorRow{}}
	return repo
}

// ptActorNameRe is PeerTube's actorNameAlphabet (actor.ts): what
// isActorPreferredUsernameValid accepts.
var ptActorNameRe = regexp.MustCompile(`^[A-Za-z0-9\-_.:]+$`)

// ptCheckActor ports sanitizeAndCheckActorObject (custom-validators/
// activitypub/actor.ts) — the gate PeerTube's fetchRemoteActor applies before
// it will create, follow or Accept an actor. It runs on the WIRE JSON.
func ptCheckActor(t *testing.T, raw []byte) []string {
	t.Helper()
	var a map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("actor json: %v", err)
	}
	var bad []string
	switch a["type"] {
	case "Person", "Application", "Group", "Service", "Organization":
	default:
		bad = append(bad, "type")
	}
	for _, k := range []string{"id", "inbox", "outbox", "following", "followers"} {
		if !ptIsAPURL(a[k]) {
			bad = append(bad, k)
		}
	}
	if name, _ := a["preferredUsername"].(string); !ptActorNameRe.MatchString(name) {
		bad = append(bad, "preferredUsername")
	}
	pk, _ := a["publicKey"].(map[string]any)
	pem, _ := pk["publicKeyPem"].(string)
	if !ptIsAPURL(pk["id"]) || !ptIsAPURL(pk["owner"]) || pk["owner"] != a["id"] ||
		!strings.HasPrefix(pem, "-----BEGIN PUBLIC KEY-----") || !strings.Contains(pem, "-----END PUBLIC KEY-----") {
		bad = append(bad, "publicKey")
	}
	if ep, ok := a["endpoints"].(map[string]any); ok && ep["sharedInbox"] != nil && !ptIsAPURL(ep["sharedInbox"]) {
		bad = append(bad, "endpoints.sharedInbox")
	}
	return bad
}

func TestInstanceActorIsPeerTubesApplicationActor(t *testing.T) {
	svc := NewService(newInstanceRepo(), WithBaseURL("https://videos.example"))
	actor, err := svc.AccountActor(context.Background(), "peertube")
	if err != nil {
		t.Fatalf("AccountActor(peertube): %v", err)
	}
	const id = "https://videos.example/accounts/peertube"
	if actor.Type != "Application" || actor.ID != id || actor.PreferredUsername != "peertube" {
		t.Fatalf("actor = %s %s %s, want Application %s peertube", actor.Type, actor.ID, actor.PreferredUsername, id)
	}
	if actor.Inbox != id+"/inbox" || actor.Outbox != id+"/outbox" || actor.PublicKey.ID != id+"#main-key" {
		t.Errorf("inbox/outbox/key = %s %s %s", actor.Inbox, actor.Outbox, actor.PublicKey.ID)
	}
	if actor.URL != "https://videos.example/about/instance" {
		t.Errorf("url = %q, want the human about page", actor.URL)
	}
	raw, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	if bad := ptCheckActor(t, raw); len(bad) != 0 {
		t.Errorf("PeerTube's actor validator would refuse it: %v\n%s", bad, raw)
	}
	if svc.InstanceActorURL() != id {
		t.Errorf("InstanceActorURL = %q", svc.InstanceActorURL())
	}
}

func TestInstanceActorKeyIsMintedOnce(t *testing.T) {
	repo := newInstanceRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	a1, err := svc.InstanceActor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a2, err := svc.InstanceActor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a1.PublicKey.PublicKeyPem == "" || a1.PublicKey.PublicKeyPem != a2.PublicKey.PublicKeyPem {
		t.Error("the instance key must be minted once and reused")
	}
	signer, err := svc.instanceActorSigner(context.Background())
	if err != nil {
		t.Fatalf("instanceActorSigner: %v", err)
	}
	if signer.KeyID != "https://videos.example/accounts/peertube#main-key" || signer.Priv == nil {
		t.Errorf("signer = %q", signer.KeyID)
	}
}

// An instance where an account held `peertube` before 0166 keeps that account:
// a username is a sign-in identifier and is never taken away.
func TestInstanceActorYieldsToAnAccountThatHeldTheName(t *testing.T) {
	repo := newActorRepo() // inst == nil: the reservation is the account's
	repo.usersByName["peertube"] = sqlcgen.GetUserActorByUsernameRow{ID: repo.usersByName["ada"].ID, Username: "peertube"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	actor, err := svc.AccountActor(context.Background(), "peertube")
	if err != nil {
		t.Fatal(err)
	}
	if actor.Type != "Person" {
		t.Errorf("type = %s, want the account's Person", actor.Type)
	}
	if _, err := svc.InstanceActor(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("InstanceActor err = %v, want ErrNotFound", err)
	}
	if _, ok := svc.InstanceActorAvailable(context.Background()); ok {
		t.Error("the instance actor must report itself unavailable")
	}
}

func TestWebFingerResolvesTheInstanceActor(t *testing.T) {
	svc := NewService(newInstanceRepo(), WithBaseURL("https://videos.example"))
	jrd, err := svc.WebFinger(context.Background(), "acct:peertube@videos.example")
	if err != nil {
		t.Fatalf("WebFinger: %v", err)
	}
	links := actorLinks(jrd)
	if len(links) != 1 || links[0].Rel != "self" || links[0].Href != "https://videos.example/accounts/peertube" {
		t.Errorf("links = %+v", jrd.Links)
	}
	off := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	if _, err := off.WebFinger(context.Background(), "acct:peertube@videos.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unreserved name: err = %v, want ErrNotFound", err)
	}
}

func TestInstanceActorCollections(t *testing.T) {
	repo := newInstanceRepo()
	repo.inst.acceptedFollowers, repo.inst.acceptedFollowing = 3, 2
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	for kind, want := range map[string]int64{"followers": 3, "following": 2} {
		col, err := svc.AccountCollection(context.Background(), "peertube", kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if col.ID != "https://videos.example/accounts/peertube/"+kind || col.TotalItems != want {
			t.Errorf("%s = %s %d, want %d", kind, col.ID, col.TotalItems, want)
		}
	}
	if _, err := svc.AccountCollection(context.Background(), "peertube", "likes"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown kind: err = %v", err)
	}
}

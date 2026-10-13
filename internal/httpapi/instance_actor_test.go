package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// instanceFedRepo is fakeFedRepo on an instance whose 0166 reservation of
// `peertube` is the instance actor's (federation.InstanceRepository).
type instanceFedRepo struct {
	fakeFedRepo
	key *sqlcgen.GetInstanceActorKeyRow
}

func (r *instanceFedRepo) InstanceActorNameReserved(_ context.Context, name string) (bool, error) {
	return strings.EqualFold(name, federation.InstanceActorName), nil
}

func (r *instanceFedRepo) GetInstanceActorKey(context.Context) (sqlcgen.GetInstanceActorKeyRow, error) {
	if r.key == nil {
		return sqlcgen.GetInstanceActorKeyRow{}, pgx.ErrNoRows
	}
	return *r.key, nil
}

func (r *instanceFedRepo) InsertInstanceActorKeyIfAbsent(_ context.Context, a sqlcgen.InsertInstanceActorKeyIfAbsentParams) (int64, error) {
	if r.key != nil {
		return 0, nil
	}
	r.key = &sqlcgen.GetInstanceActorKeyRow{PublicKeyPem: a.PublicKeyPem, PrivateKeyPem: a.PrivateKeyPem}
	return 1, nil
}

func (r *instanceFedRepo) CountAcceptedInstanceFollowers(context.Context) (int64, error) {
	return 0, nil
}
func (r *instanceFedRepo) CountAcceptedInstanceFollowing(context.Context) (int64, error) {
	return 0, nil
}

func (r *instanceFedRepo) InsertInstanceFollowerIfAbsent(context.Context, sqlcgen.InsertInstanceFollowerIfAbsentParams) (int64, error) {
	return 0, nil
}
func (r *instanceFedRepo) GetInstanceFollowerByActor(context.Context, string) (sqlcgen.GetInstanceFollowerByActorRow, error) {
	return sqlcgen.GetInstanceFollowerByActorRow{}, pgx.ErrNoRows
}
func (r *instanceFedRepo) DeleteInstanceFollowerByActor(context.Context, string) (int64, error) {
	return 0, nil
}

func instanceFedServer() *Server {
	cfg := fedTestConfig()
	repo := &instanceFedRepo{fakeFedRepo: newFedRepoFor(cfg)}
	return New(cfg, nil, nil, WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))))
}

// PeerTube finds the actor to follow "by host" through this link
// (getApplicationActorOfHost) before it falls back to WebFinger.
func TestNodeInfoDiscoveryNamesTheInstanceActor(t *testing.T) {
	rec := get(t, instanceFedServer(), "/.well-known/nodeinfo")
	var body nodeInfoDiscovery
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Links) != 2 {
		t.Fatalf("links = %+v, want nodeinfo + Application", body.Links)
	}
	if l := body.Links[1]; l.Rel != "https://www.w3.org/ns/activitystreams#Application" ||
		l.Href != "https://videos.example/accounts/peertube" {
		t.Errorf("application link = %+v", l)
	}
}

func TestInstanceActorIsServed(t *testing.T) {
	srv := instanceFedServer()
	rec := getAccept(t, srv, "/accounts/peertube", "application/activity+json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var actor federation.Actor
	if err := json.Unmarshal(rec.Body.Bytes(), &actor); err != nil {
		t.Fatal(err)
	}
	if actor.Type != "Application" || actor.ID != "https://videos.example/accounts/peertube" {
		t.Errorf("actor = %s %s", actor.Type, actor.ID)
	}
	// A browser is sent to the instance's about page, not to a profile that
	// does not exist.
	if rec := getAccept(t, srv, "/accounts/peertube", "text/html"); rec.Code != http.StatusFound ||
		rec.Header().Get("Location") != "/about/instance" {
		t.Errorf("html = %d %q, want 302 /about/instance", rec.Code, rec.Header().Get("Location"))
	}
	if rec := getAccept(t, srv, "/accounts/peertube/followers", "application/activity+json"); rec.Code != http.StatusOK {
		t.Errorf("followers collection = %d, want 200", rec.Code)
	}
}

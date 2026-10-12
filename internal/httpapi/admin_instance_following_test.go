package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (r *instanceFedRepo) UpsertInstanceFollowing(_ context.Context, a sqlcgen.UpsertInstanceFollowingParams) (sqlcgen.InstanceFollowing, error) {
	if r.following == nil {
		r.following = map[uuid.UUID]*sqlcgen.InstanceFollowing{}
	}
	f := &sqlcgen.InstanceFollowing{ID: uuid.New(), RemoteActorUrl: a.RemoteActorUrl, Host: a.Host, State: "pending", FollowActivityUrl: a.FollowActivityUrl}
	r.following[f.ID] = f
	return *f, nil
}

func (r *instanceFedRepo) ListInstanceFollowing(context.Context, sqlcgen.ListInstanceFollowingParams) ([]sqlcgen.ListInstanceFollowingRow, error) {
	var out []sqlcgen.ListInstanceFollowingRow
	for _, f := range r.following {
		out = append(out, sqlcgen.ListInstanceFollowingRow{ID: f.ID, RemoteActorUrl: f.RemoteActorUrl, Host: f.Host, State: f.State, PreferredUsername: "peertube"})
	}
	return out, nil
}

func (r *instanceFedRepo) CountInstanceFollowing(context.Context, *string) (int64, error) {
	return int64(len(r.following)), nil
}

func (r *instanceFedRepo) DeleteInstanceFollowingByID(_ context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowingByIDRow, error) {
	f, ok := r.following[id]
	if !ok {
		return sqlcgen.DeleteInstanceFollowingByIDRow{}, pgx.ErrNoRows
	}
	delete(r.following, id)
	return sqlcgen.DeleteInstanceFollowingByIDRow{RemoteActorUrl: f.RemoteActorUrl, FollowActivityUrl: f.FollowActivityUrl, State: f.State}, nil
}

func (r *instanceFedRepo) AcceptInstanceFollowingByActivity(context.Context, sqlcgen.AcceptInstanceFollowingByActivityParams) (int64, error) {
	return 0, nil
}

func (r *instanceFedRepo) RejectInstanceFollowingByActivity(context.Context, sqlcgen.RejectInstanceFollowingByActivityParams) (int64, error) {
	return 0, nil
}

func (r *instanceFedRepo) HasAcceptedInstanceFollowingForHost(context.Context, string) (bool, error) {
	return false, nil
}

// UpsertRemoteActor keeps the inbox the base fake drops: following needs it.
func (r *instanceFedRepo) UpsertRemoteActor(_ context.Context, a sqlcgen.UpsertRemoteActorParams) error {
	r.remoteActors[a.ActorUrl] = sqlcgen.RemoteActor{ActorUrl: a.ActorUrl, ActorType: a.ActorType,
		PreferredUsername: a.PreferredUsername, Domain: a.Domain, InboxUrl: a.InboxUrl, PublicKeyPem: a.PublicKeyPem}
	return nil
}

// peerInstanceServer is a remote instance whose NodeInfo names its
// Application actor.
func peerInstanceServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub, _ := json.Marshal(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/nodeinfo":
			_, _ = w.Write([]byte(`{"links":[{"rel":"https://www.w3.org/ns/activitystreams#Application","href":"` + srv.URL + `/accounts/peertube"}]}`))
		case "/accounts/peertube":
			_, _ = w.Write([]byte(`{"id":"` + srv.URL + `/accounts/peertube","type":"Application","preferredUsername":"peertube","inbox":"` +
				srv.URL + `/accounts/peertube/inbox","publicKey":{"publicKeyPem":` + string(pub) + `}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAdminFollowsAndUnfollowsAnInstance(t *testing.T) {
	var buf bytes.Buffer
	peer := peerInstanceServer(t)
	host := strings.TrimPrefix(peer.URL, "http://")
	srv, repo, _, admin := instanceFollowsServer(t, &buf, federation.WithAllowPrivateFetch(true))
	const base = "/api/v1/admin/federation/instance-following"

	rec := sendJSONAuth(srv, http.MethodPost, base, `{"host":"`+host+`"}`, admin)
	var f instanceFollowView
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("follow = %d %s", rec.Code, rec.Body.String())
	}
	if f.Host != host || f.State != "pending" || f.ActorURL != peer.URL+"/accounts/peertube" {
		t.Errorf("follow = %+v", f)
	}
	if d, ok := repo.deliveries[peer.URL+"/accounts/peertube/inbox"]; !ok || !d.SigningInstanceActor {
		t.Errorf("no Follow queued as the instance actor: %+v", repo.deliveries)
	}
	var list instanceFollowListResponse
	if rec := getWithAuth(srv, base, admin); json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Follows) != 1 {
		t.Errorf("list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, base+"/"+f.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("unfollow = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, base+"/"+f.ID, "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("second unfollow = %d, want 404", rec.Code)
	}
	events := auditEvents(t, &buf)
	for _, action := range []string{observability.ActionFederationInstanceFollow, observability.ActionFederationInstanceUnfollow} {
		if findAudit(events, action, observability.ResultSuccess) == nil {
			t.Errorf("no audit event %s", action)
		}
	}
}

func TestFollowInstanceRefusals(t *testing.T) {
	var buf bytes.Buffer
	srv, repo, _, admin := instanceFollowsServer(t, &buf)
	repo.blockedDomains = map[string]bool{"blocked.example": true}
	user := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	const base = "/api/v1/admin/federation/instance-following"
	for body, want := range map[string]int{
		`{"host":"bad/host"}`:        http.StatusUnprocessableEntity,
		`{"host":"videos.example"}`:  http.StatusUnprocessableEntity,
		`{"host":"blocked.example"}`: http.StatusConflict,
	} {
		if rec := sendJSONAuth(srv, http.MethodPost, base, body, admin); rec.Code != want {
			t.Errorf("%s = %d, want %d", body, rec.Code, want)
		}
	}
	if rec := sendJSONAuth(srv, http.MethodPost, base, `{"host":"pt.example"}`, user); rec.Code != http.StatusForbidden {
		t.Errorf("as a user = %d, want 403", rec.Code)
	}
	if len(repo.deliveries) != 0 {
		t.Error("a refused follow must send nothing")
	}
}

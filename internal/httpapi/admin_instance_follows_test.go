package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (r *instanceFedRepo) ListInstanceFollowers(_ context.Context, a sqlcgen.ListInstanceFollowersParams) ([]sqlcgen.ListInstanceFollowersRow, error) {
	var out []sqlcgen.ListInstanceFollowersRow
	for _, f := range r.followers {
		if a.State == nil || *a.State == f.State {
			out = append(out, *f)
		}
	}
	return out, nil
}

func (r *instanceFedRepo) CountInstanceFollowers(ctx context.Context, state *string) (int64, error) {
	rows, _ := r.ListInstanceFollowers(ctx, sqlcgen.ListInstanceFollowersParams{State: state})
	return int64(len(rows)), nil
}

// setState flips a follower whose state is one of from, as the SQL's WHERE does.
func (r *instanceFedRepo) setState(id uuid.UUID, to string, from ...string) (*sqlcgen.ListInstanceFollowersRow, error) {
	f, ok := r.followers[id]
	if ok {
		for _, s := range from {
			if f.State == s {
				f.State = to
				return f, nil
			}
		}
	}
	return nil, pgx.ErrNoRows
}

func (r *instanceFedRepo) AcceptPendingInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.AcceptPendingInstanceFollowerByIDRow, error) {
	f, err := r.setState(id, "accepted", "pending")
	if err != nil {
		return sqlcgen.AcceptPendingInstanceFollowerByIDRow{}, err
	}
	return sqlcgen.AcceptPendingInstanceFollowerByIDRow{RemoteActorUrl: f.RemoteActorUrl, FollowActivityUrl: "follow-" + id.String()}, nil
}

func (r *instanceFedRepo) RejectInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.RejectInstanceFollowerByIDRow, error) {
	f, err := r.setState(id, "rejected", "pending", "accepted")
	if err != nil {
		return sqlcgen.RejectInstanceFollowerByIDRow{}, err
	}
	return sqlcgen.RejectInstanceFollowerByIDRow{RemoteActorUrl: f.RemoteActorUrl, FollowActivityUrl: "follow-" + id.String()}, nil
}

func (r *instanceFedRepo) DeleteInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowerByIDRow, error) {
	f, ok := r.followers[id]
	if !ok {
		return sqlcgen.DeleteInstanceFollowerByIDRow{}, pgx.ErrNoRows
	}
	delete(r.followers, id)
	return sqlcgen.DeleteInstanceFollowerByIDRow{RemoteActorUrl: f.RemoteActorUrl, FollowActivityUrl: "follow-" + id.String(), State: f.State}, nil
}

// A missing method would silently switch the instance actor off (NewService
// picks the surface up by type assertion); make it a compile error instead.
var _ federation.InstanceRepository = (*instanceFedRepo)(nil)

const ptActor = "https://pt.example/accounts/peertube"

// instanceFollowsServer is an auth-enabled server (first account = admin) whose
// instance has one PENDING instance follower; audit events land in buf.
func instanceFollowsServer(t *testing.T, buf *bytes.Buffer) (*Server, *instanceFedRepo, uuid.UUID, string) {
	t.Helper()
	cfg := fedTestConfig()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	id := uuid.New()
	repo := &instanceFedRepo{fakeFedRepo: newFedRepoFor(cfg), followers: map[uuid.UUID]*sqlcgen.ListInstanceFollowersRow{
		id: {ID: id, RemoteActorUrl: ptActor, State: "pending", PreferredUsername: "peertube", Domain: "pt.example", CreatedAt: time.Now()},
	}}
	repo.remoteActors[ptActor] = sqlcgen.RemoteActor{ActorUrl: ptActor, InboxUrl: ptActor + "/inbox"}
	fedsvc := federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))
	srv := New(cfg, nil, nil, WithAuthService(authsvc, 15*time.Minute), WithFederationService(fedsvc),
		WithLogger(slog.New(slog.NewJSONHandler(buf, nil))))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	return srv, repo, id, admin
}

func TestAdminDecidesInstanceFollowers(t *testing.T) {
	var buf bytes.Buffer
	srv, repo, id, admin := instanceFollowsServer(t, &buf)
	const base = "/api/v1/admin/federation/instance-followers"

	rec := getWithAuth(srv, base+"?state=pending", admin)
	var list instanceFollowListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Follows) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	if f := list.Follows[0]; f.ID != id.String() || f.Handle != "peertube@pt.example" || f.Host != "pt.example" || f.State != "pending" {
		t.Errorf("follow = %+v", f)
	}
	if rec := getWithAuth(srv, base+"?state=bogus", admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad state = %d, want 422", rec.Code)
	}

	if rec := postWithAuth(srv, base+"/"+id.String()+"/accept", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("accept = %d %s", rec.Code, rec.Body.String())
	}
	if d, ok := repo.deliveries[ptActor+"/inbox"]; !ok || !d.SigningInstanceActor {
		t.Errorf("the Accept was not queued as the instance actor: %+v", repo.deliveries)
	}
	if rec := postWithAuth(srv, base+"/"+id.String()+"/accept", admin); rec.Code != http.StatusNotFound {
		t.Errorf("accepting an accepted follower = %d, want 404", rec.Code)
	}
	if rec := postWithAuth(srv, base+"/"+id.String()+"/reject", admin); rec.Code != http.StatusNoContent || repo.followers[id].State != "rejected" {
		t.Errorf("reject = %d, state %s", rec.Code, repo.followers[id].State)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, base+"/"+id.String(), "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("remove = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, base+"/"+id.String(), "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("second remove = %d, want 404", rec.Code)
	}
	events := auditEvents(t, &buf)
	for _, action := range []string{observability.ActionFederationInstanceFollowerAccept,
		observability.ActionFederationInstanceFollowerReject, observability.ActionFederationInstanceFollowerRemove} {
		if e := findAudit(events, action, observability.ResultSuccess); e == nil {
			t.Errorf("no audit event %s", action)
		}
	}
}

func TestInstanceFollowersAreAdminOnly(t *testing.T) {
	var buf bytes.Buffer
	srv, _, id, _ := instanceFollowsServer(t, &buf)
	user := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	const base = "/api/v1/admin/federation/instance-followers"
	for name, rec := range map[string]int{
		"list":   getWithAuth(srv, base, user).Code,
		"accept": postWithAuth(srv, base+"/"+id.String()+"/accept", user).Code,
		"reject": postWithAuth(srv, base+"/"+id.String()+"/reject", user).Code,
		"remove": sendJSONAuth(srv, http.MethodDelete, base+"/"+id.String(), "", user).Code,
		"anon":   getWithAuth(srv, base, "").Code,
	} {
		if want := http.StatusForbidden; name == "anon" && rec != http.StatusUnauthorized || name != "anon" && rec != want {
			t.Errorf("%s = %d", name, rec)
		}
	}
}

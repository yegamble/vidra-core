package httpapi

// Handler tests for the W12 admin federation follower-approval queue:
// GET /admin/federation/follower-requests + POST .../{id}/approve|reject,
// modeled on the registration-requests suite. The federation service runs over
// an in-memory fake seeded with pending remote_follows rows.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// followerQueueRepo extends fakeFedRepo with a mutable pending-follower queue
// and a delivery log (the base fake's stubs answer empty/ErrNoRows).
type followerQueueRepo struct {
	fakeFedRepo
	pending    map[uuid.UUID]*sqlcgen.ListPendingRemoteFollowsRow
	accepted   map[uuid.UUID]bool
	channel    sqlcgen.Channel
	deliverLog *[]sqlcgen.EnqueueDeliveryParams
}

func (f followerQueueRepo) ListPendingRemoteFollows(_ context.Context, _ sqlcgen.ListPendingRemoteFollowsParams) ([]sqlcgen.ListPendingRemoteFollowsRow, error) {
	var out []sqlcgen.ListPendingRemoteFollowsRow
	for _, r := range f.pending {
		out = append(out, *r)
	}
	return out, nil
}

func (f followerQueueRepo) AcceptPendingRemoteFollowByID(_ context.Context, id uuid.UUID) (sqlcgen.AcceptPendingRemoteFollowByIDRow, error) {
	r, ok := f.pending[id]
	if !ok {
		return sqlcgen.AcceptPendingRemoteFollowByIDRow{}, pgx.ErrNoRows
	}
	delete(f.pending, id)
	f.accepted[id] = true
	return sqlcgen.AcceptPendingRemoteFollowByIDRow{
		ChannelID: r.ChannelID, RemoteActorUrl: r.RemoteActorUrl, FollowActivityUrl: r.FollowActivityUrl,
	}, nil
}

func (f followerQueueRepo) DeletePendingRemoteFollowByID(_ context.Context, id uuid.UUID) (sqlcgen.DeletePendingRemoteFollowByIDRow, error) {
	r, ok := f.pending[id]
	if !ok {
		return sqlcgen.DeletePendingRemoteFollowByIDRow{}, pgx.ErrNoRows
	}
	delete(f.pending, id)
	return sqlcgen.DeletePendingRemoteFollowByIDRow{
		ChannelID: r.ChannelID, RemoteActorUrl: r.RemoteActorUrl, FollowActivityUrl: r.FollowActivityUrl,
	}, nil
}

func (f followerQueueRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	if id == f.channel.ID {
		return f.channel, nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}

func (f followerQueueRepo) GetRemoteActor(_ context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	return sqlcgen.RemoteActor{
		ActorUrl: actorURL, PreferredUsername: "kaisa", Domain: "mastodon.example",
		InboxUrl: actorURL + "/inbox",
	}, nil
}

func (f followerQueueRepo) EnqueueDelivery(_ context.Context, arg sqlcgen.EnqueueDeliveryParams) error {
	*f.deliverLog = append(*f.deliverLog, arg)
	return nil
}

// followerQueueServer builds an auth-enabled server with the follower queue
// seeded with one pending request; returns the server, an admin token, the
// pending row id, and the delivery log.
func followerQueueServer(t *testing.T) (*Server, string, uuid.UUID, *[]sqlcgen.EnqueueDeliveryParams) {
	t.Helper()
	cfg := testConfig()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)

	channelID := uuid.New()
	rowID := uuid.New()
	deliveries := []sqlcgen.EnqueueDeliveryParams{}
	repo := followerQueueRepo{
		fakeFedRepo: fakeFedRepo{
			chanKeys:     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
			acctKeys:     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
			remoteActors: map[string]sqlcgen.RemoteActor{},
			processed:    map[string]bool{},
		},
		pending: map[uuid.UUID]*sqlcgen.ListPendingRemoteFollowsRow{
			rowID: {
				ID: rowID, ChannelID: channelID,
				RemoteActorUrl:    "https://mastodon.example/users/kaisa",
				FollowActivityUrl: "https://mastodon.example/follows/1",
				CreatedAt:         time.Now(),
				ChannelHandle:     "films", PreferredUsername: "kaisa", Domain: "mastodon.example",
			},
		},
		accepted:   map[uuid.UUID]bool{},
		channel:    sqlcgen.Channel{ID: channelID, Handle: "films", DisplayName: "Films"},
		deliverLog: &deliveries,
	}
	fedsvc := federation.NewService(repo, federation.WithBaseURL("https://videos.example"))
	srv := New(cfg, nil, nil, WithAuthService(authsvc, 15*time.Minute), WithFederationService(fedsvc))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	return srv, admin, rowID, &deliveries
}

func TestFederationFollowerQueueListApproveReject(t *testing.T) {
	srv, admin, rowID, deliveries := followerQueueServer(t)

	// List: the pending request shows with channel + follower identity.
	rec := getWithAuth(srv, "/api/v1/admin/federation/follower-requests", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list federationFollowerRequestListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Requests) != 1 {
		t.Fatalf("queue = %+v, want one pending request", list.Requests)
	}
	got := list.Requests[0]
	if got.ID != rowID.String() || got.ChannelHandle != "films" || got.Handle != "kaisa@mastodon.example" {
		t.Errorf("request = %+v, want kaisa@mastodon.example → films", got)
	}

	// Approve: 204, the Accept is queued, the queue empties.
	rec = postWithAuth(srv, "/api/v1/admin/federation/follower-requests/"+rowID.String()+"/approve", admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(*deliveries) != 1 {
		t.Fatalf("deliveries = %d, want the Accept", len(*deliveries))
	}
	var accept map[string]any
	_ = json.Unmarshal((*deliveries)[0].Payload, &accept)
	if accept["type"] != "Accept" {
		t.Errorf("queued %v, want an Accept", accept["type"])
	}
	if rec := getWithAuth(srv, "/api/v1/admin/federation/follower-requests", admin); rec.Code == http.StatusOK {
		var after federationFollowerRequestListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &after)
		if len(after.Requests) != 0 {
			t.Errorf("queue after approve = %+v, want empty", after.Requests)
		}
	}

	// Approving the resolved id again → 404.
	if rec := postWithAuth(srv, "/api/v1/admin/federation/follower-requests/"+rowID.String()+"/approve", admin); rec.Code != http.StatusNotFound {
		t.Errorf("second approve = %d, want 404", rec.Code)
	}
}

func TestFederationFollowerQueueReject(t *testing.T) {
	srv, admin, rowID, deliveries := followerQueueServer(t)

	rec := postWithAuth(srv, "/api/v1/admin/federation/follower-requests/"+rowID.String()+"/reject", admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reject = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(*deliveries) != 1 {
		t.Fatalf("deliveries = %d, want the Reject", len(*deliveries))
	}
	var reject map[string]any
	_ = json.Unmarshal((*deliveries)[0].Payload, &reject)
	if reject["type"] != "Reject" {
		t.Errorf("queued %v, want a Reject", reject["type"])
	}
	// Unknown / malformed ids → 404.
	if rec := postWithAuth(srv, "/api/v1/admin/federation/follower-requests/"+uuid.NewString()+"/reject", admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id reject = %d, want 404", rec.Code)
	}
	if rec := postWithAuth(srv, "/api/v1/admin/federation/follower-requests/not-a-uuid/reject", admin); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id reject = %d, want 404", rec.Code)
	}
}

func TestFederationFollowerQueueRequiresAdmin(t *testing.T) {
	srv, _, rowID, _ := followerQueueServer(t)
	// Second registered account is a regular user.
	user := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := getWithAuth(srv, "/api/v1/admin/federation/follower-requests", user); rec.Code != http.StatusForbidden {
		t.Errorf("list as user = %d, want 403", rec.Code)
	}
	if rec := postWithAuth(srv, "/api/v1/admin/federation/follower-requests/"+rowID.String()+"/approve", user); rec.Code != http.StatusForbidden {
		t.Errorf("approve as user = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/federation/follower-requests", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("list unauthenticated = %d, want 401", rec.Code)
	}
}

func (f followerQueueRepo) CountPendingRemoteFollows(ctx context.Context) (int64, error) {
	rows, err := f.ListPendingRemoteFollows(ctx, sqlcgen.ListPendingRemoteFollowsParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f followerQueueRepo) CountRemoteChannelFollows(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListRemoteChannelFollows(ctx, sqlcgen.ListRemoteChannelFollowsParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

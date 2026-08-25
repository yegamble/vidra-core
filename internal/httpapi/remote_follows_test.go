package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// followFakeRepo backs the remote-follow handler tests: the shared fakeFedRepo
// provides actors/keys/deliveries; the follow rows are map-backed here and the
// caller's actor identity resolves through the auth fake (like the real JOIN
// on users).
type followFakeRepo struct {
	fakeFedRepo
	auth      *authFakeRepo
	rcFollows map[uuid.UUID]*sqlcgen.RemoteChannelFollow
}

func (f *followFakeRepo) GetUserActorByID(_ context.Context, id uuid.UUID) (sqlcgen.GetUserActorByIDRow, error) {
	u, err := f.auth.GetUserByID(context.Background(), id)
	if err != nil || !u.IsActive {
		return sqlcgen.GetUserActorByIDRow{}, pgx.ErrNoRows
	}
	return sqlcgen.GetUserActorByIDRow{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName}, nil
}

func (f *followFakeRepo) UpsertRemoteChannelFollow(_ context.Context, arg sqlcgen.UpsertRemoteChannelFollowParams) (sqlcgen.RemoteChannelFollow, error) {
	for _, row := range f.rcFollows {
		if row.UserID == arg.UserID && row.RemoteActorUrl == arg.RemoteActorUrl {
			return *row, nil
		}
	}
	row := &sqlcgen.RemoteChannelFollow{
		ID: uuid.New(), UserID: arg.UserID, RemoteActorUrl: arg.RemoteActorUrl,
		State: "pending", FollowActivityUrl: arg.FollowActivityUrl, CreatedAt: time.Now(),
	}
	f.rcFollows[row.ID] = row
	return *row, nil
}

func (f *followFakeRepo) GetRemoteChannelFollowByID(_ context.Context, arg sqlcgen.GetRemoteChannelFollowByIDParams) (sqlcgen.GetRemoteChannelFollowByIDRow, error) {
	row, ok := f.rcFollows[arg.ID]
	if !ok || row.UserID != arg.UserID {
		return sqlcgen.GetRemoteChannelFollowByIDRow{}, pgx.ErrNoRows
	}
	ra := f.remoteActors[row.RemoteActorUrl]
	return sqlcgen.GetRemoteChannelFollowByIDRow{
		ID: row.ID, UserID: row.UserID, RemoteActorUrl: row.RemoteActorUrl,
		State: row.State, FollowActivityUrl: row.FollowActivityUrl, CreatedAt: row.CreatedAt,
		PreferredUsername: ra.PreferredUsername, Domain: ra.Domain, InboxUrl: ra.InboxUrl,
	}, nil
}

func (f *followFakeRepo) ListRemoteChannelFollows(_ context.Context, arg sqlcgen.ListRemoteChannelFollowsParams) ([]sqlcgen.ListRemoteChannelFollowsRow, error) {
	var out []sqlcgen.ListRemoteChannelFollowsRow
	for _, row := range f.rcFollows {
		if row.UserID != arg.UserID {
			continue
		}
		ra := f.remoteActors[row.RemoteActorUrl]
		out = append(out, sqlcgen.ListRemoteChannelFollowsRow{
			ID: row.ID, RemoteActorUrl: row.RemoteActorUrl, State: row.State,
			CreatedAt: row.CreatedAt, PreferredUsername: ra.PreferredUsername, Domain: ra.Domain,
		})
	}
	return out, nil
}

func (f *followFakeRepo) DeleteRemoteChannelFollowByID(_ context.Context, arg sqlcgen.DeleteRemoteChannelFollowByIDParams) (int64, error) {
	if row, ok := f.rcFollows[arg.ID]; ok && row.UserID == arg.UserID {
		delete(f.rcFollows, arg.ID)
		return 1, nil
	}
	return 0, nil
}

const testRemoteChannel = "https://tube.remote.example/video-channels/movies"

// remoteFollowServer builds a server with auth + federation wired over fakes,
// the remote channel actor pre-cached, and federation enabled.
func remoteFollowServer(cfg *config.Config) (*Server, *followFakeRepo) {
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)

	repo := &followFakeRepo{
		fakeFedRepo: fakeFedRepo{
			acctKeys:     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
			chanKeys:     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
			remoteActors: map[string]sqlcgen.RemoteActor{},
			processed:    map[string]bool{},
			deliveries:   map[string]sqlcgen.EnqueueDeliveryParams{},
		},
		auth:      authRepo,
		rcFollows: map[uuid.UUID]*sqlcgen.RemoteChannelFollow{},
	}
	repo.remoteActors[testRemoteChannel] = sqlcgen.RemoteActor{
		ActorUrl: testRemoteChannel, ActorType: "Group", PreferredUsername: "movies",
		Domain: "tube.remote.example", InboxUrl: "https://tube.remote.example/inbox",
		PublicKeyPem: "PEM",
	}
	fedsvc := federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithFederationService(fedsvc),
	)
	return srv, repo
}

func TestRemoteFollowFlow(t *testing.T) {
	srv, repo := remoteFollowServer(fedTestConfig())
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Follow by actor URL → 201 pending with the resolved identity.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/remote-follows", `{"actor_url":"`+testRemoteChannel+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("follow = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created remoteFollowView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.State != "pending" || created.Handle != "movies@tube.remote.example" || created.Domain != "tube.remote.example" {
		t.Fatalf("created = %+v", created)
	}
	if len(repo.fakeFedRepo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want the queued Follow", len(repo.fakeFedRepo.deliveries))
	}
	for _, d := range repo.fakeFedRepo.deliveries {
		if !d.SigningUserID.Valid || d.SigningUsername != "ada" {
			t.Errorf("Follow delivery not account-signed: %+v", d)
		}
	}

	// Re-follow is idempotent: same row, no second delivery.
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/me/remote-follows", `{"actor_url":"`+testRemoteChannel+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-follow = %d", rec.Code)
	}
	var again remoteFollowView
	_ = json.Unmarshal(rec.Body.Bytes(), &again)
	if again.ID != created.ID {
		t.Errorf("re-follow minted a new row (%s vs %s)", again.ID, created.ID)
	}
	if len(repo.fakeFedRepo.deliveries) != 1 {
		t.Errorf("deliveries after re-follow = %d, want still 1", len(repo.fakeFedRepo.deliveries))
	}

	// The list shows it.
	var list remoteFollowListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/remote-follows", tok).Body.Bytes(), &list)
	if len(list.Follows) != 1 || list.Follows[0].ID != created.ID || list.Follows[0].ActorURL != testRemoteChannel {
		t.Fatalf("list = %+v", list.Follows)
	}

	// Unfollow → 204, row gone, second (Undo) delivery queued; repeat → 404.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/remote-follows/"+created.ID, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("unfollow = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/remote-follows", tok).Body.Bytes(), &list)
	if len(list.Follows) != 0 {
		t.Errorf("list after unfollow = %+v, want empty", list.Follows)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/remote-follows/"+created.ID, "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("second unfollow = %d, want 404", rec.Code)
	}
}

func TestRemoteFollowValidationAndAuth(t *testing.T) {
	srv, _ := remoteFollowServer(fedTestConfig())

	// Unauthenticated → 401 on every route.
	if rec := postTo(srv, "/api/v1/me/remote-follows", `{"actor_url":"`+testRemoteChannel+`"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous follow = %d, want 401", rec.Code)
	}
	if rec := getPath(srv, "/api/v1/me/remote-follows"); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d, want 401", rec.Code)
	}

	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	cases := map[string]string{
		"empty":         `{}`,
		"both":          `{"handle":"a@b.example","actor_url":"https://b.example/a"}`,
		"malformed":     `{"handle":"garbage"}`,
		"local target":  `{"actor_url":"https://videos.example/video-channels/films"}`,
		"unresolvable":  `{"actor_url":"https://unknown.example/video-channels/nobody"}`, // not cached; fetch refused (https to a non-existent host through the guarded client)
		"missing @name": `{"handle":"@only.domain"}`,
	}
	for name, body := range cases {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/remote-follows", body, tok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422 (body=%s)", name, rec.Code, rec.Body.String())
		}
	}

	// Malformed unfollow id → 404.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/remote-follows/not-a-uuid", "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("bad id unfollow = %d, want 404", rec.Code)
	}
}

func TestRemoteFollowFederationDisabled(t *testing.T) {
	cfg := fedTestConfig()
	cfg.FederationEnabled = false
	srv, repo := remoteFollowServer(cfg)
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// The routes exist (REST contract) but the POST refuses: no outbound
	// fetches happen on a non-federating instance.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/remote-follows", `{"actor_url":"`+testRemoteChannel+`"}`, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("follow while disabled = %d, want 503", rec.Code)
	}
	if len(repo.rcFollows) != 0 || len(repo.fakeFedRepo.deliveries) != 0 {
		t.Error("disabled follow must not store or enqueue anything")
	}
	// Reads still serve (empty).
	if rec := getWithAuth(srv, "/api/v1/me/remote-follows", tok); rec.Code != http.StatusOK {
		t.Errorf("list while disabled = %d, want 200", rec.Code)
	}
}

func (f *followFakeRepo) CountRemoteChannelFollows(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListRemoteChannelFollows(ctx, sqlcgen.ListRemoteChannelFollowsParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *followFakeRepo) CountPendingRemoteFollows(ctx context.Context) (int64, error) {
	rows, err := f.ListPendingRemoteFollows(ctx, sqlcgen.ListPendingRemoteFollowsParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

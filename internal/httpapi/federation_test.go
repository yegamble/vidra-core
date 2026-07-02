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

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeFedRepo serves NodeInfo counts + a single known account ("ada") and channel
// ("films"), storing minted keys in memory.
type fakeFedRepo struct {
	users, videos, comments int64
	userID, channelID       uuid.UUID
	acctKeys                map[uuid.UUID]sqlcgen.GetAccountActorKeyRow
	chanKeys                map[uuid.UUID]sqlcgen.GetChannelActorKeyRow
	remoteActors            map[string]sqlcgen.RemoteActor
	processed               map[string]bool
	remoteFollows           map[string]sqlcgen.InsertRemoteFollowParams
	deliveries              map[string]sqlcgen.EnqueueDeliveryParams
}

func (f fakeFedRepo) CountUsers(context.Context) (int64, error)        { return f.users, nil }
func (f fakeFedRepo) CountPublicVideos(context.Context) (int64, error) { return f.videos, nil }
func (f fakeFedRepo) CountComments(context.Context) (int64, error)     { return f.comments, nil }

func (f fakeFedRepo) GetUserActorByUsername(_ context.Context, name string) (sqlcgen.GetUserActorByUsernameRow, error) {
	if strings.EqualFold(name, "ada") {
		return sqlcgen.GetUserActorByUsernameRow{ID: f.userID, Username: "ada", DisplayName: "Ada"}, nil
	}
	return sqlcgen.GetUserActorByUsernameRow{}, pgx.ErrNoRows
}

func (f fakeFedRepo) GetChannelByHandle(_ context.Context, handle string) (sqlcgen.Channel, error) {
	if strings.EqualFold(handle, "films") {
		return sqlcgen.Channel{ID: f.channelID, Handle: "films", DisplayName: "Films"}, nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}

func (f fakeFedRepo) GetAccountActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetAccountActorKeyRow, error) {
	if k, ok := f.acctKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetAccountActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeFedRepo) InsertAccountActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertAccountActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.acctKeys[arg.UserID]; ok {
		return 0, nil
	}
	f.acctKeys[arg.UserID] = sqlcgen.GetAccountActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeFedRepo) GetChannelActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetChannelActorKeyRow, error) {
	if k, ok := f.chanKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetChannelActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeFedRepo) InsertChannelActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertChannelActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.chanKeys[arg.ChannelID]; ok {
		return 0, nil
	}
	f.chanKeys[arg.ChannelID] = sqlcgen.GetChannelActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeFedRepo) GetRemoteActor(_ context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	if r, ok := f.remoteActors[actorURL]; ok {
		return r, nil
	}
	return sqlcgen.RemoteActor{}, pgx.ErrNoRows
}

func (f fakeFedRepo) UpsertRemoteActor(_ context.Context, arg sqlcgen.UpsertRemoteActorParams) error {
	f.remoteActors[arg.ActorUrl] = sqlcgen.RemoteActor{ActorUrl: arg.ActorUrl, PublicKeyPem: arg.PublicKeyPem}
	return nil
}

func (f fakeFedRepo) IsActivityProcessed(_ context.Context, id string) (bool, error) {
	return f.processed[id], nil
}
func (f fakeFedRepo) MarkActivityProcessed(_ context.Context, id string) error {
	f.processed[id] = true
	return nil
}
func (f fakeFedRepo) InsertRemoteFollow(_ context.Context, arg sqlcgen.InsertRemoteFollowParams) error {
	f.remoteFollows[arg.RemoteActorUrl] = arg
	return nil
}

func (fakeFedRepo) GetVideoByID(context.Context, uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	return sqlcgen.GetVideoByIDRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) GetChannelByID(context.Context, uuid.UUID) (sqlcgen.Channel, error) {
	return sqlcgen.Channel{}, pgx.ErrNoRows
}
func (fakeFedRepo) ListRemoteFollowerInboxes(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

func (f fakeFedRepo) EnqueueDelivery(_ context.Context, arg sqlcgen.EnqueueDeliveryParams) error {
	f.deliveries[arg.InboxUrl] = arg
	return nil
}
func (fakeFedRepo) ClaimDueDeliveries(context.Context, int32) ([]sqlcgen.ClaimDueDeliveriesRow, error) {
	return nil, nil
}
func (fakeFedRepo) MarkDeliveryDelivered(context.Context, uuid.UUID) error { return nil }
func (fakeFedRepo) RescheduleDelivery(context.Context, sqlcgen.RescheduleDeliveryParams) error {
	return nil
}
func (fakeFedRepo) FailDelivery(context.Context, sqlcgen.FailDeliveryParams) error { return nil }

func fedTestConfig() *config.Config {
	c := testConfig()
	c.FederationEnabled = true
	c.PublicBaseURL = "https://videos.example"
	c.RegistrationEnabled = true
	return c
}

func fedServerRepo(cfg *config.Config) (*Server, fakeFedRepo) {
	repo := fakeFedRepo{
		users: 7, videos: 3, comments: 11,
		userID:        uuid.New(),
		channelID:     uuid.New(),
		acctKeys:      map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		chanKeys:      map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		remoteActors:  map[string]sqlcgen.RemoteActor{},
		processed:     map[string]bool{},
		remoteFollows: map[string]sqlcgen.InsertRemoteFollowParams{},
		deliveries:    map[string]sqlcgen.EnqueueDeliveryParams{},
	}
	svc := federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))
	return New(cfg, nil, nil, WithFederationService(svc)), repo
}

func fedServer(cfg *config.Config) *Server {
	srv, _ := fedServerRepo(cfg)
	return srv
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return getAccept(t, srv, path, "")
}

func getAccept(t *testing.T, srv *Server, path, accept string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestNodeInfoDiscovery(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/.well-known/nodeinfo")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body nodeInfoDiscovery
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Links) != 1 {
		t.Fatalf("links = %d, want 1", len(body.Links))
	}
	if body.Links[0].Rel != nodeInfo21Rel {
		t.Errorf("rel = %q, want %q", body.Links[0].Rel, nodeInfo21Rel)
	}
	if want := "https://videos.example/nodeinfo/2.1"; body.Links[0].Href != want {
		t.Errorf("href = %q, want %q", body.Links[0].Href, want)
	}
}

func TestNodeInfo21Document(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/nodeinfo/2.1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "schema/2.1") {
		t.Errorf("content-type = %q, want the nodeinfo 2.1 profile", ct)
	}
	var doc nodeInfo21Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Version != "2.1" {
		t.Errorf("version = %q, want 2.1", doc.Version)
	}
	if doc.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", doc.Software.Name)
	}
	if len(doc.Protocols) != 1 || doc.Protocols[0] != "activitypub" {
		t.Errorf("protocols = %v, want [activitypub]", doc.Protocols)
	}
	if !doc.OpenRegistrations {
		t.Errorf("openRegistrations = false, want true (RegistrationEnabled)")
	}
	if doc.Usage.Users.Total != 7 || doc.Usage.LocalPosts != 3 || doc.Usage.LocalComments != 11 {
		t.Errorf("usage = %+v, want {7,3,11}", doc.Usage)
	}
}

const apAccept = "application/activity+json"

func TestAccountActorServesPerson(t *testing.T) {
	rec := getAccept(t, fedServer(fedTestConfig()), "/accounts/ada", apAccept)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "activity+json") {
		t.Errorf("content-type = %q, want activity+json", ct)
	}
	var actor federation.Actor
	if err := json.Unmarshal(rec.Body.Bytes(), &actor); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if actor.Type != "Person" || actor.ID != "https://videos.example/accounts/ada" {
		t.Errorf("type/id = %q/%q", actor.Type, actor.ID)
	}
	if !strings.Contains(actor.PublicKey.PublicKeyPem, "BEGIN PUBLIC KEY") {
		t.Errorf("missing public key PEM: %q", actor.PublicKey.PublicKeyPem)
	}
}

func TestChannelActorServesGroup(t *testing.T) {
	rec := getAccept(t, fedServer(fedTestConfig()), "/video-channels/films", apAccept)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var actor federation.Actor
	if err := json.Unmarshal(rec.Body.Bytes(), &actor); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if actor.Type != "Group" || actor.ID != "https://videos.example/video-channels/films" {
		t.Errorf("type/id = %q/%q", actor.Type, actor.ID)
	}
}

func TestActorRequiresActivityJSONAccept(t *testing.T) {
	// A browser Accept (no AP media type) is 406 — the HTML profile is the frontend's.
	rec := getAccept(t, fedServer(fedTestConfig()), "/accounts/ada", "text/html")
	if rec.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 for a non-AP Accept", rec.Code)
	}
}

func TestActorUnknownReturns404(t *testing.T) {
	rec := getAccept(t, fedServer(fedTestConfig()), "/accounts/ghost", apAccept)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown actor", rec.Code)
	}
}

func TestWebFingerResolvesActor(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/.well-known/webfinger?resource=acct:ada@videos.example")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var jrd federation.JRD
	if err := json.Unmarshal(rec.Body.Bytes(), &jrd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if jrd.Subject != "acct:ada@videos.example" || len(jrd.Links) != 1 {
		t.Fatalf("jrd = %+v", jrd)
	}
	if jrd.Links[0].Href != "https://videos.example/accounts/ada" {
		t.Errorf("href = %q", jrd.Links[0].Href)
	}
}

func TestWebFingerErrors(t *testing.T) {
	srv := fedServer(fedTestConfig())
	if rec := get(t, srv, "/.well-known/webfinger"); rec.Code != http.StatusBadRequest {
		t.Errorf("missing resource: status = %d, want 400", rec.Code)
	}
	if rec := get(t, srv, "/.well-known/webfinger?resource=acct:ada@elsewhere.example"); rec.Code != http.StatusNotFound {
		t.Errorf("foreign domain: status = %d, want 404", rec.Code)
	}
}

func signerKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

const bobActor = "https://remote.example/accounts/bob"

func TestInboxAcceptsSignedFollow(t *testing.T) {
	key, pubPEM := signerKeyPEM(t)
	srv, repo := fedServerRepo(fedTestConfig())
	// Pre-seed the signer in the remote-actor cache (key for ResolveKey + inbox for
	// the Accept delivery) so no network fetch is needed.
	bobInbox := "https://remote.example/accounts/bob/inbox"
	repo.remoteActors[bobActor] = sqlcgen.RemoteActor{ActorUrl: bobActor, PublicKeyPem: pubPEM, InboxUrl: bobInbox}

	body := []byte(`{"id":"https://remote.example/act/1","type":"Follow","actor":"` + bobActor +
		`","object":"https://videos.example/video-channels/films"}`)
	req := httptest.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
	if err := (httpsig.Signer{KeyID: bobActor + "#main-key", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := repo.remoteFollows[bobActor]; !ok {
		t.Errorf("remote follow not recorded: %+v", repo.remoteFollows)
	}
	// An Accept was durably enqueued to the follower's inbox.
	if _, ok := repo.deliveries[bobInbox]; !ok {
		t.Errorf("Accept not enqueued for delivery: %+v", repo.deliveries)
	}
}

func TestInboxRejectsBadSignature(t *testing.T) {
	key, pubPEM := signerKeyPEM(t)
	srv, repo := fedServerRepo(fedTestConfig())
	repo.remoteActors[bobActor] = sqlcgen.RemoteActor{ActorUrl: bobActor, PublicKeyPem: pubPEM}

	body := []byte(`{"id":"https://remote.example/act/2","type":"Follow","actor":"` + bobActor +
		`","object":"https://videos.example/video-channels/films"}`)
	req := httptest.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
	if err := (httpsig.Signer{KeyID: bobActor + "#main-key", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Corrupt the signature after signing.
	sig := req.Header.Get("Signature")
	req.Header.Set("Signature", sig[:len(sig)-2]+`A"`)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(repo.remoteFollows) != 0 {
		t.Error("a follow was recorded despite a bad signature")
	}
}

// The routes are a prod-safe opt-in: absent (404) when FEDERATION_ENABLED is off,
// even though the service is wired — mirroring the dev-endpoint exclusion.
func TestFederationRoutesAbsentWhenDisabled(t *testing.T) {
	cfg := fedTestConfig()
	cfg.FederationEnabled = false
	srv := fedServer(cfg)
	cases := []struct{ path, accept string }{
		{"/.well-known/nodeinfo", ""},
		{"/nodeinfo/2.1", ""},
		{"/.well-known/webfinger?resource=acct:ada@videos.example", ""},
		{"/accounts/ada", apAccept},
		{"/video-channels/films", apAccept},
	}
	for _, tc := range cases {
		if rec := getAccept(t, srv, tc.path, tc.accept); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when federation disabled", tc.path, rec.Code)
		}
	}
}

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
	"sort"
	"strings"
	"testing"
	"time"

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
	localFollowerN          int64
	remoteFollowerN         int64
	// A29 parity (0142): handles a channel was renamed away from, the subset
	// that carries the frozen ActivityPub identity, and the admin's
	// instance-wide per-actor blocks.
	channelAliases   map[string]sqlcgen.GetChannelHandleAliasRow
	actorAliases     map[uuid.UUID]string
	adminActorBlocks map[string]string
	channelVideoN    int64
	outboxVideos     []sqlcgen.ListChannelOutboxVideosRow
	// A29 remediation: the rows the dereferenceable object ids read.
	videosByID map[uuid.UUID]sqlcgen.GetVideoByIDRow
	commentsBy map[uuid.UUID]sqlcgen.Comment
	tombstones map[uuid.UUID]time.Time
	// blockedDomains is the admin instance blocklist the inbox consults.
	blockedDomains map[string]bool
	// remoteBlocks are per-viewer blocks of a remote ACTOR (migration 0138),
	// keyed blockerID|actorURL.
	remoteBlocks map[string]bool
	// remoteVideoComments are MIRRORED threads on remote videos (0140), keyed
	// by the origin's object url.
	remoteVideoComments map[string]sqlcgen.UpsertRemoteVideoCommentParams
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
		return sqlcgen.Channel{ID: f.channelID, OwnerID: f.userID, Handle: "films", DisplayName: "Films", ActivitypubEnabled: true}, nil
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
func (f fakeFedRepo) DeleteRemoteFollow(_ context.Context, arg sqlcgen.DeleteRemoteFollowParams) error {
	delete(f.remoteFollows, arg.RemoteActorUrl)
	return nil
}

func (f fakeFedRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if v, ok := f.videosByID[id]; ok {
		return v, nil
	}
	return sqlcgen.GetVideoByIDRow{}, pgx.ErrNoRows
}
func (f fakeFedRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	if id == f.channelID {
		return sqlcgen.Channel{ID: f.channelID, OwnerID: f.userID, Handle: "films", DisplayName: "Films", ActivitypubEnabled: true}, nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}
func (fakeFedRepo) ListRemoteFollowerInboxes(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

// CountChannelFollowers is the TOTAL since A29-F6 — local plus accepted remote,
// one definition shared by the AP collection and every REST surface.
func (f fakeFedRepo) CountChannelFollowers(context.Context, uuid.UUID) (int64, error) {
	return f.localFollowerN + f.remoteFollowerN, nil
}
func (f fakeFedRepo) CountRemoteFollowers(context.Context, uuid.UUID) (int64, error) {
	return f.remoteFollowerN, nil
}
func (f fakeFedRepo) CountPublicVideosByChannel(context.Context, uuid.UUID) (int64, error) {
	return f.channelVideoN, nil
}
func (f fakeFedRepo) ListChannelOutboxVideos(_ context.Context, arg sqlcgen.ListChannelOutboxVideosParams) ([]sqlcgen.ListChannelOutboxVideosRow, error) {
	all := f.outboxVideos
	lo := int(arg.Offset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(arg.Limit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func (f fakeFedRepo) EnqueueDelivery(_ context.Context, arg sqlcgen.EnqueueDeliveryParams) error {
	f.deliveries[arg.InboxUrl] = arg
	return nil
}
func (fakeFedRepo) ClaimDueDeliveries(context.Context, sqlcgen.ClaimDueDeliveriesParams) ([]sqlcgen.ClaimDueDeliveriesRow, error) {
	return nil, nil
}
func (fakeFedRepo) MarkDeliveryDelivered(context.Context, uuid.UUID) error { return nil }
func (fakeFedRepo) RescheduleDelivery(context.Context, sqlcgen.RescheduleDeliveryParams) error {
	return nil
}
func (fakeFedRepo) FailDelivery(context.Context, sqlcgen.FailDeliveryParams) error { return nil }
func (fakeFedRepo) ListCancelledDeliveriesForRedelivery(context.Context, sqlcgen.ListCancelledDeliveriesForRedeliveryParams) ([]sqlcgen.ListCancelledDeliveriesForRedeliveryRow, error) {
	return nil, nil
}
func (fakeFedRepo) RequeueCancelledDelivery(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (fakeFedRepo) UpsertRemoteVideo(context.Context, sqlcgen.UpsertRemoteVideoParams) (sqlcgen.UpsertRemoteVideoRow, error) {
	return sqlcgen.UpsertRemoteVideoRow{ID: uuid.New()}, nil
}
func (fakeFedRepo) SetRemoteVideoThumbnail(context.Context, sqlcgen.SetRemoteVideoThumbnailParams) error {
	return nil
}
func (f fakeFedRepo) IsInstanceBlocked(_ context.Context, domain string) (bool, error) {
	return f.blockedDomains[domain], nil
}

// A29 remediation (migration 0138): dereferenceable ids and per-remote-account
// blocks. Empty by default — the tests that exercise them seed their own repo.
func (f fakeFedRepo) InsertFederatedVideoTombstone(_ context.Context, id uuid.UUID) error {
	if f.tombstones != nil {
		f.tombstones[id] = fedFakeDeletedAt
	}
	return nil
}

func (f fakeFedRepo) GetFederatedVideoTombstone(_ context.Context, id uuid.UUID) (sqlcgen.FederatedVideoTombstone, error) {
	if at, ok := f.tombstones[id]; ok {
		return sqlcgen.FederatedVideoTombstone{VideoID: id, DeletedAt: at}, nil
	}
	return sqlcgen.FederatedVideoTombstone{}, pgx.ErrNoRows
}

// fedFakeDeletedAt is the fixed deletion time the fake tombstones carry.
var fedFakeDeletedAt = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func (f fakeFedRepo) IsRemoteActorBlockedByAnyone(_ context.Context, actorURL string) (bool, error) {
	for key := range f.remoteBlocks {
		if _, url, ok := strings.Cut(key, "|"); ok && url == actorURL {
			return true, nil
		}
	}
	return false, nil
}

func (f fakeFedRepo) IsRemoteActorBlockedBy(_ context.Context, arg sqlcgen.IsRemoteActorBlockedByParams) (bool, error) {
	return f.remoteBlocks[arg.BlockerID.String()+"|"+arg.ActorUrl], nil
}

func (f fakeFedRepo) BlockRemoteActor(_ context.Context, arg sqlcgen.BlockRemoteActorParams) error {
	if f.remoteBlocks != nil {
		f.remoteBlocks[arg.BlockerID.String()+"|"+arg.RemoteActorUrl] = true
	}
	return nil
}

func (f fakeFedRepo) UnblockRemoteActor(_ context.Context, arg sqlcgen.UnblockRemoteActorParams) (int64, error) {
	key := arg.BlockerID.String() + "|" + arg.RemoteActorUrl
	if f.remoteBlocks[key] {
		delete(f.remoteBlocks, key)
		return 1, nil
	}
	return 0, nil
}

func (f fakeFedRepo) ListRemoteActorBlocks(_ context.Context, arg sqlcgen.ListRemoteActorBlocksParams) ([]sqlcgen.ListRemoteActorBlocksRow, error) {
	var out []sqlcgen.ListRemoteActorBlocksRow
	for key := range f.remoteBlocks {
		blocker, actorURL, _ := strings.Cut(key, "|")
		if blocker != arg.BlockerID.String() {
			continue
		}
		out = append(out, sqlcgen.ListRemoteActorBlocksRow{RemoteActorUrl: actorURL, CreatedAt: fedFakeDeletedAt})
	}
	return out, nil
}

func (f fakeFedRepo) CountRemoteActorBlocks(_ context.Context, blockerID uuid.UUID) (int64, error) {
	var n int64
	for key := range f.remoteBlocks {
		if blocker, _, _ := strings.Cut(key, "|"); blocker == blockerID.String() {
			n++
		}
	}
	return n, nil
}

func (fakeFedRepo) GetRemoteVideoByURL(context.Context, string) (sqlcgen.GetRemoteVideoByURLRow, error) {
	return sqlcgen.GetRemoteVideoByURLRow{}, pgx.ErrNoRows
}

// GetUserActorByID resolves ada — the owner of the "films" channel — so the
// served Group actor can attribute itself to the owner account (PeerTube interop).
func (f fakeFedRepo) GetUserActorByID(_ context.Context, id uuid.UUID) (sqlcgen.GetUserActorByIDRow, error) {
	if id == f.userID {
		return sqlcgen.GetUserActorByIDRow{ID: f.userID, Username: "ada", DisplayName: "Ada"}, nil
	}
	return sqlcgen.GetUserActorByIDRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) UpsertRemoteChannelFollow(context.Context, sqlcgen.UpsertRemoteChannelFollowParams) (sqlcgen.RemoteChannelFollow, error) {
	return sqlcgen.RemoteChannelFollow{}, pgx.ErrNoRows
}
func (fakeFedRepo) GetRemoteChannelFollowByID(context.Context, sqlcgen.GetRemoteChannelFollowByIDParams) (sqlcgen.GetRemoteChannelFollowByIDRow, error) {
	return sqlcgen.GetRemoteChannelFollowByIDRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) ListRemoteChannelFollows(context.Context, sqlcgen.ListRemoteChannelFollowsParams) ([]sqlcgen.ListRemoteChannelFollowsRow, error) {
	return nil, nil
}
func (fakeFedRepo) DeleteRemoteChannelFollowByID(context.Context, sqlcgen.DeleteRemoteChannelFollowByIDParams) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) AcceptRemoteChannelFollowByActivity(context.Context, sqlcgen.AcceptRemoteChannelFollowByActivityParams) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) RejectRemoteChannelFollowByActivity(context.Context, sqlcgen.RejectRemoteChannelFollowByActivityParams) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) HasAcceptedRemoteChannelFollow(context.Context, string) (bool, error) {
	return false, nil
}
func (fakeFedRepo) HasRemoteChannelFollow(context.Context, string) (bool, error) {
	return false, nil
}

// W12 follower-approval queue + follow-back stubs: the handler suite for the
// admin queue lives in admin_federation_test.go, whose fake overrides these.
func (fakeFedRepo) InsertRemoteFollowPending(context.Context, sqlcgen.InsertRemoteFollowPendingParams) error {
	return nil
}
func (fakeFedRepo) ListPendingRemoteFollows(context.Context, sqlcgen.ListPendingRemoteFollowsParams) ([]sqlcgen.ListPendingRemoteFollowsRow, error) {
	return nil, nil
}
func (fakeFedRepo) AcceptPendingRemoteFollowByID(context.Context, uuid.UUID) (sqlcgen.AcceptPendingRemoteFollowByIDRow, error) {
	return sqlcgen.AcceptPendingRemoteFollowByIDRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) DeletePendingRemoteFollowByID(context.Context, uuid.UUID) (sqlcgen.DeletePendingRemoteFollowByIDRow, error) {
	return sqlcgen.DeletePendingRemoteFollowByIDRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) InsertChannelFollowBackIfAbsent(context.Context, sqlcgen.InsertChannelFollowBackIfAbsentParams) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) AcceptChannelFollowBackByActivity(context.Context, sqlcgen.AcceptChannelFollowBackByActivityParams) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) DeleteChannelFollowBackByActivity(context.Context, sqlcgen.DeleteChannelFollowBackByActivityParams) (int64, error) {
	return 0, nil
}

// Federated-comment + inbound-delete stubs (remote-content §6-7): the handler
// suite doesn't exercise Note ingestion (see internal/federation's own tests).
func (f fakeFedRepo) GetComment(_ context.Context, id uuid.UUID) (sqlcgen.Comment, error) {
	if c, ok := f.commentsBy[id]; ok {
		return c, nil
	}
	return sqlcgen.Comment{}, pgx.ErrNoRows
}
func (fakeFedRepo) GetCommentByRemoteObjectURL(context.Context, string) (sqlcgen.Comment, error) {
	return sqlcgen.Comment{}, pgx.ErrNoRows
}
func (fakeFedRepo) CreateRemoteComment(context.Context, sqlcgen.CreateRemoteCommentParams) (sqlcgen.Comment, error) {
	return sqlcgen.Comment{}, pgx.ErrNoRows
}
func (fakeFedRepo) UpdateComment(context.Context, sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error) {
	return sqlcgen.Comment{}, pgx.ErrNoRows
}
func (fakeFedRepo) DeleteComment(context.Context, uuid.UUID) error { return nil }
func (fakeFedRepo) GetRemoteVideoByObjectURL(context.Context, string) (sqlcgen.GetRemoteVideoByObjectURLRow, error) {
	return sqlcgen.GetRemoteVideoByObjectURLRow{}, pgx.ErrNoRows
}
func (fakeFedRepo) DeleteRemoteVideoByObjectURL(context.Context, string) (int64, error) {
	return 0, nil
}
func (fakeFedRepo) DeleteRemoteActor(context.Context, string) (int64, error) { return 0, nil }

func fedTestConfig() *config.Config {
	c := testConfig()
	c.FederationEnabled = true
	c.PublicBaseURL = "https://videos.example"
	c.RegistrationEnabled = true
	return c
}

func fedServerRepo(cfg *config.Config) (*Server, fakeFedRepo) {
	repo := newFedRepoFor(cfg)
	svc := federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))
	return New(cfg, nil, nil, WithFederationService(svc)), repo
}

// newFedRepoFor builds the seeded fake without the server, so a test that needs
// its OWN server options (a captured logger, say) can still start from the same
// world every other federation test uses.
func newFedRepoFor(_ *config.Config) fakeFedRepo {
	repo := fakeFedRepo{
		users: 7, videos: 3, comments: 11,
		localFollowerN:  3,
		remoteFollowerN: 2,
		channelVideoN:   7,
		outboxVideos: []sqlcgen.ListChannelOutboxVideosRow{
			{ID: uuid.New(), Title: "One", Description: "a"},
			{ID: uuid.New(), Title: "Two", Description: "b"},
		},
		userID:              uuid.New(),
		channelID:           uuid.New(),
		acctKeys:            map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		chanKeys:            map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		remoteActors:        map[string]sqlcgen.RemoteActor{},
		processed:           map[string]bool{},
		remoteFollows:       map[string]sqlcgen.InsertRemoteFollowParams{},
		deliveries:          map[string]sqlcgen.EnqueueDeliveryParams{},
		videosByID:          map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		commentsBy:          map[uuid.UUID]sqlcgen.Comment{},
		tombstones:          map[uuid.UUID]time.Time{},
		blockedDomains:      map[string]bool{},
		remoteBlocks:        map[string]bool{},
		remoteVideoComments: map[string]sqlcgen.UpsertRemoteVideoCommentParams{},
	}
	return repo
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

func TestChannelCollectionEndpoints(t *testing.T) {
	srv := fedServer(fedTestConfig())

	followers := getAccept(t, srv, "/video-channels/films/followers", apAccept)
	if followers.Code != http.StatusOK {
		t.Fatalf("followers status = %d, want 200", followers.Code)
	}
	if ct := followers.Header().Get("Content-Type"); !strings.Contains(ct, "activity+json") {
		t.Errorf("content-type = %q", ct)
	}
	var col federation.OrderedCollection
	if err := json.Unmarshal(followers.Body.Bytes(), &col); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if col.Type != "OrderedCollection" || col.TotalItems != 5 { // 3 local + 2 remote
		t.Errorf("followers = %+v, want OrderedCollection totalItems 5", col)
	}

	outbox := getAccept(t, srv, "/video-channels/films/outbox", apAccept)
	_ = json.Unmarshal(outbox.Body.Bytes(), &col)
	if outbox.Code != http.StatusOK || col.TotalItems != 7 {
		t.Errorf("outbox status=%d totalItems=%d, want 200/7", outbox.Code, col.TotalItems)
	}

	account := getAccept(t, srv, "/accounts/ada/followers", apAccept)
	_ = json.Unmarshal(account.Body.Bytes(), &col)
	if account.Code != http.StatusOK || col.TotalItems != 0 {
		t.Errorf("account followers status=%d totalItems=%d, want 200/0", account.Code, col.TotalItems)
	}
}

func TestChannelOutboxPaging(t *testing.T) {
	srv := fedServer(fedTestConfig())

	// Base outbox: a summary OrderedCollection with a `first` page link.
	base := getAccept(t, srv, "/video-channels/films/outbox", apAccept)
	if base.Code != http.StatusOK {
		t.Fatalf("outbox status = %d, want 200", base.Code)
	}
	var col federation.OrderedCollection
	if err := json.Unmarshal(base.Body.Bytes(), &col); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if col.Type != "OrderedCollection" || col.First == "" {
		t.Errorf("outbox = %+v, want OrderedCollection with a first link", col)
	}

	// Page 1: an OrderedCollectionPage carrying the seeded videos as Create items.
	pageRec := getAccept(t, srv, "/video-channels/films/outbox?page=1", apAccept)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("outbox page status = %d, want 200", pageRec.Code)
	}
	var page federation.OrderedCollectionPage
	if err := json.Unmarshal(pageRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	if page.Type != "OrderedCollectionPage" || len(page.OrderedItems) != 2 {
		t.Errorf("page = type %q with %d items, want OrderedCollectionPage/2", page.Type, len(page.OrderedItems))
	}
}

func TestCollectionRequiresActivityJSONAndExists(t *testing.T) {
	srv := fedServer(fedTestConfig())
	if rec := getAccept(t, srv, "/video-channels/films/outbox", "text/html"); rec.Code != http.StatusNotAcceptable {
		t.Errorf("non-AP Accept status = %d, want 406", rec.Code)
	}
	if rec := getAccept(t, srv, "/video-channels/ghost/followers", apAccept); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel status = %d, want 404", rec.Code)
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
		{"/video-channels/films/followers", apAccept},
		{"/video-channels/films/outbox", apAccept},
	}
	for _, tc := range cases {
		if rec := getAccept(t, srv, tc.path, tc.accept); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when federation disabled", tc.path, rec.Code)
		}
	}
}

func (fakeFedRepo) CountPendingRemoteFollows(context.Context) (int64, error) { return 0, nil }

func (fakeFedRepo) CountRemoteChannelFollows(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

// --- mirrored remote-video comments (A29-F8, migration 0140) ----------------

func (f fakeFedRepo) UpsertRemoteVideoComment(_ context.Context, arg sqlcgen.UpsertRemoteVideoCommentParams) (sqlcgen.UpsertRemoteVideoCommentRow, error) {
	if f.remoteVideoComments != nil {
		f.remoteVideoComments[arg.ObjectUrl] = arg
	}
	return sqlcgen.UpsertRemoteVideoCommentRow{ObjectUrl: arg.ObjectUrl, Body: arg.Body}, nil
}

func (f fakeFedRepo) GetRemoteVideoCommentByObjectURL(_ context.Context, objectURL string) (sqlcgen.GetRemoteVideoCommentByObjectURLRow, error) {
	if c, ok := f.remoteVideoComments[objectURL]; ok {
		return sqlcgen.GetRemoteVideoCommentByObjectURLRow{
			RemoteVideoID: c.RemoteVideoID, RemoteActorUrl: c.RemoteActorUrl, ObjectUrl: objectURL,
		}, nil
	}
	return sqlcgen.GetRemoteVideoCommentByObjectURLRow{}, pgx.ErrNoRows
}

func (f fakeFedRepo) DeleteRemoteVideoCommentByObjectURL(_ context.Context, objectURL string) (int64, error) {
	if _, ok := f.remoteVideoComments[objectURL]; ok {
		delete(f.remoteVideoComments, objectURL)
		return 1, nil
	}
	return 0, nil
}

func (f fakeFedRepo) ListRemoteVideoComments(_ context.Context, arg sqlcgen.ListRemoteVideoCommentsParams) ([]sqlcgen.ListRemoteVideoCommentsRow, error) {
	var out []sqlcgen.ListRemoteVideoCommentsRow
	for objectURL, c := range f.remoteVideoComments {
		if c.RemoteVideoID != arg.RemoteVideoID {
			continue
		}
		out = append(out, sqlcgen.ListRemoteVideoCommentsRow{
			RemoteActorUrl:   c.RemoteActorUrl,
			RemoteAuthorName: c.RemoteAuthorName,
			ObjectUrl:        objectURL,
			Body:             c.Body,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectUrl < out[j].ObjectUrl })
	return out, nil
}

func (f fakeFedRepo) CountRemoteVideoComments(_ context.Context, arg sqlcgen.CountRemoteVideoCommentsParams) (int64, error) {
	var n int64
	for _, c := range f.remoteVideoComments {
		if c.RemoteVideoID == arg.RemoteVideoID {
			n++
		}
	}
	return n, nil
}

// --- A29 parity (migration 0142): handle aliases + the admin per-actor block ---
//
// The base fake has no renamed channels and no admin blocks, which is the
// shipped default; the tests that need either build them explicitly.

func (f fakeFedRepo) GetChannelHandleAlias(_ context.Context, handle string) (sqlcgen.GetChannelHandleAliasRow, error) {
	if row, ok := f.channelAliases[strings.ToLower(handle)]; ok {
		return row, nil
	}
	return sqlcgen.GetChannelHandleAliasRow{}, pgx.ErrNoRows
}

func (f fakeFedRepo) GetChannelActorAlias(_ context.Context, channelID uuid.UUID) (string, error) {
	if h, ok := f.actorAliases[channelID]; ok {
		return h, nil
	}
	return "", pgx.ErrNoRows
}

func (f fakeFedRepo) IsRemoteActorBlockedInstanceWide(_ context.Context, actorURL string) (bool, error) {
	if _, ok := f.adminActorBlocks[actorURL]; ok {
		return true, nil
	}
	if ra, ok := f.remoteActors[actorURL]; ok && ra.AttributedTo != "" {
		_, owned := f.adminActorBlocks[ra.AttributedTo]
		return owned, nil
	}
	return false, nil
}

func (f fakeFedRepo) BlockRemoteActorInstanceWide(_ context.Context, arg sqlcgen.BlockRemoteActorInstanceWideParams) error {
	if f.adminActorBlocks == nil {
		return nil
	}
	// Mirrors the SQL CONFLICT arm: a re-block with an empty reason keeps the
	// first note, which is the only prose on the row.
	if existing, ok := f.adminActorBlocks[arg.RemoteActorUrl]; ok && arg.Reason == "" {
		f.adminActorBlocks[arg.RemoteActorUrl] = existing
		return nil
	}
	f.adminActorBlocks[arg.RemoteActorUrl] = arg.Reason
	return nil
}

func (f fakeFedRepo) UnblockRemoteActorInstanceWide(_ context.Context, actorURL string) (int64, error) {
	if _, ok := f.adminActorBlocks[actorURL]; !ok {
		return 0, nil
	}
	delete(f.adminActorBlocks, actorURL)
	return 1, nil
}

func (f fakeFedRepo) ListBlockedRemoteActors(_ context.Context, _ sqlcgen.ListBlockedRemoteActorsParams) ([]sqlcgen.ListBlockedRemoteActorsRow, error) {
	urls := make([]string, 0, len(f.adminActorBlocks))
	for u := range f.adminActorBlocks {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	out := make([]sqlcgen.ListBlockedRemoteActorsRow, 0, len(urls))
	for _, u := range urls {
		row := sqlcgen.ListBlockedRemoteActorsRow{RemoteActorUrl: u, Reason: f.adminActorBlocks[u]}
		if ra, ok := f.remoteActors[u]; ok {
			row.PreferredUsername, row.Domain = ra.PreferredUsername, ra.Domain
		}
		out = append(out, row)
	}
	return out, nil
}

func (f fakeFedRepo) CountBlockedRemoteActors(_ context.Context) (int64, error) {
	return int64(len(f.adminActorBlocks)), nil
}

// GetRemoteVideoByID is the mirrored thread's origin-authority lookup: a REPLY
// to a mirrored comment must still come from the server that hosts the VIDEO, so
// the check needs the video's object url and not the parent comment's.
func (f fakeFedRepo) GetRemoteVideoByID(_ context.Context, _ uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error) {
	return sqlcgen.GetRemoteVideoByIDRow{}, pgx.ErrNoRows
}

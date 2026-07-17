package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/observability"
)

// ipfsServer builds an auth-enabled server with the given config so the IPFS
// admin stubs can be exercised with real admin/non-admin/anon principals. Extra
// options (e.g. a fake mirror) are appended.
func ipfsServer(t *testing.T, cfg *config.Config, extra ...Option) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	opts := append([]Option{WithAuthService(svc, 15*time.Minute)}, extra...)
	return New(cfg, nil, nil, opts...)
}

// fakeIPFSMirror is a test double for ipfsMirrorProvider.
type fakeIPFSMirror struct {
	status     ipfsmirror.Status
	reevalHits int
	reevalErr  error
	// videoPins is returned (with ok=true) by VideoPins; pinnedIDs is the set
	// PinnedVideoIDs reports true for. Both default to empty/nothing pinned.
	videoPins map[uuid.UUID]ipfsmirror.VideoIPFS
	pinnedIDs map[uuid.UUID]bool
	assetURLs map[string]string
	assetErr  error
	// reconcile/backfill test knobs + call counters.
	rearmed         int64
	backfill        ipfsmirror.BackfillCounts
	reconcileHits   int
	backfillHits    int
	backfillNetwork string // the ?network= filter the handler passed to Backfill
}

func (f *fakeIPFSMirror) Status(ctx context.Context) (ipfsmirror.Status, error) {
	return f.status, nil
}
func (f *fakeIPFSMirror) EnqueueUserReeval(ctx context.Context, userID uuid.UUID) error {
	f.reevalHits++
	return f.reevalErr
}
func (f *fakeIPFSMirror) Reconcile(ctx context.Context) (int64, error) {
	f.reconcileHits++
	return f.rearmed, nil
}
func (f *fakeIPFSMirror) Backfill(ctx context.Context, networkFilter string) (ipfsmirror.BackfillCounts, error) {
	f.backfillHits++
	f.backfillNetwork = networkFilter
	return f.backfill, nil
}
func (f *fakeIPFSMirror) VideoPins(ctx context.Context, videoID uuid.UUID) (ipfsmirror.VideoIPFS, bool, error) {
	if p, ok := f.videoPins[videoID]; ok {
		return p, true, nil
	}
	return ipfsmirror.VideoIPFS{}, false, nil
}
func fakeAssetLookupKey(objectKey string, class ipfsmirror.MediaClass) string {
	return string(class) + "\x00" + objectKey
}
func (f *fakeIPFSMirror) PublicAssetURL(ctx context.Context, objectKey string, class ipfsmirror.MediaClass) (string, bool, error) {
	if f.assetErr != nil {
		return "", false, f.assetErr
	}
	u, ok := f.assetURLs[fakeAssetLookupKey(objectKey, class)]
	return u, ok, nil
}
func (f *fakeIPFSMirror) PinnedVideoIDs(ctx context.Context, videoIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range videoIDs {
		if f.pinnedIDs[id] {
			out[id] = true
		}
	}
	return out, nil
}

// TestIPFSStatusDisabled: with IPFS off (the default), the admin status endpoint
// answers 503 ipfs_disabled; access control (401/403) still applies.
func TestIPFSStatusDisabled(t *testing.T) {
	srv := ipfsServer(t, testConfig()) // IPFSEnabled defaults false
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/ipfs/status", admin)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status (disabled) = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "ipfs_disabled" {
		t.Errorf("error code = %q, want ipfs_disabled", code)
	}

	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/ipfs/status", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/ipfs/status", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon status = %d, want 401", rec.Code)
	}
}

func TestIPFSStatusPrivateOnly(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSMirrorPrivate = true
	cfg.IPFSPrivateAPIURL = "http://ipfs-private:5001"
	mirror := &fakeIPFSMirror{status: ipfsmirror.Status{
		Enabled: true,
		Networks: map[string]ipfsmirror.NetworkStatus{
			ipfsmirror.NetworkPublic:  {Enabled: false},
			ipfsmirror.NetworkPrivate: {Enabled: true, NodeReachable: true},
		},
	}}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/ipfs/status", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("private-only status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got ipfsStatusView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode private-only status: %v", err)
	}
	if !got.Networks.Private.Enabled {
		t.Error("private network enabled = false, want true")
	}
	if got.Networks.Public.Enabled {
		t.Error("public network enabled = true, want false")
	}

	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile?network=private", admin, `{}`); rec.Code != http.StatusAccepted {
		t.Fatalf("private-only reconcile = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestIPFSReconcileDisabled: same 503 ipfs_disabled contract for the reconcile
// kick, with access control.
func TestIPFSReconcileDisabled(t *testing.T) {
	srv := ipfsServer(t, testConfig())
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", admin, `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("reconcile (disabled) = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "ipfs_disabled" {
		t.Errorf("error code = %q, want ipfs_disabled", code)
	}

	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", bob, `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reconcile = %d, want 403", rec.Code)
	}
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon reconcile = %d, want 401", rec.Code)
	}
}

// TestIPFSEnabledNotImplemented: enabled but with NO mirror service wired, status
// answers an honest 501 (rather than fabricating data), and reconcile is 501
// until the real backfill lands in P19.6.
func TestIPFSEnabledNotImplemented(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	srv := ipfsServer(t, cfg)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := getWithAuth(srv, "/api/v1/ipfs/status", admin); rec.Code != http.StatusNotImplemented {
		t.Errorf("status (enabled, no mirror) = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", admin, `{}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("reconcile (enabled) = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

// TestIPFSReconcileEnabled: enabled with a wired mirror, POST /admin/ipfs/reconcile
// runs the real one-shot scan — re-arm dead-letters + seed missing pin intents —
// returning 202 with the per-class counts (schema IPFSReconcileResult). Admin-gated
// and audit-logged.
func TestIPFSReconcileEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"

	mirror := &fakeIPFSMirror{
		rearmed: 3,
		backfill: ipfsmirror.BackfillCounts{
			Total:   5,
			ByClass: map[string]int64{"video_original": 2, "thumbnail": 2, "user_avatar": 1},
		},
	}
	auditRepo := &httpAuditFakeRepo{}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror), WithAuditLog(audit.NewService(auditRepo)))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", admin, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reconcile (enabled+mirror) = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var got ipfsReconcileResultView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode reconcile result: %v", err)
	}
	if got.Enqueued != 5 {
		t.Errorf("enqueued = %d, want 5", got.Enqueued)
	}
	if got.ByClass["video_original"] != 2 || got.ByClass["thumbnail"] != 2 || got.ByClass["user_avatar"] != 1 {
		t.Errorf("by_class = %+v, want {video_original:2, thumbnail:2, user_avatar:1}", got.ByClass)
	}
	if mirror.backfillHits != 1 {
		t.Errorf("Backfill called %d times, want 1", mirror.backfillHits)
	}
	if mirror.reconcileHits != 1 {
		t.Errorf("Reconcile (re-arm) called %d times, want 1", mirror.reconcileHits)
	}

	// Audit-logged: exactly one admin.ipfs.reconcile success by the admin.
	found := false
	for _, r := range auditRepo.rows {
		if r.Action == observability.ActionIPFSReconcile && r.Result == observability.ResultSuccess {
			found = true
			if !r.ActorID.Valid {
				t.Error("reconcile audit entry missing actor_id")
			}
		}
	}
	if !found {
		t.Errorf("no admin.ipfs.reconcile success in the audit log; rows=%+v", auditRepo.rows)
	}

	// Still admin-gated.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", bob, `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reconcile = %d, want 403", rec.Code)
	}
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon reconcile = %d, want 401", rec.Code)
	}
}

// TestIPFSReconcileNetworkFilter: the optional ?network= filter (P19.P2) is validated
// and threaded to Backfill; a bogus value is a 400 and never reaches the service.
func TestIPFSReconcileNetworkFilter(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	mirror := &fakeIPFSMirror{}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// A valid ?network=private is passed straight through to Backfill.
	rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile?network=private", admin, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reconcile ?network=private = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if mirror.backfillNetwork != "private" {
		t.Errorf("Backfill network filter = %q, want private", mirror.backfillNetwork)
	}

	// No filter ⇒ both swarms (empty string).
	mirror.backfillNetwork = "sentinel"
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", admin, `{}`); rec.Code != http.StatusAccepted {
		t.Fatalf("reconcile (no filter) = %d, want 202", rec.Code)
	}
	if mirror.backfillNetwork != "" {
		t.Errorf("Backfill network filter = %q, want empty (both swarms)", mirror.backfillNetwork)
	}

	// A bogus filter is a 400 and never reaches the service (backfill not called again).
	before := mirror.backfillHits
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile?network=bogus", admin, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("reconcile ?network=bogus = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if mirror.backfillHits != before {
		t.Errorf("Backfill was invoked on a bogus network filter, want short-circuited at validation")
	}
}

// TestUpdateMeUnlistedEnqueuesReevalNotInline: toggling unlisted via PATCH /auth/me
// enqueues a durable per-user mirror re-evaluation (a cheap off-request-path write)
// exactly once; a non-unlisted profile edit does not; and a mirror enqueue error is
// swallowed (best-effort — the profile update still 200s). The provider interface
// exposes ONLY EnqueueUserReeval, so the handler structurally cannot run the SyncVideo
// fan-out inline (round-2 audit MAJOR).
func TestUpdateMeUnlistedEnqueuesReevalNotInline(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	mirror := &fakeIPFSMirror{}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror))
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// A plain profile edit (no unlisted) must NOT enqueue a re-eval.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Ada L"}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("patch display_name = %d; body=%s", rec.Code, rec.Body.String())
	}
	if mirror.reevalHits != 0 {
		t.Fatalf("reeval enqueued %d times on a non-unlisted edit, want 0", mirror.reevalHits)
	}

	// Toggling unlisted enqueues exactly one durable re-eval job and returns 200 fast.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"unlisted":true}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("patch unlisted = %d; body=%s", rec.Code, rec.Body.String())
	}
	if mirror.reevalHits != 1 {
		t.Fatalf("reeval enqueued %d times on unlisted toggle, want 1", mirror.reevalHits)
	}

	// A mirror enqueue error is best-effort: the profile update still succeeds.
	mirror.reevalErr = errors.New("queue write failed")
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"unlisted":false}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("patch unlisted=false with mirror error = %d, want 200 (best-effort); body=%s", rec.Code, rec.Body.String())
	}
	if mirror.reevalHits != 2 {
		t.Errorf("reeval enqueued %d times total, want 2", mirror.reevalHits)
	}
}

// TestIPFSStatusEnabled: enabled with a wired mirror, GET /ipfs/status returns 200
// and the real aggregated payload; still admin-gated.
func TestIPFSStatusEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	mirror := &fakeIPFSMirror{status: ipfsmirror.Status{
		Enabled: true, NodeReachable: true, GatewayURL: "https://gw.example.org",
		ClusterEnabled: false,
		Pins:           ipfsmirror.PinCounts{Pinned: 3, Pending: 1, Failed: 0, Unpinned: 2},
		ByClass: []ipfsmirror.ClassCounts{
			{MediaClass: "user_avatar", PinCounts: ipfsmirror.PinCounts{Pinned: 2}},
			{MediaClass: "thumbnail", PinCounts: ipfsmirror.PinCounts{Pinned: 1, Pending: 1}},
		},
		// P19.P2 additive per-swarm split: a public block + a separate private block.
		Networks: map[string]ipfsmirror.NetworkStatus{
			ipfsmirror.NetworkPublic: {
				Enabled: true, NodeReachable: true,
				Pins: ipfsmirror.PinCounts{Pinned: 2, Unpinned: 2},
				ByClass: []ipfsmirror.ClassCounts{
					{MediaClass: "user_avatar", PinCounts: ipfsmirror.PinCounts{Pinned: 2}},
				},
			},
			ipfsmirror.NetworkPrivate: {
				Enabled: true, NodeReachable: false,
				Pins:    ipfsmirror.PinCounts{Pinned: 1, Pending: 1},
				ByClass: []ipfsmirror.ClassCounts{{MediaClass: "thumbnail", PinCounts: ipfsmirror.PinCounts{Pinned: 1, Pending: 1}}},
			},
		},
	}}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/ipfs/status", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status (enabled+mirror) = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got ipfsStatusView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !got.Enabled || !got.NodeReachable || got.GatewayURL != "https://gw.example.org" {
		t.Errorf("status header fields wrong: %+v", got)
	}
	if got.Pins.Pinned != 3 || got.Pins.Pending != 1 || got.Pins.Unpinned != 2 {
		t.Errorf("pins = %+v, want pinned=3 pending=1 unpinned=2", got.Pins)
	}
	if len(got.ByClass) != 2 {
		t.Fatalf("by_class len = %d, want 2", len(got.ByClass))
	}
	// The additive networks split renders both swarm columns (P19.P2). The private
	// block carries its OWN reachability (independent node) and pin tally — and NEVER a
	// gateway URL or CID (there is no gateway_url field on a network block at all).
	if !got.Networks.Public.Enabled || !got.Networks.Public.NodeReachable {
		t.Errorf("networks.public = %+v, want enabled+reachable", got.Networks.Public)
	}
	if got.Networks.Public.Pins.Pinned != 2 {
		t.Errorf("networks.public.pins.pinned = %d, want 2", got.Networks.Public.Pins.Pinned)
	}
	if !got.Networks.Private.Enabled || got.Networks.Private.NodeReachable {
		t.Errorf("networks.private = %+v, want enabled + node_reachable=false (down)", got.Networks.Private)
	}
	if got.Networks.Private.Pins.Pinned != 1 || got.Networks.Private.Pins.Pending != 1 {
		t.Errorf("networks.private.pins = %+v, want pinned=1 pending=1", got.Networks.Private.Pins)
	}
	// The private swarm is replication, not distribution: the networks block carries no
	// gateway URL and no CID for either swarm (structurally — there is no such field).
	if networks := gjsonField(t, rec.Body.String(), "networks"); jsonHasKey(t, networks, "gateway_url") || jsonHasKey(t, networks, "cid") {
		t.Errorf("networks block leaked a gateway_url/cid, want none: %s", networks)
	}

	// Still admin-gated.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/ipfs/status", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", rec.Code)
	}
}

// gjsonField extracts a top-level JSON field's raw object as a string (small test
// helper for the networks-block gateway/cid-leak assertion).
func gjsonField(t *testing.T, body, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return string(m[field])
}

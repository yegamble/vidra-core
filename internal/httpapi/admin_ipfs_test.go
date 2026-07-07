package httpapi

import (
	"context"
	"encoding/json"
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
	// videoPins is returned (with ok=true) by VideoPins; pinnedIDs is the set
	// PinnedVideoIDs reports true for. Both default to empty/nothing pinned.
	videoPins map[uuid.UUID]ipfsmirror.VideoIPFS
	pinnedIDs map[uuid.UUID]bool
	// reconcile/backfill test knobs + call counters.
	rearmed       int64
	backfill      ipfsmirror.BackfillCounts
	reconcileHits int
	backfillHits  int
}

func (f *fakeIPFSMirror) Status(ctx context.Context) (ipfsmirror.Status, error) {
	return f.status, nil
}
func (f *fakeIPFSMirror) ReevaluateUser(ctx context.Context, userID uuid.UUID) error {
	f.reevalHits++
	return nil
}
func (f *fakeIPFSMirror) Reconcile(ctx context.Context) (int64, error) {
	f.reconcileHits++
	return f.rearmed, nil
}
func (f *fakeIPFSMirror) Backfill(ctx context.Context) (ipfsmirror.BackfillCounts, error) {
	f.backfillHits++
	return f.backfill, nil
}
func (f *fakeIPFSMirror) VideoPins(ctx context.Context, videoID uuid.UUID) (ipfsmirror.VideoIPFS, bool, error) {
	if p, ok := f.videoPins[videoID]; ok {
		return p, true, nil
	}
	return ipfsmirror.VideoIPFS{}, false, nil
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

	// Still admin-gated.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/ipfs/status", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", rec.Code)
	}
}

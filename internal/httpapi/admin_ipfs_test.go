package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
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
}

func (f *fakeIPFSMirror) Status(ctx context.Context) (ipfsmirror.Status, error) {
	return f.status, nil
}
func (f *fakeIPFSMirror) ReevaluateUser(ctx context.Context, userID uuid.UUID) error {
	f.reevalHits++
	return nil
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

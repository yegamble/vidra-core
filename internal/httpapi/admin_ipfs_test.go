package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
)

// ipfsServer builds an auth-enabled server with the given config so the IPFS
// admin stubs can be exercised with real admin/non-admin/anon principals.
func ipfsServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
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

// TestIPFSEnabledNotImplemented: when the mirror is enabled but the subsystem is
// not yet wired (P19.1), the stubs answer an honest 501 not_implemented rather
// than fabricating data.
func TestIPFSEnabledNotImplemented(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	srv := ipfsServer(t, cfg)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := getWithAuth(srv, "/api/v1/ipfs/status", admin); rec.Code != http.StatusNotImplemented {
		t.Errorf("status (enabled) = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postJSONWithAuth(srv, "/api/v1/admin/ipfs/reconcile", admin, `{}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("reconcile (enabled) = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

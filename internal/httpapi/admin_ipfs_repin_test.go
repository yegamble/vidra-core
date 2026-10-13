package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/observability"
)

type fakeRepinMirror struct {
	*fakeIPFSMirror
	status     ipfsmirror.RepinStatus
	startErr   error
	startHits  int
	cancelHits int
	cancelErr  error
}

func (f *fakeRepinMirror) StartRepin(_ context.Context) (ipfsmirror.RepinStatus, error) {
	f.startHits++
	return f.status, f.startErr
}
func (f *fakeRepinMirror) CancelRepin(_ context.Context) (ipfsmirror.RepinStatus, error) {
	f.cancelHits++
	return f.status, f.cancelErr
}
func (f *fakeRepinMirror) RepinStatus(_ context.Context) (ipfsmirror.RepinStatus, error) {
	return f.status, nil
}

// The re-pin routes are admin-only, audited, report the run, and refuse a second start.
func TestIPFSRepinRoutes(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://ipfs:5001"
	cfg.IPFSGatewayURL = "https://gw.example.org"
	mirror := &fakeRepinMirror{fakeIPFSMirror: &fakeIPFSMirror{}, status: ipfsmirror.RepinStatus{Phase: ipfsmirror.RepinRunning, Remaining: 4, Done: 2}}
	auditRepo := &httpAuditFakeRepo{}
	srv := ipfsServer(t, cfg, WithIPFSMirrorService(mirror), WithAuditLog(audit.NewService(auditRepo)))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	const path = "/api/v1/admin/ipfs/repin-playback-only"

	// Not an admin, or not signed in: neither route reaches the service.
	for _, tok := range []string{bob, ""} {
		want := http.StatusForbidden
		if tok == "" {
			want = http.StatusUnauthorized
		}
		if rec := postJSONWithAuth(srv, path, tok, `{}`); rec.Code != want {
			t.Errorf("POST as %q = %d, want %d", tok, rec.Code, want)
		}
		if rec := getWithAuth(srv, path, tok); rec.Code != want {
			t.Errorf("GET as %q = %d, want %d", tok, rec.Code, want)
		}
		if rec := deleteJSONWithAuth(srv, path, tok, ""); rec.Code != want {
			t.Errorf("DELETE as %q = %d, want %d", tok, rec.Code, want)
		}
	}
	if mirror.startHits != 0 || mirror.cancelHits != 0 {
		t.Fatalf("StartRepin reached by a non-admin (%d calls)", mirror.startHits)
	}

	rec := postJSONWithAuth(srv, path, admin, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var got ipfsRepinView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Phase != "running" || got.Remaining != 4 || got.Done != 2 {
		t.Fatalf("start body = %+v err=%v", got, err)
	}
	if rec := getWithAuth(srv, path, admin); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	found := false
	for _, r := range auditRepo.rows {
		found = found || (r.Action == observability.ActionIPFSRepin && r.Result == observability.ResultSuccess && r.ActorID.Valid)
	}
	if !found {
		t.Errorf("no admin.ipfs.repin success with an actor in the audit log: %+v", auditRepo.rows)
	}

	if rec := deleteJSONWithAuth(srv, path, admin, ""); rec.Code != http.StatusOK || mirror.cancelHits != 1 {
		t.Errorf("cancel = %d (hits %d), want 200", rec.Code, mirror.cancelHits)
	}
	cancelled := false
	for _, r := range auditRepo.rows {
		cancelled = cancelled || (r.Action == observability.ActionIPFSRepinCancel && r.Result == observability.ResultSuccess)
	}
	if !cancelled {
		t.Error("no admin.ipfs.repin.cancel audit row")
	}
	mirror.cancelErr = ipfsmirror.ErrRepinNotRunning
	if rec := deleteJSONWithAuth(srv, path, admin, ""); rec.Code != http.StatusConflict {
		t.Errorf("cancel with nothing running = %d, want 409", rec.Code)
	}
	mirror.startErr = ipfsmirror.ErrRepinRunning
	if rec := postJSONWithAuth(srv, path, admin, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("start while running = %d, want 409", rec.Code)
	}
}

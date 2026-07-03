package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/peertubeimport"
)

// fakePTImport is a static peerTubeImportProvider for handler tests.
type fakePTImport struct {
	configured bool
	run        peertubeimport.Run
	createErr  error
	getErr     error
}

func (f fakePTImport) Configured() bool { return f.configured }
func (f fakePTImport) CreateRun(context.Context, string, peertubeimport.ConflictPolicy, uuid.UUID) (peertubeimport.Run, error) {
	if f.createErr != nil {
		return peertubeimport.Run{}, f.createErr
	}
	return f.run, nil
}
func (f fakePTImport) GetRun(context.Context, uuid.UUID) (peertubeimport.Run, error) {
	if f.getErr != nil {
		return peertubeimport.Run{}, f.getErr
	}
	return f.run, nil
}
func (f fakePTImport) ListRuns(context.Context, int32, int32) ([]peertubeimport.Run, error) {
	return []peertubeimport.Run{f.run}, nil
}

func ptImportServer(t *testing.T, provider peerTubeImportProvider) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute), WithPeerTubeImportService(provider))
}

func TestPeerTubeImportLaunch(t *testing.T) {
	run := peertubeimport.Run{ID: uuid.New(), Mode: "dry_run", State: "pending", ConflictPolicy: "skip"}
	srv := ptImportServer(t, fakePTImport{configured: true, run: run})
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// admin launch → 202
	rec := postJSONWithAuth(srv, "/api/v1/admin/peertube-import", admin, `{"mode":"dry_run"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launch = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// non-admin 403, anon 401
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := postJSONWithAuth(srv, "/api/v1/admin/peertube-import", bob, `{"mode":"dry_run"}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin launch = %d, want 403", rec.Code)
	}
	if rec := postJSONWithAuth(srv, "/api/v1/admin/peertube-import", "", `{"mode":"dry_run"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon launch = %d, want 401", rec.Code)
	}

	// bad conflict policy → 400 (rejected before the service is called)
	if rec := postJSONWithAuth(srv, "/api/v1/admin/peertube-import", admin, `{"mode":"run","conflict_policy":"bogus"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad policy = %d, want 400", rec.Code)
	}
}

func TestPeerTubeImportLaunchErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not configured", peertubeimport.ErrNotConfigured, http.StatusServiceUnavailable},
		{"busy", peertubeimport.ErrBusy, http.StatusConflict},
		{"invalid mode", peertubeimport.ErrInvalidMode, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := ptImportServer(t, fakePTImport{configured: true, createErr: c.err})
			admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
			rec := postJSONWithAuth(srv, "/api/v1/admin/peertube-import", admin, `{"mode":"run"}`)
			if rec.Code != c.want {
				t.Errorf("%s launch = %d, want %d; body=%s", c.name, rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestPeerTubeImportListAndGet(t *testing.T) {
	run := peertubeimport.Run{ID: uuid.New(), Mode: "run", State: "done", ConflictPolicy: "skip"}
	srv := ptImportServer(t, fakePTImport{configured: true, run: run})
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := getWithAuth(srv, "/api/v1/admin/peertube-import", admin); rec.Code != http.StatusOK {
		t.Errorf("list = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/admin/peertube-import/"+run.ID.String(), admin); rec.Code != http.StatusOK {
		t.Errorf("get = %d, want 200", rec.Code)
	}
	// A bad uuid → 400.
	if rec := getWithAuth(srv, "/api/v1/admin/peertube-import/not-a-uuid", admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", rec.Code)
	}
	// Unknown id (service returns ErrRunNotFound) → 404.
	srv2 := ptImportServer(t, fakePTImport{configured: true, getErr: peertubeimport.ErrRunNotFound})
	admin2 := registerAndToken(t, srv2, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv2, "/api/v1/admin/peertube-import/"+uuid.New().String(), admin2); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}

	// non-admin 403, anon 401 on the list.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/peertube-import", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/peertube-import", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}
}

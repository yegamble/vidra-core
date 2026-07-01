package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
)

// approvalServer builds an auth-enabled server whose config it also returns, so
// the test can flip RegistrationRequireApproval at runtime (the handler reads
// s.cfg live). Registration starts OPEN so the first account (the admin) can be
// created before approval mode is switched on.
func approvalServer(t *testing.T) (*Server, *config.Config) {
	t.Helper()
	cfg := testConfig()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	srv := New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
	return srv, cfg
}

func TestRegistrationApprovalFlow(t *testing.T) {
	srv, cfg := approvalServer(t)

	// First account (approval still off) becomes admin.
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Turn on approval; now signups are queued, not created.
	cfg.RegistrationRequireApproval = true

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","note":"let me in"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register in approval mode = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var pending map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &pending)
	if pending["status"] != "pending" {
		t.Errorf("body = %v, want status=pending", pending)
	}
	// No token or account: bob cannot log in yet.
	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`); rec.Code == http.StatusOK {
		t.Error("bob should not be able to log in before approval")
	}

	// The admin sees bob in the pending queue.
	rec = getWithAuth(srv, "/api/v1/admin/registration-requests?status=pending", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list registrationRequestListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Requests) != 1 || list.Requests[0].Username != "bob" || list.Requests[0].Status != "pending" {
		t.Fatalf("queue = %+v, want one pending bob", list.Requests)
	}
	bobReqID := list.Requests[0].ID

	// A regular user (once bob exists) or anon cannot use the admin endpoints.
	if rec := getWithAuth(srv, "/api/v1/admin/registration-requests", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}

	// Approve bob → 204, then bob can log in (the account was created).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+bobReqID+"/approve", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`); rec.Code != http.StatusOK {
		t.Errorf("bob login after approval = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Approving an unknown/resolved id → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+uuid.New().String()+"/approve", "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("approve unknown = %d, want 404", rec.Code)
	}

	// Now bob is a regular user; he cannot see the queue.
	var bobResp authResponse
	_ = json.Unmarshal(postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`).Body.Bytes(), &bobResp)
	if rec := getWithAuth(srv, "/api/v1/admin/registration-requests", bobResp.Token); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list = %d, want 403", rec.Code)
	}
}

func TestRegistrationRejectFlow(t *testing.T) {
	srv, cfg := approvalServer(t)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	cfg.RegistrationRequireApproval = true

	if rec := postTo(srv, "/api/v1/auth/register", `{"username":"carol","email":"carol@example.test","password":"supersecret"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("register carol = %d, want 202", rec.Code)
	}
	var list registrationRequestListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/registration-requests?status=pending", admin).Body.Bytes(), &list)
	if len(list.Requests) != 1 {
		t.Fatalf("queue = %d, want 1", len(list.Requests))
	}
	id := list.Requests[0].ID

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+id+"/reject", `{"note":"not now"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reject = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Rejected → carol cannot log in, and the request drops out of the pending queue.
	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"carol@example.test","password":"supersecret"}`); rec.Code == http.StatusOK {
		t.Error("carol should not be able to log in after rejection")
	}
	var after registrationRequestListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/registration-requests?status=pending", admin).Body.Bytes(), &after)
	if len(after.Requests) != 0 {
		t.Errorf("pending after reject = %d, want 0", len(after.Requests))
	}
	// Rejecting again → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+id+"/reject", `{}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("re-reject = %d, want 404", rec.Code)
	}
}

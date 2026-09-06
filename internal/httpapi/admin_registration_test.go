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

// TestRegistrationRequestsStatusFilterIsValidated mirrors the reports fix: the
// handler used to be `c.QueryParam("status") == "pending"`, so ?status=approved
// silently returned the whole queue.
func TestRegistrationRequestsStatusFilterIsValidated(t *testing.T) {
	srv, cfg := approvalServer(t)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	cfg.RegistrationRequireApproval = true
	for _, u := range []string{"bob", "cass"} {
		if rec := postTo(srv, "/api/v1/auth/register",
			`{"username":"`+u+`","email":"`+u+`@example.test","password":"supersecret"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("register %s = %d; body=%s", u, rec.Code, rec.Body.String())
		}
	}
	list := func(query string) registrationRequestListResponse {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/admin/registration-requests"+query, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d; body=%s", query, rec.Code, rec.Body.String())
		}
		var out registrationRequestListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	pending := list("?status=pending")
	if pending.Total != 2 {
		t.Fatalf("pending total = %d, want 2", pending.Total)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+pending.Requests[0].ID+"/approve", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+pending.Requests[1].ID+"/reject", `{"note":"no"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reject = %d", rec.Code)
	}

	for _, tc := range []struct {
		query string
		want  int64
	}{
		{query: "?status=pending", want: 0},
		// The bug: both of these used to return 2.
		{query: "?status=approved", want: 1},
		{query: "?status=rejected", want: 1},
		{query: "?status=all", want: 2},
		{query: "", want: 2},
	} {
		if got := list(tc.query); got.Total != tc.want || int64(len(got.Requests)) != tc.want {
			t.Errorf("registration-requests%s = %d rows / total %d, want %d", tc.query, len(got.Requests), got.Total, tc.want)
		}
	}

	if rec := getWithAuth(srv, "/api/v1/admin/registration-requests?status=nonsense", admin); rec.Code != http.StatusBadRequest {
		t.Errorf("status=nonsense = %d, want 400", rec.Code)
	}
}

// approvalServerWithMailer is approvalServer plus a capturing mailer, for the
// signup-decision notices (A16). Registration starts OPEN so the first account
// (the admin) exists before approval mode is switched on.
func approvalServerWithMailer(t *testing.T) (*Server, *config.Config, *captureResetMailer) {
	t.Helper()
	cfg := testConfig()
	cfg.PublicBaseURL = "https://videos.example"
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	svc := auth.NewService(repo, issuer, 720*time.Hour,
		auth.WithMailer(mailer), auth.WithPublicBaseURL(cfg.PublicBaseURL))
	srv := New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
	return srv, cfg, mailer
}

// TestRegistrationDecisionsMailTheApplicant covers SC5 over HTTP: the decision
// routes notify the applicant, and the refusals notify nobody — a moderator's
// 403 must not leak an "approved" mail to somebody whose request is untouched.
func TestRegistrationDecisionsMailTheApplicant(t *testing.T) {
	srv, cfg, mailer := approvalServerWithMailer(t)
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	// An ordinary account, created while signups are still open, to attempt the
	// decisions the queue must refuse.
	outsiderTok := registerAndToken(t, srv, `{"username":"dana","email":"dana@example.test","password":"supersecret"}`)
	cfg.RegistrationRequireApproval = true

	apply := func(name string) string {
		t.Helper()
		if rec := postTo(srv, "/api/v1/auth/register",
			`{"username":"`+name+`","email":"`+name+`@example.test","password":"supersecret"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("apply %s = %d; body=%s", name, rec.Code, rec.Body.String())
		}
		rec := getWithAuth(srv, "/api/v1/admin/registration-requests?status=pending", adminTok)
		var list registrationRequestListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &list)
		for _, r := range list.Requests {
			if r.Username == name {
				return r.ID
			}
		}
		t.Fatalf("applicant %s not in the pending queue", name)
		return ""
	}

	approveID := apply("bob")
	rejectID := apply("cleo")

	// Applying notifies nobody: the decision is what the applicant is waiting on.
	if len(mailer.regDecisions) != 0 {
		t.Fatalf("notices after two applications = %+v, want none", mailer.regDecisions)
	}

	// A non-admin is refused on both, and mails nothing.
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+approveID+"/approve", outsiderTok, ""); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin approve = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+rejectID+"/reject", outsiderTok, `{"note":"no"}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reject = %d, want 403", rec.Code)
	}
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+approveID+"/approve", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous approve = %d, want 401", rec.Code)
	}
	if len(mailer.regDecisions) != 0 {
		t.Fatalf("notices after the refusals = %+v, want none", mailer.regDecisions)
	}

	// The admin's approval mails the applicant, with the instance's sign-in page.
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+approveID+"/approve", adminTok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(mailer.regDecisions) != 1 {
		t.Fatalf("notices after approve = %+v, want one", mailer.regDecisions)
	}
	got := mailer.regDecisions[0]
	if got.Decision != "approved" || got.Email != "bob@example.test" || got.SignInURL != "https://videos.example/login" {
		t.Errorf("approval notice = %+v, want approved bob with the sign-in URL", got)
	}

	// And the rejection carries the reviewer's note.
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+rejectID+"/reject", adminTok, `{"note":"not this time"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("reject = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(mailer.regDecisions) != 2 {
		t.Fatalf("notices after reject = %+v, want two", mailer.regDecisions)
	}
	got = mailer.regDecisions[1]
	if got.Decision != "rejected" || got.Email != "cleo@example.test" || got.Note != "not this time" {
		t.Errorf("rejection notice = %+v, want rejected cleo with the note", got)
	}

	// A repeat decision is 404 and mails nothing more.
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+approveID+"/approve", adminTok, ""); rec.Code != http.StatusNotFound {
		t.Errorf("repeat approve = %d, want 404", rec.Code)
	}
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+rejectID+"/reject", adminTok, `{"note":"again"}`); rec.Code != http.StatusNotFound {
		t.Errorf("repeat reject = %d, want 404", rec.Code)
	}
	if len(mailer.regDecisions) != 2 {
		t.Errorf("notices after the repeats = %+v, want still two", mailer.regDecisions)
	}
}

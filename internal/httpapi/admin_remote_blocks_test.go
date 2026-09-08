package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/federation"
)

// The ADMIN half of the per-remote-account block (A29 parity): one remote person
// removed for everyone, instead of defederating the whole server they live on.

// adminRemoteBlockServer registers an admin (the first account) and an ordinary
// user, so the authorization half is testable and not assumed.
func adminRemoteBlockServer(t *testing.T) (srv *Server, admin, user string) {
	t.Helper()
	cfg := fedTestConfig()
	repo := newFedRepoFor(cfg)
	repo.remoteBlocks = map[string]bool{}
	repo.adminActorBlocks = map[string]string{}
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	srv = New(cfg, nil, nil,
		WithAuthService(auth.NewService(authRepo, issuer, 720*time.Hour), 15*time.Minute),
		WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))),
	)
	admin = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	user = registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	return srv, admin, user
}

func TestAdminRemoteActorBlockIsModeratorOnly(t *testing.T) {
	srv, admin, user := adminRemoteBlockServer(t)
	const actor = "https://peer.example/accounts/kaisa"

	if rec := getWith(srv, "/api/v1/admin/federation/blocked-actors", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}
	if rec := getWith(srv, "/api/v1/admin/federation/blocked-actors", user, ""); rec.Code != http.StatusForbidden {
		t.Errorf("ordinary user list = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/federation/blocked-actors",
		`{"actor":"`+actor+`"}`, user); rec.Code != http.StatusForbidden {
		t.Errorf("ordinary user block = %d, want 403", rec.Code)
	}
	if rec := getWith(srv, "/api/v1/admin/federation/blocked-actors", admin, ""); rec.Code != http.StatusOK {
		t.Errorf("admin list = %d, want 200", rec.Code)
	}
}

func TestAdminRemoteActorBlockFlow(t *testing.T) {
	srv, admin, _ := adminRemoteBlockServer(t)
	const actor = "https://peer.example/accounts/kaisa"

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/federation/blocked-actors",
		`{"actor":"`+actor+`","reason":"sustained harassment"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Idempotent, and a re-block with no reason keeps the first one — a
	// moderator's note is the only prose on the row and must not be erasable by
	// a careless repeat.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/federation/blocked-actors",
		`{"actor":"`+actor+`"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("re-block = %d", rec.Code)
	}

	var list struct {
		Actors []blockedRemoteActorView `json:"actors"`
		Total  int64                    `json:"total"`
	}
	rec := getWith(srv, "/api/v1/admin/federation/blocked-actors", admin, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 1 || len(list.Actors) != 1 || list.Actors[0].ActorURL != actor {
		t.Fatalf("list = %+v, want the one blocked actor", list)
	}
	if list.Actors[0].Reason != "sustained harassment" {
		t.Errorf("reason = %q, want the first note kept", list.Actors[0].Reason)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete,
		"/api/v1/admin/federation/blocked-actors?actor="+actor, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Idempotent in the other direction too.
	if rec := sendJSONAuth(srv, http.MethodDelete,
		"/api/v1/admin/federation/blocked-actors?actor="+actor, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("re-unblock = %d", rec.Code)
	}
	rec = getWith(srv, "/api/v1/admin/federation/blocked-actors", admin, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 0 {
		t.Errorf("after unblock total = %d, want 0", list.Total)
	}
}

// A LOCAL identity is refused rather than silently stored: an admin who wants a
// local account gone has an account surface for it, and a local actor url in the
// remote block table would be a row nothing ever reads.
func TestAdminRemoteActorBlockRefusesLocalAndMalformedIdentities(t *testing.T) {
	srv, admin, _ := adminRemoteBlockServer(t)
	cfg := fedTestConfig()

	for name, body := range map[string]string{
		"empty":     `{"actor":""}`,
		"plaintext": `{"actor":"just a name"}`,
		"local":     `{"actor":"` + cfg.PublicBaseURL + `/accounts/ada"}`,
	} {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/federation/blocked-actors", body, admin)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s identity = %d, want 422; body=%s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := sendJSONAuth(srv, http.MethodDelete,
		"/api/v1/admin/federation/blocked-actors", "", admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unblock with no actor = %d, want 422", rec.Code)
	}
}

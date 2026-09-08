package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/federation"
)

// remoteBlockServer builds an auth + federation server over the in-memory fed
// repo, and returns a token for a registered caller.
func remoteBlockServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := fedTestConfig()
	repo := newFedRepoFor(cfg)
	repo.remoteBlocks = map[string]bool{}
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	srv := New(cfg, nil, nil,
		WithAuthService(auth.NewService(authRepo, issuer, 720*time.Hour), 15*time.Minute),
		WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))),
	)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	return srv, tok
}

// A29-F7: per-remote-ACCOUNT blocks. Before them the only control a viewer had
// against one remote person was blocking their whole instance.

func TestRemoteBlockAcceptsAnActorURLAndListsIt(t *testing.T) {
	srv, tok := remoteBlockServer(t)
	const actor = "https://peer.example/accounts/kaisa"

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/remote", `{"actor":"`+actor+`"}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Idempotent.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/remote", `{"actor":"`+actor+`"}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("re-block = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := getWith(srv, "/api/v1/me/blocks/remote", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Actors []remoteBlockView `json:"actors"`
		Total  int64             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 1 || len(list.Actors) != 1 || list.Actors[0].ActorURL != actor {
		t.Fatalf("list = %+v, want the one blocked actor", list)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/remote?actor="+actor, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = getWith(srv, "/api/v1/me/blocks/remote", tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Total != 0 {
		t.Errorf("after unblock total = %d, want 0", list.Total)
	}
}

// A local identity belongs on /me/blocks/{id}; saying so is better than silently
// storing a block that can never match an actor URL.
func TestRemoteBlockRefusesLocalAndMalformedIdentities(t *testing.T) {
	srv, tok := remoteBlockServer(t)
	for name, body := range map[string]string{
		"local handle": `{"actor":"@ada@videos.example"}`,
		"local URL":    `{"actor":"https://videos.example/accounts/ada"}`,
		"plain text":   `{"actor":"kaisa"}`,
		"empty":        `{"actor":"  "}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/remote", body, tok)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRemoteBlockRequiresAuth(t *testing.T) {
	srv, _ := remoteBlockServer(t)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/remote", `{"actor":"https://peer.example/a"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous block = %d, want 401", rec.Code)
	}
	if rec := getWith(srv, "/api/v1/me/blocks/remote", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d, want 401", rec.Code)
	}
}

// The stored URL is what unblock matches, verbatim — a handle whose WebFinger
// has since started failing must never strand a viewer with an unliftable block.
func TestUnblockMatchesTheStoredURLWithoutResolving(t *testing.T) {
	srv, tok := remoteBlockServer(t)
	const actor = "https://gone.example/accounts/ghost"
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/remote", `{"actor":"`+actor+`"}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/remote?actor="+actor, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnblockWithoutAnActorIs422(t *testing.T) {
	srv, tok := remoteBlockServer(t)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/remote", "", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

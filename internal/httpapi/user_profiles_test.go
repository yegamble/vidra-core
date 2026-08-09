package httpapi

// Tests for showing a user's linked Bluesky/ATProto sign-in handle on their
// public profile, gated by the per-user show_bluesky opt-in (default hidden).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// blueskyProfileServer wires real auth + oauth + channel services over the
// in-memory oauth fake (which carries oauth identities), so the profile handler
// can resolve a linked ATProto handle.
func blueskyProfileServer(t *testing.T) (*Server, *oauthHTTPFakeRepo) {
	t.Helper()
	issuer := auth.NewTokenIssuer("bluesky-test-secret-bluesky-test-0", "vidra", "vidra", 15*time.Minute)
	repo := newOAuthHTTPFakeRepo()
	authsvc := auth.NewService(repo, issuer, 720*time.Hour)
	oauthsvc := auth.NewOAuthService(repo, authsvc, nil)
	chRepo := newChannelFakeRepo()
	chRepo.users = repo.authFakeRepo
	srv := New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithOAuthService(oauthsvc),
		WithChannelService(channel.NewService(chRepo)),
	)
	return srv, repo
}

func TestUserProfileBlueskyHandleGatedByOptIn(t *testing.T) {
	srv, repo := blueskyProfileServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Link an ATProto sign-in identity carrying a handle (as the login flow does).
	ada := repo.users["ada@example.test"]
	handle := "ada.bsky.social"
	if _, err := repo.CreateOAuthIdentity(context.Background(), sqlcgen.CreateOAuthIdentityParams{
		Provider: "atproto", Subject: "did:plc:ada", UserID: ada.ID, Email: "", Handle: &handle,
	}); err != nil {
		t.Fatalf("link atproto identity: %v", err)
	}

	// Publish the profile (still show_bluesky=false by default).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"profile_public":true}`, token); rec.Code != http.StatusOK {
		t.Fatalf("publish profile = %d; body=%s", rec.Code, rec.Body.String())
	}

	path := "/api/v1/users/ada/profile"
	getProfile := func(tok string) publicUserProfileView {
		t.Helper()
		rec := getWithAuth(srv, path, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET profile = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var p publicUserProfileView
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode profile: %v", err)
		}
		return p
	}

	// Default (opt-out): the handle is linked but NOT exposed.
	if p := getProfile(""); p.BlueskyHandle != nil {
		t.Fatalf("bluesky_handle exposed while opted out: %v", *p.BlueskyHandle)
	}

	// Opt in.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"show_bluesky":true}`, token); rec.Code != http.StatusOK {
		t.Fatalf("opt-in = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Now a public (anonymous) visitor sees the handle...
	if p := getProfile(""); p.BlueskyHandle == nil || *p.BlueskyHandle != handle {
		t.Fatalf("public bluesky_handle = %v, want %s", p.BlueskyHandle, handle)
	}
	// ...and the owner's own-profile preview matches.
	if p := getProfile(token); p.BlueskyHandle == nil || *p.BlueskyHandle != handle {
		t.Fatalf("owner preview bluesky_handle = %v, want %s", p.BlueskyHandle, handle)
	}
}

func TestUpdateMeShowBlueskyPersistsAndIsReflected(t *testing.T) {
	srv, _ := blueskyProfileServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	me := func() userView {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/auth/me", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET me = %d; body=%s", rec.Code, rec.Body.String())
		}
		var v userView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode me: %v", err)
		}
		return v
	}

	if me().ShowBluesky {
		t.Fatal("show_bluesky must default to false")
	}
	// show_bluesky alone is a valid single-field update.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"show_bluesky":true}`, token); rec.Code != http.StatusOK {
		t.Fatalf("PATCH show_bluesky = %d; body=%s", rec.Code, rec.Body.String())
	}
	if !me().ShowBluesky {
		t.Fatal("show_bluesky did not persist")
	}
}

func TestListOAuthIdentitiesExposesHandle(t *testing.T) {
	srv, repo := blueskyProfileServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	ada := repo.users["ada@example.test"]
	handle := "ada.bsky.social"
	if _, err := repo.CreateOAuthIdentity(context.Background(), sqlcgen.CreateOAuthIdentityParams{
		Provider: "atproto", Subject: "did:plc:ada", UserID: ada.ID, Email: "", Handle: &handle,
	}); err != nil {
		t.Fatalf("link atproto identity: %v", err)
	}

	rec := getWithAuth(srv, "/api/v1/me/oauth-identities", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET oauth-identities = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp oauthIdentitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Identities) != 1 {
		t.Fatalf("identities = %d, want 1", len(resp.Identities))
	}
	got := resp.Identities[0]
	if got.Provider != "atproto" || got.Handle == nil || *got.Handle != handle {
		t.Fatalf("identity = %+v, want atproto handle %s", got, handle)
	}
}

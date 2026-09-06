package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
)

// authedRoutes are the routes these tests probe to prove revocation reaches
// EVERY authenticated route, not just the handful that happen to load the user
// row. PATCH /auth/me is the load-bearing one: before this slice a revoked,
// deactivated or hard-deleted account's unexpired access token still answered
// 200 there (recorded in the A12 deletion evidence).
var authedRoutes = []struct {
	method string
	path   string
	body   string
}{
	{http.MethodGet, "/api/v1/auth/me", ""},
	{http.MethodPatch, "/api/v1/auth/me", `{"display_name":"still here"}`},
	{http.MethodPost, "/api/v1/auth/logout-all", ""},
}

func assertAllRoutesUnauthorized(t *testing.T, srv *Server, token, when string) {
	t.Helper()
	for _, r := range authedRoutes {
		rec := sendJSONAuth(srv, r.method, r.path, r.body, token)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %s %s = %d, want 401; body=%s", when, r.method, r.path, rec.Code, rec.Body.String())
		}
	}
}

// TestRevokedSessionKillsTheAccessToken — "sign out everywhere" must mean the
// access token too, not just the refresh token.
func TestRevokedSessionKillsTheAccessToken(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/logout-all", "", reg.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("logout-all = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	assertAllRoutesUnauthorized(t, srv, reg.Token, "after logout-all")
}

// TestDeactivatedAccountAccessTokenIsRejectedEverywhere — the A12 deletion slice
// proved a deactivated account could still PATCH its profile and create content
// for the rest of JWT_ACCESS_TTL. It must not.
func TestDeactivatedAccountAccessTokenIsRejectedEverywhere(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate",
		`{"password":"supersecret"}`, reg.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("deactivate = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	assertAllRoutesUnauthorized(t, srv, reg.Token, "after deactivate")
}

// TestChangePasswordRevokesOtherSessionsAccessTokens is SC2's core claim: the
// OTHER browser's already-issued access token stops working immediately, while
// the changer's own keeps working.
func TestChangePasswordRevokesOtherSessionsAccessTokens(t *testing.T) {
	srv := authServer(t)
	first := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	loginRec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("second login = %d, want 200; body=%s", loginRec.Code, loginRec.Body.String())
	}
	var second authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal second session: %v", err)
	}
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", second.Token); me.Code != http.StatusOK {
		t.Fatalf("second session is not usable before the change: %d", me.Code)
	}

	// The SECOND session changes the password; the FIRST must die.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		`{"current_password":"supersecret","new_password":"evenmoresecret"}`, second.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("change password = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	assertAllRoutesUnauthorized(t, srv, first.Token, "other session after password change")
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", second.Token); me.Code != http.StatusOK {
		t.Errorf("the changer's own session died: /auth/me = %d, want 200", me.Code)
	}
	// The other session's refresh token is dead too.
	if rec := postTo(srv, "/api/v1/auth/refresh",
		`{"refresh_token":"`+first.RefreshToken+`"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("other session refresh after password change = %d, want 401", rec.Code)
	}
}

// TestAccessTokenWithoutASessionClaimIsRejected pins the fail-closed rule: an
// access token that names no session cannot be checked against a revocation, so
// it is refused. Only the previous binary could mint one, and its holder's
// refresh token still works, so the client re-authenticates transparently.
func TestAccessTokenWithoutASessionClaimIsRejected(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	claims, err := srv.authsvc.Parse(reg.Token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	legacy := mintSessionlessToken(t, claims.Subject, claims.Role)
	assertAllRoutesUnauthorized(t, srv, legacy, "session-less token")
}

// mintSessionlessToken forges the shape the PREVIOUS binary minted: a valid
// access token with no "sid" claim. It uses the same secret/issuer/audience as
// authServer, so only the missing session claim distinguishes it.
func mintSessionlessToken(t *testing.T, subject, role string) string {
	t.Helper()
	id, err := uuid.Parse(subject)
	if err != nil {
		t.Fatalf("parse subject: %v", err)
	}
	tok, err := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute).Issue(id, role)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}

// clearPasswordHash puts the account into the OAuth/ATProto-only shape (empty
// stored hash) that no endpoint can produce.
func (f *authFakeRepo) clearPasswordHash(t *testing.T, userID string) {
	t.Helper()
	id, err := uuid.Parse(userID)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	for k, u := range f.users {
		if u.ID == id {
			u.PasswordHash = ""
			f.users[k] = u
			return
		}
	}
	t.Fatalf("user %s not in the fake repo", userID)
}

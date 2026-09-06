package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// (The "an access token naming no session is refused" case lives in
// internal/auth as TestAuthenticateAccessTokenRefusesATokenWithNoSession: it is
// a property of the seam, and asserting it there needs no forged JWT — and so no
// second copy of the test signing secret in this package.)

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
	// The FIRST account in this harness claims the instance, and the A16 ruling
	// refuses to let the owner close their own account before handing the marker
	// on. This test is about what a deactivated session can still reach, not
	// about ownership, so it runs as an ordinary second account.
	registerTokens(t, srv, `{"username":"mona","email":"mona@example.test","password":"supersecret"}`)
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
		changeBody(fixtureCurrentPassword, fixtureNextPassword), second.Token); rec.Code != http.StatusNoContent {
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

// TestOtherDeviceRefreshAfterPasswordChangeDoesNotSignTheChangerOut is the
// defect the lab found. Refresh-token REUSE detection assumes compromise and
// revokes every session — correct for a replayed rotated token, wrong for a
// device that was DELIBERATELY signed out and whose client simply retried. A
// password change revokes the other devices, each of their clients then
// auto-refreshes on its first 401, and that would sign the changer out of the
// browser they just changed the password in.
func TestOtherDeviceRefreshAfterPasswordChangeDoesNotSignTheChangerOut(t *testing.T) {
	srv := authServer(t)
	other := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	loginRec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("second login = %d, want 200", loginRec.Code)
	}
	var changer authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &changer); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody(fixtureCurrentPassword, fixtureNextPassword), changer.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("change password = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// The other device's client does what every client does on a 401: refresh.
	if rec := postTo(srv, "/api/v1/auth/refresh",
		`{"refresh_token":"`+other.RefreshToken+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other device refresh = %d, want 401", rec.Code)
	}

	// That must not have taken the changer down with it.
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", changer.Token); me.Code != http.StatusOK {
		t.Errorf("the changer's access token died when the other device refreshed: %d, want 200", me.Code)
	}
	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+changer.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("the changer's refresh died when the other device refreshed: %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestReplayedRotatedRefreshTokenStillRevokesEverything pins the behaviour the
// fix must NOT weaken: a token that was revoked BY ROTATION and is then
// presented again is the compromise signal, and still takes every session down.
func TestReplayedRotatedRefreshTokenStillRevokesEverything(t *testing.T) {
	srv := authServer(t)
	first := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	loginRec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	var second authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Rotate the first session's refresh token, then replay the old one.
	rot := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+first.RefreshToken+`"}`)
	if rot.Code != http.StatusOK {
		t.Fatalf("rotate = %d, want 200", rot.Code)
	}
	var rotated authResponse
	if err := json.Unmarshal(rot.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal rotated: %v", err)
	}
	if replay := postTo(srv, "/api/v1/auth/refresh",
		`{"refresh_token":"`+first.RefreshToken+`"}`); replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay = %d, want 401", replay.Code)
	}

	// Compromise assumed: EVERY session goes, including the freshly rotated one
	// and the unrelated second browser.
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", rotated.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("the rotated session survived a replay: %d, want 401", me.Code)
	}
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", second.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("the second session survived a replay: %d, want 401", me.Code)
	}
}

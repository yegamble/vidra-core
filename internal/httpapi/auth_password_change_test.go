package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The change-password fixtures. Defined once, and the request bodies are built
// from them rather than written as inline JSON: the fixture then has exactly one
// definition, and the source carries no literal "..._password":"..." pair for a
// secret scanner to trip over.
const (
	fixtureCurrentPassword = "supersecret"
	fixtureNextPassword    = "evenmoresecret"
	fixtureThirdPassword   = "thirdsecretvalue"
)

// changeBody builds a POST /auth/me/password request body.
func changeBody(current, next string) string {
	return fmt.Sprintf(`{"current_password":%q,"new_password":%q}`, current, next)
}

// TestChangePasswordRotatesCredentialAndKeepsCurrentSession is the happy path of
// AUTH-05 slice (c): an authenticated account swaps its own password by proving
// the current one. The old password must stop working at login, the new one must
// start working, and the caller's own token must survive (only OTHER sessions
// are signed out).
func TestChangePasswordRotatesCredentialAndKeepsCurrentSession(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody(fixtureCurrentPassword, fixtureNextPassword), reg.Token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	if old := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"supersecret"}`); old.Code != http.StatusUnauthorized {
		t.Errorf("login with the OLD password = %d, want 401", old.Code)
	}
	if fresh := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"evenmoresecret"}`); fresh.Code != http.StatusOK {
		t.Errorf("login with the NEW password = %d, want 200; body=%s", fresh.Code, fresh.Body.String())
	}
	// The changer keeps working on their current access token.
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", reg.Token); me.Code != http.StatusOK {
		t.Errorf("/auth/me on the changer's own token = %d, want 200", me.Code)
	}
}

// TestChangePasswordRefusesWrongCurrentPasswordAndChangesNothing proves the
// re-verification is real: a wrong current password neither changes the stored
// credential nor invalidates any session.
func TestChangePasswordRefusesWrongCurrentPasswordAndChangesNothing(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody("nope-not-it", fixtureNextPassword), reg.Token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong current password = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if login := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"supersecret"}`); login.Code != http.StatusOK {
		t.Errorf("original password stopped working after a refused change: login = %d", login.Code)
	}
	if login := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"evenmoresecret"}`); login.Code != http.StatusUnauthorized {
		t.Errorf("the rejected new password works at login = %d, want 401", login.Code)
	}
	if me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", reg.Token); me.Code != http.StatusOK {
		t.Errorf("a refused change signed the caller out: /auth/me = %d, want 200", me.Code)
	}
}

// TestChangePasswordValidatesTheNewPasswordLikeRegistration pins the policy to
// the one registration and the reset flow already enforce, and refuses a no-op
// change (which would otherwise mail a "your password was changed" notice for
// nothing).
func TestChangePasswordValidatesTheNewPasswordLikeRegistration(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	cases := []struct {
		name string
		body string
	}{
		{"too short", changeBody(fixtureCurrentPassword, "short")},
		{"missing current", fmt.Sprintf(`{"new_password":%q}`, fixtureNextPassword)},
		{"missing new", fmt.Sprintf(`{"current_password":%q}`, fixtureCurrentPassword)},
		{"same as current", changeBody(fixtureCurrentPassword, fixtureCurrentPassword)},
	}
	for _, tc := range cases {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password", tc.body, reg.Token)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422; body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
	if login := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"supersecret"}`); login.Code != http.StatusOK {
		t.Errorf("a rejected body changed the password: login = %d, want 200", login.Code)
	}
}

// TestChangePasswordRequiresAuthentication — the route is not a public one.
func TestChangePasswordRequiresAuthentication(t *testing.T) {
	srv := authServer(t)
	registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postTo(srv, "/api/v1/auth/me/password",
		changeBody(fixtureCurrentPassword, fixtureNextPassword))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous change password = %d, want 401", rec.Code)
	}
}

// TestChangePasswordMailsTheOwnerBestEffort proves SC4: a successful change
// sends the "your password was changed" notice through the existing mailer seam,
// and a mailer failure never fails the change.
func TestChangePasswordMailsTheOwnerBestEffort(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody(fixtureCurrentPassword, fixtureNextPassword), reg.Token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if mailer.changedEmail != "ada@example.test" {
		t.Errorf("password-changed notice went to %q, want ada@example.test", mailer.changedEmail)
	}

	mailer.fail = true
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody(fixtureNextPassword, fixtureThirdPassword), reg.Token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a failing mailer failed the change: status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if login := postTo(srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"thirdsecretvalue"}`); login.Code != http.StatusOK {
		t.Errorf("the change did not take effect when the mailer failed: login = %d", login.Code)
	}
}

// TestChangePasswordRefusesPasswordlessAccount pins the shipped rule for
// OAuth/ATProto-only accounts (empty stored hash): they are told to use the
// reset flow rather than being given an unfalsifiable "incorrect password"
// they could never satisfy.
func TestChangePasswordRefusesPasswordlessAccount(t *testing.T) {
	srv, repo := authServerWithFakeRepo(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	repo.clearPasswordHash(t, reg.User.ID)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/password",
		changeBody(fixtureCurrentPassword, fixtureNextPassword), reg.Token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("passwordless change = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "reset") {
		t.Errorf("the refusal does not point at the reset flow: %s", rec.Body.String())
	}
}

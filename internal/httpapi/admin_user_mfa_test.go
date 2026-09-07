package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// adminMFAEnv wires ONE fake user store behind both the auth service (which
// owns TOTP) and the admin service (which lists accounts), plus the capture
// mailer and the audit stream. Sharing the store is the point: the admin list's
// mfa_enabled and the reset route must read the same state /auth/mfa/totp
// writes, and a harness that kept two copies would let them drift.
type adminMFAEnv struct {
	srv     *Server
	repo    *authFakeRepo
	mfaRepo *mfaFakeRepo
	mailer  *captureResetMailer
	logs    *bytes.Buffer
}

func newAdminMFAEnv(t *testing.T) *adminMFAEnv {
	t.Helper()
	repo := newAuthFakeRepo()
	mfaRepo := newMFAFakeRepo()
	repo.mfaOf = func(id uuid.UUID) bool {
		row, ok := mfaRepo.rows[id]
		return ok && row.Enabled
	}
	mailer := &captureResetMailer{}
	logs := &bytes.Buffer{}
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour,
		auth.WithMFA(mfaRepo, nil, "Vidra Test"),
		auth.WithMailer(mailer))
	srv := New(testConfig(), nil, nil,
		WithAuthService(svc, 15*time.Minute),
		WithAdminService(admin.NewService(repo)),
		WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))
	return &adminMFAEnv{srv: srv, repo: repo, mfaRepo: mfaRepo, mailer: mailer, logs: logs}
}

// enableMFAFor drives enroll → verify for an already-registered account and
// returns the TOTP secret.
func (env *adminMFAEnv) enableMFAFor(t *testing.T, token string) string {
	t.Helper()
	rec := postJSONWithAuth(env.srv, "/api/v1/auth/mfa/totp", token, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll = %d; body=%s", rec.Code, rec.Body.String())
	}
	var enr totpEnrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &enr); err != nil {
		t.Fatalf("unmarshal enrollment: %v", err)
	}
	code := totpNow(t, enr.Secret)
	if rec := postJSONWithAuth(env.srv, "/api/v1/auth/mfa/totp/verify", token,
		fmt.Sprintf(`{"code":%q}`, code)); rec.Code != http.StatusOK {
		t.Fatalf("verify = %d; body=%s", rec.Code, rec.Body.String())
	}
	return enr.Secret
}

// mfaEnabledInList reads the admin list's own answer for one username, so every
// assertion about "the console can see it" goes through the shipped projection
// rather than the fake.
func (env *adminMFAEnv) mfaEnabledInList(t *testing.T, adminToken, username string) bool {
	t.Helper()
	for _, u := range adminUsers(t, env.srv, "", adminToken).Users {
		if u.Username == username {
			return u.MFAEnabled
		}
	}
	t.Fatalf("no %s in the admin user list", username)
	return false
}

// TestAdminRemovesUserSecondFactor is Ruling 2's whole point: the operator
// answer to "I lost my phone AND my recovery codes", which self-service cannot
// reach by construction — removing TOTP needs the account's own password and a
// session, and that account can no longer sign in.
func TestAdminRemovesUserSecondFactor(t *testing.T) {
	env := newAdminMFAEnv(t)
	adminTok := registerTokens(t, env.srv, `{"username":"mona","email":"mona@example.test","password":"supersecret"}`).Token
	userReg := registerTokens(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	env.enableMFAFor(t, userReg.Token)

	// The console can SEE it before it acts.
	if !env.mfaEnabledInList(t, adminTok, "ada") {
		t.Fatal("admin list says ada has no second factor, but she just enabled one")
	}
	adaID := userIDByName(t, adminUsers(t, env.srv, "", adminTok).Users, "ada")

	rec := deleteJSONWithAuth(env.srv, "/api/v1/admin/users/"+adaID+"/mfa", adminTok, `{"password":"supersecret"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin mfa reset = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// 204 with an empty body: nothing secret can be in a response that has no
	// body at all. The secret and the recovery codes are DELETED, never handed
	// to the admin.
	if body := strings.TrimSpace(rec.Body.String()); body != "" {
		t.Errorf("reset response body = %q, want empty", body)
	}
	if env.mfaEnabledInList(t, adminTok, "ada") {
		t.Error("ada still reads as MFA-enabled after the reset")
	}
	adaUUID := uuid.MustParse(adaID)
	if _, ok := env.mfaRepo.rows[adaUUID]; ok {
		t.Error("the user_mfa row survived the reset")
	}
	if len(env.mfaRepo.codes[adaUUID]) != 0 {
		t.Error("recovery codes survived the reset")
	}

	// Ruling 3's path: the target's sessions are revoked, so the access token
	// she was holding stops working within one request (session-bound).
	if me := sendJSONAuth(env.srv, http.MethodGet, "/api/v1/auth/me", "", userReg.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("target's access token after an admin reset = %d, want 401", me.Code)
	}
	// She can sign in again with her password alone — the point of the action.
	if login := postTo(env.srv, "/api/v1/auth/login",
		`{"email":"ada@example.test","password":"supersecret"}`); login.Code != http.StatusOK {
		t.Errorf("login after the reset = %d, want 200; body=%s", login.Code, login.Body.String())
	}

	// She is told, and told that an ADMINISTRATOR did it.
	notices := env.mailer.twoFactorRemoved
	if len(notices) != 1 || notices[0].Email != "ada@example.test" || !notices[0].ByAdmin {
		t.Fatalf("two-factor-removed notices = %+v, want one admin-initiated notice to ada", notices)
	}

	// Audited with the structured envelope, naming the target, carrying no prose.
	events := auditEvents(t, env.logs)
	ev := findAudit(events, observability.ActionAdminMFAReset, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("no admin.user.mfa_reset success audit event")
	}
	if ev["resource_id"] != adaID {
		t.Errorf("audit resource_id = %v, want the target %s", ev["resource_id"], adaID)
	}
	raw, _ := json.Marshal(ev)
	if strings.Contains(string(raw), "supersecret") {
		t.Error("the audit event carries the password")
	}
}

// TestAdminMFAResetRefusesWrongActorsAndWrongPassword: the guard is the ADMIN's
// own password (the target cannot supply theirs — that is the situation), and
// the route is admin-only.
func TestAdminMFAResetRefusesWrongActorsAndWrongPassword(t *testing.T) {
	env := newAdminMFAEnv(t)
	adminTok := registerTokens(t, env.srv, `{"username":"mona","email":"mona@example.test","password":"supersecret"}`).Token
	userReg := registerTokens(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	env.enableMFAFor(t, userReg.Token)
	registerTokens(t, env.srv, `{"username":"mod","email":"mod@example.test","password":"supersecret"}`)

	list := adminUsers(t, env.srv, "", adminTok).Users
	adaID := userIDByName(t, list, "ada")
	modID := userIDByName(t, list, "mod")
	if rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+modID,
		`{"role":"moderator"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("promote mod = %d; body=%s", rec.Code, rec.Body.String())
	}
	modTok := loginToken(t, env.srv, "mod@example.test", "supersecret")

	path := "/api/v1/admin/users/" + adaID + "/mfa"
	body := `{"password":"supersecret"}`
	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"ordinary user", userReg.Token, http.StatusForbidden},
		{"moderator", modTok, http.StatusForbidden},
	} {
		if rec := deleteJSONWithAuth(env.srv, path, tc.token, body); rec.Code != tc.want {
			t.Errorf("%s reset = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	// A wrong ADMIN password is refused and changes nothing.
	if rec := deleteJSONWithAuth(env.srv, path, adminTok, `{"password":"not-the-password"}`); rec.Code != http.StatusForbidden {
		t.Errorf("wrong admin password = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !env.mfaEnabledInList(t, adminTok, "ada") {
		t.Error("a refused reset removed the second factor anyway")
	}
	// A target with nothing to remove is 404, not a silent success.
	if rec := deleteJSONWithAuth(env.srv, "/api/v1/admin/users/"+modID+"/mfa", adminTok, body); rec.Code != http.StatusNotFound {
		t.Errorf("reset on an account with no MFA = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := deleteJSONWithAuth(env.srv, "/api/v1/admin/users/"+uuid.New().String()+"/mfa", adminTok, body); rec.Code != http.StatusNotFound {
		t.Errorf("reset on an unknown user = %d, want 404", rec.Code)
	}
	// Every refusal left the notice unsent: no mail for something that did not
	// happen.
	if n := len(env.mailer.twoFactorRemoved); n != 0 {
		t.Errorf("%d two-factor-removed notices after refusals only, want 0", n)
	}
}

// TestAdminCanResetTheirOwnSecondFactor is the ruling's deliberate allowance:
// the owner may lock themselves out too, and there is nobody above them to ask.
// The acting session survives — signing the admin out of the console they are
// standing in would be gratuitous — and the action is audited like any other.
func TestAdminCanResetTheirOwnSecondFactor(t *testing.T) {
	env := newAdminMFAEnv(t)
	reg := registerTokens(t, env.srv, `{"username":"mona","email":"mona@example.test","password":"supersecret"}`)
	env.enableMFAFor(t, reg.Token)
	monaID := userIDByName(t, adminUsers(t, env.srv, "", reg.Token).Users, "mona")

	rec := deleteJSONWithAuth(env.srv, "/api/v1/admin/users/"+monaID+"/mfa", reg.Token, `{"password":"supersecret"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("self reset = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if me := sendJSONAuth(env.srv, http.MethodGet, "/api/v1/auth/me", "", reg.Token); me.Code != http.StatusOK {
		t.Errorf("acting admin's own token after a self reset = %d, want 200", me.Code)
	}
	if env.mfaEnabledInList(t, reg.Token, "mona") {
		t.Error("mona still reads as MFA-enabled after resetting her own")
	}
	// Self-reset is NOT an admin-initiated removal from the account's point of
	// view: she did it herself, and the message must not tell her somebody else
	// did.
	notices := env.mailer.twoFactorRemoved
	if len(notices) != 1 || notices[0].ByAdmin {
		t.Errorf("self-reset notices = %+v, want one non-admin-initiated notice", notices)
	}
	if findAudit(auditEvents(t, env.logs), observability.ActionAdminMFAReset, observability.ResultSuccess) == nil {
		t.Error("a self reset must still be audited")
	}
}

// TestSelfServiceTOTPRemovalRevokesOtherSessions is Ruling 3: turning the second
// factor off lowers the account's protection, so it does what a password change
// does — every OTHER session is signed out and the account is told. Before this,
// an attacker holding a planted session AND the password could strip 2FA and
// leave every session they had alive, unmentioned and unaffected.
func TestSelfServiceTOTPRemovalRevokesOtherSessions(t *testing.T) {
	env := newAdminMFAEnv(t)
	reg := registerTokens(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	secret := env.enableMFAFor(t, reg.Token)

	// A second signed-in device, through the real two-step login.
	rec := postTo(env.srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second login = %d; body=%s", rec.Code, rec.Body.String())
	}
	var challenge struct {
		MFAToken string `json:"mfa_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &challenge); err != nil || challenge.MFAToken == "" {
		t.Fatalf("second login did not return an mfa_token: %s", rec.Body.String())
	}
	rec = postTo(env.srv, "/api/v1/auth/mfa/challenge",
		fmt.Sprintf(`{"mfa_token":%q,"code":%q}`, challenge.MFAToken, challengeCode(t, secret)))
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge = %d; body=%s", rec.Code, rec.Body.String())
	}
	var second authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil || second.Token == "" {
		t.Fatalf("challenge returned no session: %s", rec.Body.String())
	}
	// Both devices are live before the removal — otherwise the assertion below
	// would pass for the wrong reason.
	for name, tok := range map[string]string{"first": reg.Token, "second": second.Token} {
		if me := sendJSONAuth(env.srv, http.MethodGet, "/api/v1/auth/me", "", tok); me.Code != http.StatusOK {
			t.Fatalf("%s device before the removal = %d, want 200", name, me.Code)
		}
	}

	if rec := deleteJSONWithAuth(env.srv, "/api/v1/auth/mfa/totp", reg.Token,
		`{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("disable totp = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// The session that made the change survives; the other is gone — access
	// token included, because access tokens are session-bound.
	if me := sendJSONAuth(env.srv, http.MethodGet, "/api/v1/auth/me", "", reg.Token); me.Code != http.StatusOK {
		t.Errorf("the removing session = %d, want 200", me.Code)
	}
	if me := sendJSONAuth(env.srv, http.MethodGet, "/api/v1/auth/me", "", second.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("the other session = %d, want 401", me.Code)
	}
	notices := env.mailer.twoFactorRemoved
	if len(notices) != 1 || notices[0].Email != "ada@example.test" || notices[0].ByAdmin {
		t.Fatalf("notices = %+v, want one self-initiated notice to ada", notices)
	}
}

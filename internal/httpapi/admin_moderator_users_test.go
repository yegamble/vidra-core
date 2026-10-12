package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
)

// modFixture: the owner (mona), an admin (ada), two moderators (mo, mo2), two
// ordinary users (uma, vic), a capturing mailer, and a durable audit ledger.
type modFixture struct {
	env                      *accountEnv
	mailer                   *captureResetMailer
	owner, ada, mo, mo2, uma string // tokens (uma = ordinary user)
	umaID, vicID, moID       string
	mo2ID, adaID             string
}

func newModFixture(t *testing.T) *modFixture {
	t.Helper()
	mailer := &captureResetMailer{}
	env := newAccountEnv(t, WithAuditLog(audit.NewService(&httpAuditFakeRepo{})))
	// Rebuild the auth service with the capturing mailer, over the same repo.
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	env.srv.authsvc = auth.NewService(env.authRepo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	env.srv.logger = slog.New(slog.NewJSONHandler(env.logs, nil))
	f := &modFixture{env: env, mailer: mailer}
	f.owner = claimOwnerIn(t, env, "mona")
	for _, n := range []string{"ada", "mod", "mod2", "uma", "vic"} {
		registerAndToken(t, env.srv, `{"username":"`+n+`","email":"`+n+`@example.test","password":"supersecret"}`)
	}
	users := adminUsers(t, env.srv, "", f.owner).Users
	id := func(n string) string { return userIDByName(t, users, n) }
	f.umaID, f.vicID, f.moID, f.mo2ID, f.adaID = id("uma"), id("vic"), id("mod"), id("mod2"), id("ada")
	for uid, role := range map[string]string{f.adaID: "admin", f.moID: "moderator", f.mo2ID: "moderator"} {
		if rec := patchUser(env, f.owner, uid, `{"role":"`+role+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("promote %s = %d; body=%s", role, rec.Code, rec.Body.String())
		}
	}
	f.ada = loginToken(t, env.srv, "ada@example.test", "supersecret")
	f.mo = loginToken(t, env.srv, "mod@example.test", "supersecret")
	f.mo2 = loginToken(t, env.srv, "mod2@example.test", "supersecret")
	f.uma = loginToken(t, env.srv, "uma@example.test", "supersecret")
	return f
}

func patchUser(env *accountEnv, tok, id, body string) *httptest.ResponseRecorder {
	return sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+id, body, tok)
}

func TestModeratorUserActionMatrix(t *testing.T) {
	f := newModFixture(t)
	ownerID := userIDByName(t, adminUsers(t, f.env.srv, "", f.owner).Users, "mona")
	ban := `{"is_active":false,"ban_reason":"spam"}`
	cases := []struct {
		name, tok, target, body string
		want                    int
	}{
		{"user cannot ban", f.uma, f.vicID, ban, http.StatusForbidden},
		{"moderator bans user", f.mo, f.umaID, ban, http.StatusOK},
		{"moderator cannot ban moderator", f.mo, f.mo2ID, ban, http.StatusForbidden},
		{"moderator cannot ban admin", f.mo, f.adaID, ban, http.StatusForbidden},
		{"moderator cannot ban owner", f.mo, ownerID, ban, http.StatusForbidden},
		{"moderator cannot ban self", f.mo, f.moID, ban, http.StatusForbidden},
		{"moderator cannot change role", f.mo, f.vicID, `{"role":"moderator"}`, http.StatusForbidden},
		{"moderator cannot verify email", f.mo, f.vicID, `{"email_verified":true}`, http.StatusForbidden},
		{"moderator cannot set quota", f.mo, f.vicID, `{"storage_quota_bytes":5}`, http.StatusForbidden},
		{"moderator cannot mix ban with role", f.mo, f.vicID, `{"is_active":false,"ban_reason":"x","role":"admin"}`, http.StatusForbidden},
		{"moderator ban needs a reason", f.mo, f.vicID, `{"is_active":false}`, http.StatusUnprocessableEntity},
		{"moderator reason too long", f.mo, f.vicID, `{"is_active":false,"ban_reason":"` + strings.Repeat("x", 501) + `"}`, http.StatusUnprocessableEntity},
		{"admin bans moderator", f.ada, f.mo2ID, ban, http.StatusOK},
		{"admin changes role", f.ada, f.vicID, `{"role":"moderator"}`, http.StatusOK},
	}
	for _, tc := range cases {
		if rec := patchUser(f.env, tc.tok, tc.target, tc.body); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestModeratorAdminOnlyRoutesStayAdminOnly(t *testing.T) {
	f := newModFixture(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/admin/users", `{"username":"zed","email":"z@example.test","role":"user","password":"supersecret"}`},
		{http.MethodDelete, "/api/v1/admin/users/" + f.vicID, ""},
		{http.MethodDelete, "/api/v1/admin/users/" + f.vicID + "/mfa", `{"password":"supersecret"}`},
		{http.MethodPost, "/api/v1/admin/users/" + f.vicID + "/password-reset-link", `{"password":"supersecret"}`},
		{http.MethodGet, "/api/v1/admin/stats", ""},
	} {
		if rec := sendJSONAuth(f.env.srv, tc.method, tc.path, tc.body, f.mo); rec.Code != http.StatusForbidden {
			t.Errorf("moderator %s %s = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	if rec := sendJSONAuth(f.env.srv, http.MethodGet, "/api/v1/admin/users", "", f.mo); rec.Code != http.StatusOK {
		t.Errorf("moderator list = %d, want 200", rec.Code)
	}
	if rec := sendJSONAuth(f.env.srv, http.MethodGet, "/api/v1/admin/users", "", f.uma); rec.Code != http.StatusForbidden {
		t.Errorf("user list = %d, want 403", rec.Code)
	}
}

func TestBanRecordsReasonMailsTheUserAndAuditsLengthOnly(t *testing.T) {
	f := newModFixture(t)
	const reason = "posting spam links in comments"
	rec := patchUser(f.env, f.mo, f.umaID, `{"is_active":false,"ban_reason":"`+reason+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ban = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v adminUserView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.IsActive || v.BanReason == nil || *v.BanReason != reason || v.BannedAt == nil || v.BannedBy == nil || *v.BannedBy != f.moID {
		t.Errorf("ban view = %+v", v)
	}
	// The list shows it too.
	for _, u := range adminUsers(t, f.env.srv, "", f.mo).Users {
		if u.ID == f.umaID && (u.BanReason == nil || *u.BanReason != reason) {
			t.Errorf("listed ban_reason = %v", u.BanReason)
		}
	}
	if len(f.mailer.suspensions) != 1 || f.mailer.suspensions[0].Email != "uma@example.test" || f.mailer.suspensions[0].Reason != reason {
		t.Errorf("suspension notices = %+v", f.mailer.suspensions)
	}
	// The ledger holds the length, never the text or the address.
	found := false
	for _, e := range auditEvents(t, f.env.logs) {
		if r, _ := e["reason"].(string); e["action"] == observability.ActionAdminUserUpdate && strings.Contains(r, "ban_reason_len=30") {
			found = true
		}
	}
	if !found {
		t.Fatal("no admin.user.update audit event carries ban_reason_len=30")
	}
	for _, line := range strings.Split(f.env.logs.String(), "\n") {
		if strings.Contains(line, reason) || strings.Contains(line, "uma@example.test") {
			t.Errorf("ban text or address leaked into a log line: %s", line)
		}
	}

	// Unban clears the explanation and mails a reinstatement.
	if rec := patchUser(f.env, f.mo, f.umaID, `{"is_active":true}`); rec.Code != http.StatusOK {
		t.Fatalf("unban = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, u := range adminUsers(t, f.env.srv, "", f.mo).Users {
		if u.ID == f.umaID && (u.BanReason != nil || u.BannedAt != nil || u.BannedBy != nil) {
			t.Errorf("unban left %+v", u)
		}
	}
	if len(f.mailer.reinstated) != 1 || f.mailer.reinstated[0] != "uma@example.test" {
		t.Errorf("reinstatement notices = %v", f.mailer.reinstated)
	}
}

func TestBanNoticeFailureDoesNotFailTheBan(t *testing.T) {
	f := newModFixture(t)
	f.mailer.fail = true
	if rec := patchUser(f.env, f.mo, f.umaID, `{"is_active":false,"ban_reason":"spam"}`); rec.Code != http.StatusOK {
		t.Errorf("ban with a dead relay = %d, want 200", rec.Code)
	}
}

func TestModeratorMayWorkTheRegistrationQueue(t *testing.T) {
	f := newModFixture(t)
	f.env.setSetting(t, instancesettings.KeyRegistrationRequireApproval, "true")
	for _, n := range []string{"bob", "cleo"} {
		if rec := postTo(f.env.srv, "/api/v1/auth/register", `{"username":"`+n+`","email":"`+n+`@example.test","password":"supersecret"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("apply %s = %d; body=%s", n, rec.Code, rec.Body.String())
		}
	}
	rec := getWithAuth(f.env.srv, "/api/v1/admin/registration-requests?status=pending", f.mo)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderator list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list registrationRequestListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	ids := map[string]string{}
	for _, r := range list.Requests {
		ids[r.Username] = r.ID
	}
	if rec := sendJSONAuth(f.env.srv, http.MethodPost, "/api/v1/admin/registration-requests/"+ids["bob"]+"/approve", "", f.mo); rec.Code != http.StatusNoContent {
		t.Errorf("moderator approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(f.env.srv, http.MethodPost, "/api/v1/admin/registration-requests/"+ids["cleo"]+"/reject", `{"note":"no"}`, f.mo); rec.Code != http.StatusNoContent {
		t.Errorf("moderator reject = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(f.env.srv, "/api/v1/admin/registration-requests", f.uma); rec.Code != http.StatusForbidden {
		t.Errorf("user list of requests = %d, want 403", rec.Code)
	}
}

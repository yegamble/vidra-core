package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// failingAuditRepo is the durable ledger with a switch for "the database
// refused the audit row", which is the fail-closed case.
type failingAuditRepo struct {
	httpAuditFakeRepo
	fail bool
}

func (f *failingAuditRepo) InsertAuditLog(ctx context.Context, a sqlcgen.InsertAuditLogParams) error {
	if f.fail {
		return errors.New("audit store down")
	}
	return f.httpAuditFakeRepo.InsertAuditLog(ctx, a)
}

// A second mint withdraws the first: only the newest link works.
func TestAdminPasswordResetLinkSecondMintInvalidatesFirst(t *testing.T) {
	env, ownerTok, userID := resetLinkFixture(t, &httpAuditFakeRepo{})
	var tokens [2]string
	for i := range tokens {
		rec := mintResetLink(env, userID, pw, ownerTok)
		if rec.Code != http.StatusOK {
			t.Fatalf("mint %d = %d; body=%s", i, rec.Code, rec.Body.String())
		}
		tokens[i] = tokenOf(t, decodeResetLink(t, rec).ResetURL)
	}
	redeem := func(token string) int {
		return postTo(env.srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":"brand-new-pass"}`).Code
	}
	if redeem(tokens[0]) == http.StatusNoContent {
		t.Error("the older link still redeems after a newer one was minted")
	}
	if got := redeem(tokens[1]); got != http.StatusNoContent {
		t.Errorf("the newest link = %d, want 204", got)
	}
}

// A credential that leaves no trace is worse than one never issued, so a failed
// (or impossible) audit write withholds the link AND withdraws the token, so it
// cannot be redeemed unseen.
func TestAdminPasswordResetLinkFailsClosedWithoutAudit(t *testing.T) {
	failing := &failingAuditRepo{fail: true}
	failEnv, failTok, failID := resetLinkFixture(t, failing)
	bareEnv := newAccountEnv(t) // no durable audit log wired at all
	bareEnv.srv.cfg.PublicBaseURL = "https://vidra.example"
	bareTok := claimOwnerIn(t, bareEnv, "mona")
	registerAndToken(t, bareEnv.srv, `{"username":"uma","email":"uma@example.test","password":"supersecret"}`)
	bareID := userIDByName(t, adminUsers(t, bareEnv.srv, "", bareTok).Users, "uma")

	for _, tc := range []struct {
		name  string
		env   *accountEnv
		id    string
		token string
	}{
		{"audit store failing", failEnv, failID, failTok},
		{"no audit log", bareEnv, bareID, bareTok},
	} {
		rec := mintResetLink(tc.env, tc.id, pw, tc.token)
		body := rec.Body.String()
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503; body=%s", tc.name, rec.Code, body)
		}
		if strings.Contains(body, "reset_url") || strings.Contains(body, "token=") {
			t.Errorf("%s: link returned: %s", tc.name, body)
		}
		if n := len(tc.env.authRepo.resets); n != 0 {
			t.Errorf("%s: %d unused reset tokens remain, want 0", tc.name, n)
		}
	}
}

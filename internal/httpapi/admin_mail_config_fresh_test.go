package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// withCurrentPassword adds the admin's fresh credential to a mail-config body,
// which every PUT now carries; the rest of the suite is about the document.
func withCurrentPassword(body string) string {
	return `{"current_password":"supersecret",` + strings.TrimPrefix(body, "{")
}

const mailPutBody = `{"transport":"resend","from_address":"a@b.test","resend":{"api_key":"k"}}`

// Redirecting a live instance's outbound mail (password resets, verification
// links) is the highest-value change a hijacked admin session could make, so
// the PUT wants proof of presence from ANY admin, not just the owner.
func TestMailConfigPutNeedsAFreshCredential(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{}
	srv, repo := mailConfigServer(t, &buf, svc)
	registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	setRole(t, repo, "bob@example.test", "admin")

	rec := doJSON(srv, http.MethodPut, mailConfigPath, bobTok, mailPutBody)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "step_up_required" {
		t.Fatalf("PUT with no proof = %d %q, want 403 step_up_required; body=%s", rec.Code, errorCode(t, rec), rec.Body.String())
	}
	rec = doJSON(srv, http.MethodPut, mailConfigPath, bobTok, `{"current_password":"wrong-password",`+strings.TrimPrefix(mailPutBody, "{"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT with a wrong password = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.saved) != 0 {
		t.Fatalf("a refused PUT reached the service: %d saves", len(svc.saved))
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionAdminMailConfigUpdate, observability.ResultFailure); ev == nil {
		t.Error("the refusals were not audited")
	}

	if rec := doJSON(srv, http.MethodPut, mailConfigPath, bobTok, withCurrentPassword(mailPutBody)); rec.Code != http.StatusOK {
		t.Fatalf("PUT with the password = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.saved) != 1 {
		t.Errorf("saves = %d, want 1", len(svc.saved))
	}
	// Neither proof is ever written to the log.
	if strings.Contains(buf.String(), "supersecret") || strings.Contains(buf.String(), "wrong-password") {
		t.Error("a password reached the log")
	}

	// Reverting to the environment is the lockout escape hatch: no proof asked.
	if rec := doJSON(srv, http.MethodDelete, mailConfigPath, bobTok, ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE without a credential = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// A passwordless admin (ATProto/OIDC sign-in) proves presence with the existing
// step-up assertion, once; an account that HAS a password cannot swap it in.
func TestMailConfigPutAcceptsAStepUpFromAPasswordlessAdmin(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{}
	srv, repo := mailConfigServer(t, &buf, svc)
	registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerTokens(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	setRole(t, repo, "bob@example.test", "admin")

	claims, err := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute).Parse(bob.Token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	userID, sessionID := uuid.MustParse(bob.User.ID), uuid.MustParse(claims.SessionID)
	grant := func() string {
		g, err := srv.authsvc.IssueStepUp(context.Background(), userID, sessionID, "atproto")
		if err != nil {
			t.Fatalf("issue step-up: %v", err)
		}
		return g.Token
	}
	body := func(tok string) string {
		return `{"step_up_token":"` + tok + `",` + strings.TrimPrefix(mailPutBody, "{")
	}

	// bob has a password, so an assertion does not stand in for it.
	rec := doJSON(srv, http.MethodPut, mailConfigPath, bob.Token, body(grant()))
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "password_already_set" {
		t.Fatalf("assertion from a password account = %d %q, want 422 password_already_set", rec.Code, errorCode(t, rec))
	}

	u := repo.users["bob@example.test"]
	u.PasswordHash = ""
	repo.users["bob@example.test"] = u

	tok := grant()
	if rec := doJSON(srv, http.MethodPut, mailConfigPath, bob.Token, body(tok)); rec.Code != http.StatusOK {
		t.Fatalf("PUT with a step-up = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Single use.
	rec = doJSON(srv, http.MethodPut, mailConfigPath, bob.Token, body(tok))
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "step_up_required" {
		t.Errorf("replayed assertion = %d %q, want 403 step_up_required", rec.Code, errorCode(t, rec))
	}
	// Both proofs at once is ambiguous, not additive.
	rec = doJSON(srv, http.MethodPut, mailConfigPath, bob.Token, `{"current_password":"x","step_up_token":"y",`+strings.TrimPrefix(mailPutBody, "{"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("both proofs = %d, want 422", rec.Code)
	}
}

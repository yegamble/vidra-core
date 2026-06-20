package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// auditServer builds an auth-enabled server whose logs are captured into buf so
// tests can assert the audit events emitted by the handlers.
func auditServer(t *testing.T, buf *bytes.Buffer) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute), WithLogger(logger))
}

// auditEvents returns every captured log line that is an audit event.
func auditEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if rec["audit"] == true {
			events = append(events, rec)
		}
	}
	return events
}

// findAudit returns the first audit event matching action+result, or nil.
func findAudit(events []map[string]any, action, result string) map[string]any {
	for _, e := range events {
		if e["action"] == action && e["result"] == result {
			return e
		}
	}
	return nil
}

func TestLoginEmitsAuditEvents(t *testing.T) {
	var buf bytes.Buffer
	srv := auditServer(t, &buf)

	postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"wrong"}`)

	events := auditEvents(t, &buf)

	if reg := findAudit(events, observability.ActionRegister, observability.ResultSuccess); reg == nil {
		t.Error("expected a register success audit event")
	} else if reg["actor_id"] == nil || reg["actor_id"] == "" {
		t.Error("register audit should carry an actor_id")
	}

	if ok := findAudit(events, observability.ActionLogin, observability.ResultSuccess); ok == nil {
		t.Error("expected a login success audit event")
	} else if ok["actor_id"] == nil || ok["actor_id"] == "" {
		t.Error("login success audit should carry an actor_id")
	}

	fail := findAudit(events, observability.ActionLogin, observability.ResultFailure)
	if fail == nil {
		t.Fatal("expected a login failure audit event")
	}
	// Enumeration-safe: a failed login must not record an actor_id (or the email).
	if _, ok := fail["actor_id"]; ok {
		t.Error("login failure audit must not carry an actor_id")
	}
	if fail["reason"] != "invalid_credentials" {
		t.Errorf("login failure reason = %v, want invalid_credentials", fail["reason"])
	}

	// No audit event may carry a denylisted key, and the password must never
	// appear anywhere in the captured logs.
	for _, e := range events {
		for k := range e {
			if observability.IsSensitiveKey(k) {
				t.Errorf("audit event contains denylisted key %q", k)
			}
		}
	}
	if bytes.Contains(buf.Bytes(), []byte("supersecret")) {
		t.Error("the password must never appear in logs")
	}
}

func TestLogoutAndResetEmitAuditEvents(t *testing.T) {
	var buf bytes.Buffer
	srv := auditServer(t, &buf)

	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	postTo(srv, "/api/v1/auth/logout", `{"refresh_token":"`+reg.RefreshToken+`"}`)
	postTo(srv, "/api/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"bad-token","password":"brand-new-pass"}`)

	events := auditEvents(t, &buf)
	if findAudit(events, observability.ActionLogout, observability.ResultSuccess) == nil {
		t.Error("expected a logout success audit event")
	}
	if findAudit(events, observability.ActionPasswordResetRequest, observability.ResultSuccess) == nil {
		t.Error("expected a password-reset request audit event")
	}
	if findAudit(events, observability.ActionPasswordResetComplete, observability.ResultFailure) == nil {
		t.Error("expected a password-reset complete failure audit event for a bad token")
	}
}

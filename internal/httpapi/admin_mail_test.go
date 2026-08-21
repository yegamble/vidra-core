package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

// refusingMailer is a mailer whose relay says no. Its error quotes the
// recipient address back, exactly as a real 550 does — that is the point of the
// fixture, since the response must not repeat it.
type refusingMailer struct{ auth.Mailer }

func (refusingMailer) SendTest(_ context.Context, to string) error {
	return errors.New("mail: RCPT TO: 550 5.1.1 <" + to + ">: Recipient address rejected")
}

// silentMailer has an outbound path but no test capability — an auth.Mailer
// implementation that predates SendTest. It is what makes the "assert the
// narrow interface rather than widening auth.Mailer" decision testable: such a
// mailer must report 503, not pretend the test succeeded.
type silentMailer struct{ auth.Mailer }

// mailTestServer builds an admin server with the given mailer options.
func mailTestServer(t *testing.T, cfg *config.Config, opts ...Option) (*Server, *bytes.Buffer) {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	all := append([]Option{WithAuthService(svc, 15*time.Minute)}, opts...)
	srv := New(cfg, nil, nil, all...)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	return srv, &buf
}

// mailTest posts the probe as the instance's first (admin) account.
func mailTest(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	return sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test", "", admin)
}

// The full outcome matrix. Each answer has to be distinguishable, because each
// one has a different fix: no mail path at all, no address to send to, a relay
// that refused, and success.
func TestMailTestOutcomes(t *testing.T) {
	contactCfg := func() *config.Config {
		cfg := testConfig()
		cfg.InstanceContactEmail = "ops@example.test"
		return cfg
	}

	t.Run("no outbound mail path is 503", func(t *testing.T) {
		srv, _ := mailTestServer(t, contactCfg())
		rec := mailTest(t, srv)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("mail test with no mailer = %d, want 503; body=%s", rec.Code, rec.Body.String())
		}
		var envelope ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal 503: %v", err)
		}
		if envelope.Error.Code != "mail_not_configured" {
			t.Errorf("503 code = %q, want mail_not_configured", envelope.Error.Code)
		}
		// 503 is a 5xx, and the central handler scrubs any 5xx without a stable
		// code down to "an unexpected error occurred". The guidance has to
		// survive that, or anyone reading the API through curl or a script gets
		// nothing to act on.
		if !strings.Contains(envelope.Error.Message, "MAIL_ENABLED") {
			t.Errorf("503 message = %q; the operator guidance was scrubbed", envelope.Error.Message)
		}
	})

	t.Run("a mailer that cannot test is 503", func(t *testing.T) {
		// Not a hypothetical: this is what every auth.Mailer implementation
		// looks like until it opts in. It must report honestly rather than
		// silently succeeding.
		srv, _ := mailTestServer(t, contactCfg(), WithContactMailer(silentMailer{}))
		if rec := mailTest(t, srv); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("mail test with a non-testable mailer = %d, want 503", rec.Code)
		}
	})

	t.Run("no contact address is 409", func(t *testing.T) {
		srv, _ := mailTestServer(t, testConfig(), WithContactMailer(auth.NewCaptureMailer()))
		rec := mailTest(t, srv)
		if rec.Code != http.StatusConflict {
			t.Fatalf("mail test with no contact email = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		// The copy has to name the thing to change, or an admin is stuck.
		if !strings.Contains(rec.Body.String(), "contact_email") {
			t.Errorf("409 body does not name the setting to fix: %s", rec.Body.String())
		}
	})

	t.Run("a refused relay is 502 with generic text", func(t *testing.T) {
		srv, buf := mailTestServer(t, contactCfg(), WithContactMailer(refusingMailer{}))
		rec := mailTest(t, srv)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("mail test against a refusing relay = %d, want 502; body=%s", rec.Code, rec.Body.String())
		}
		var envelope ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("unmarshal 502: %v", err)
		}
		if envelope.Error.Code != "mail_test_failed" {
			t.Errorf("502 code = %q, want mail_test_failed", envelope.Error.Code)
		}
		// The relay quoted the recipient back. That is operator PII and must
		// not survive into a response body a browser caches.
		if strings.Contains(rec.Body.String(), "ops@example.test") {
			t.Errorf("the 502 body repeats the recipient address: %s", rec.Body.String())
		}
		// Nor into the audit trail: the reason is the outcome, nothing else.
		for _, line := range auditLines(buf.String()) {
			if strings.Contains(line, "ops@example.test") {
				t.Errorf("an audit event carries the recipient address: %s", line)
			}
		}
		if !strings.Contains(buf.String(), "send_failed") {
			t.Errorf("no send_failed audit reason in: %s", buf.String())
		}
	})

	t.Run("a delivered probe is 202", func(t *testing.T) {
		capture := auth.NewCaptureMailer()
		srv, buf := mailTestServer(t, contactCfg(), WithContactMailer(capture))
		rec := mailTest(t, srv)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("mail test = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		var body mailTestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal 202: %v", err)
		}
		if body.Status != "sent" {
			t.Errorf("status = %q, want sent", body.Status)
		}
		// The recipient is the instance's own contact address and nothing else:
		// the caller never chose it, and could not have.
		sent := capture.TestMessages()
		if len(sent) != 1 || sent[0].To != "ops@example.test" {
			t.Fatalf("captured test messages = %+v, want exactly one to the contact address", sent)
		}
		// The address never reaches the audit trail, success or failure.
		for _, line := range auditLines(buf.String()) {
			if strings.Contains(line, "ops@example.test") {
				t.Errorf("an audit event carries the recipient address: %s", line)
			}
		}
		if !strings.Contains(buf.String(), string(observability.ActionAdminMailTest)) {
			t.Errorf("no %s audit event in: %s", observability.ActionAdminMailTest, buf.String())
		}
	})
}

// The endpoint takes no recipient, and there is no body shape that could
// smuggle one in: that is what stops an admin session from becoming an open
// relay for phishing from the instance's own domain.
func TestMailTestCannotChooseTheRecipient(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	capture := auth.NewCaptureMailer()
	srv, _ := mailTestServer(t, cfg, WithContactMailer(capture))

	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test",
		`{"to":"victim@elsewhere.test","email":"victim@elsewhere.test"}`, admin)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("mail test with a body = %d, want it ignored and 202; body=%s", rec.Code, rec.Body.String())
	}
	sent := capture.TestMessages()
	if len(sent) != 1 || sent[0].To != "ops@example.test" {
		t.Fatalf("captured = %+v; a caller-supplied address must be ignored entirely", sent)
	}
}

// Non-admins and anonymous callers cannot make the instance send anything.
func TestMailTestGuards(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	capture := auth.NewCaptureMailer()
	srv, _ := mailTestServer(t, cfg, WithContactMailer(capture))

	_ = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon mail test = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test", "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin mail test = %d, want 403", rec.Code)
	}
	if sent := capture.TestMessages(); len(sent) != 0 {
		t.Errorf("a rejected caller still caused %d sends", len(sent))
	}
}

// The dedicated budget is keyed on the ADMIN, not the address: two admins
// behind one office IP must not spend each other's allowance, and a stuck
// browser tab must not be able to hammer the relay until the domain is
// throttled.
func TestMailTestRateLimitedPerAdmin(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	capture := auth.NewCaptureMailer()
	srv, _ := mailTestServer(t, cfg,
		WithContactMailer(capture),
		WithMailTestRateLimiter(ratelimit.NewLimiter(ratelimit.NewMemoryCounter(), 2, time.Hour)),
	)

	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	for i := 1; i <= 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test", "", admin); rec.Code != http.StatusAccepted {
			t.Fatalf("send %d = %d, want 202", i, rec.Code)
		}
	}
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/mail/test", "", admin)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("send 3 = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 carries no Retry-After, so a client cannot know when to try again")
	}
	if sent := capture.TestMessages(); len(sent) != 2 {
		t.Errorf("captured %d sends, want the budget's 2", len(sent))
	}
}

// auditLines returns the audit events from a captured JSON log.
func auditLines(logged string) []string {
	var out []string
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, `"audit":true`) {
			out = append(out, line)
		}
	}
	return out
}

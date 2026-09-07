package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// resendBody builds a POST /auth/verify-email/resend request body.
func resendBody(email string) string {
	return fmt.Sprintf(`{"email":%q}`, email)
}

// TestAnonymousVerificationResendDeliversAWorkingToken is A05 defect 2: with
// the verification gate on, registration returns 202 and no session and login
// answers 403 email_verification_required, so the account that lost the message
// is exactly the one that cannot authenticate to ask for another. The only
// resend that shipped sat behind requireAuth.
func TestAnonymousVerificationResendDeliversAWorkingToken(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	mailer.token = ""

	// No Authorization header anywhere in this flow.
	rec := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("ada@example.test"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resend = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if mailer.token == "" {
		t.Fatal("no verification message was sent to a known unverified address")
	}
	// The token in the message actually verifies the account.
	confirm := postTo(srv, "/api/v1/auth/verify-email/confirm", fmt.Sprintf(`{"token":%q}`, mailer.token))
	if confirm.Code != http.StatusNoContent {
		t.Fatalf("confirm with the resent token = %d, want 204; body=%s", confirm.Code, confirm.Body.String())
	}
	// And now that the address is verified, a further resend sends nothing —
	// while still answering the same 202.
	mailer.token = ""
	if rec := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("ada@example.test")); rec.Code != http.StatusAccepted {
		t.Errorf("resend for a verified address = %d, want 202", rec.Code)
	}
	if mailer.token != "" {
		t.Error("a verified address was sent another verification message")
	}
}

// TestVerificationResendIsEnumerationSafe: the answer must be identical — the
// status AND the bytes — for a known address, an unknown one, and an
// already-verified one. A route that takes an address and answers differently
// is an account-existence oracle, and this one is unauthenticated.
func TestVerificationResendIsEnumerationSafe(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	known := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("ada@example.test"))
	unknown := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("nobody@example.test"))
	if known.Code != unknown.Code {
		t.Errorf("known = %d, unknown = %d; they must not differ", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Errorf("known body %q != unknown body %q", known.Body.String(), unknown.Body.String())
	}
	if known.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", known.Code)
	}

	// A relay that will not take the message must not change the answer either:
	// a 500 for a registered address beside a 202 for an unregistered one is a
	// flat oracle, available exactly when the instance is already unhealthy.
	mailer.fail = true
	mailer.token = ""
	brokenKnown := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("ada@example.test"))
	brokenUnknown := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("nobody@example.test"))
	if brokenKnown.Code != http.StatusAccepted || brokenUnknown.Code != http.StatusAccepted {
		t.Errorf("with the relay down: known = %d, unknown = %d; both must be 202",
			brokenKnown.Code, brokenUnknown.Code)
	}
	if brokenKnown.Body.String() != brokenUnknown.Body.String() {
		t.Errorf("with the relay down the bodies differ: %q vs %q",
			brokenKnown.Body.String(), brokenUnknown.Body.String())
	}
}

// TestVerificationResendCooldownThrottlesOneAddress: the per-IP auth limiter
// bounds one caller; this bounds what a distributed caller can do to a single
// inbox. It throttles the SEND, never the answer.
func TestVerificationResendCooldownThrottlesOneAddress(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	mailer.calls = 0

	for i := range 3 {
		if rec := postTo(srv, "/api/v1/auth/verify-email/resend", resendBody("ada@example.test")); rec.Code != http.StatusAccepted {
			t.Fatalf("resend %d = %d, want 202", i, rec.Code)
		}
	}
	if mailer.calls != 1 {
		t.Errorf("three rapid resends produced %d messages, want 1", mailer.calls)
	}
}

// TestVerificationResendRejectsAnEmptyAddress: validation runs before any
// lookup, so an empty body is 422 rather than a 202 that quietly did nothing.
func TestVerificationResendRejectsAnEmptyAddress(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	if rec := postTo(srv, "/api/v1/auth/verify-email/resend", `{"email":"  "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty address = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// captureMailer records the most recent password-reset delivery so tests can
// recover the raw token the service generated.
type captureMailer struct {
	calls int
	email string
	token string
	// changed records the addresses that got a "your password was changed"
	// notice; failChanged makes that send fail, so a test can prove it is
	// best-effort.
	changed     []string
	failChanged bool
	// ownershipNotices records both sides of an instance-ownership transfer.
	ownershipNotices []CapturedOwnershipNotice
	// twoFactorRemoved records the two-factor-removed notices, with the flag
	// saying whether an administrator did the removing.
	twoFactorRemoved []CapturedTwoFactorRemoval
}

func (m *captureMailer) SendTwoFactorRemoved(_ context.Context, email string, byAdmin bool) error {
	m.twoFactorRemoved = append(m.twoFactorRemoved, CapturedTwoFactorRemoval{Email: email, ByAdmin: byAdmin})
	return nil
}

func (m *captureMailer) SendPasswordChanged(_ context.Context, email string) error {
	if m.failChanged {
		return errors.New("mailer down")
	}
	m.changed = append(m.changed, email)
	return nil
}

func (m *captureMailer) SendPasswordReset(_ context.Context, email, token string) error {
	m.calls++
	m.email = email
	m.token = token
	return nil
}

func (m *captureMailer) SendEmailVerification(_ context.Context, email, token string) error {
	m.calls++
	m.email = email
	m.token = token
	return nil
}

// The email-change senders default to no-ops here: reset_test's captureMailer
// only has to SATISFY the interface. emailchange_test.go's changeMailer embeds
// this type and overrides both, and its overrides are what the email-change
// tests assert on.
func (m *captureMailer) SendEmailChangeVerification(context.Context, string, string) error {
	return nil
}

func (m *captureMailer) SendEmailChanged(context.Context, string, string) error { return nil }

func (m *captureMailer) SendNewReportAlert(context.Context, string, string, string, string) error {
	return nil
}

func (m *captureMailer) SendContactForm(context.Context, string, string, string, string, string) error {
	return nil
}

// The signup-decision notices default to no-ops here for the same reason as the
// email-change senders above: registration_test.go asserts on them through
// CaptureMailer, and this type only has to satisfy the interface.
func (m *captureMailer) SendRegistrationApproved(context.Context, string, string, string, bool) error {
	return nil
}

func (m *captureMailer) SendRegistrationRejected(context.Context, string, string, string) error {
	return nil
}

// SendOwnershipTransferred is captured so a test can assert both sides were
// mailed; the bodies belong to internal/mail, which has its own tests.
func (m *captureMailer) SendOwnershipTransferred(_ context.Context, email, recipientUsername, counterpartUsername, consoleURL string, isNewOwner bool) error {
	party := "former_owner"
	if isNewOwner {
		party = "new_owner"
	}
	m.ownershipNotices = append(m.ownershipNotices, CapturedOwnershipNotice{
		Party: party, Email: email, Username: recipientUsername,
		Counterpart: counterpartUsername, ConsoleURL: consoleURL,
	})
	return nil
}

func newResetService(repo Repository, mailer Mailer) *Service {
	return NewService(repo, newTestIssuer(), time.Hour, WithMailer(mailer))
}

func TestRequestPasswordResetDeliversTokenForKnownAccount(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	register(t, svc, "ada", "ada@example.test")

	if err := svc.RequestPasswordReset(context.Background(), "ada@example.test"); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if mailer.calls != 1 {
		t.Fatalf("mailer called %d times, want 1", mailer.calls)
	}
	if mailer.token == "" {
		t.Fatal("expected a non-empty reset token to be delivered")
	}
	if mailer.email != "ada@example.test" {
		t.Errorf("token delivered to %q, want ada@example.test", mailer.email)
	}
}

func TestRequestPasswordResetIsEnumerationSafe(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)

	// No account exists for this email: the call succeeds but delivers nothing,
	// so a caller cannot tell registered emails from unregistered ones.
	if err := svc.RequestPasswordReset(context.Background(), "nobody@example.test"); err != nil {
		t.Fatalf("RequestPasswordReset for unknown email should be a no-op, got %v", err)
	}
	if mailer.calls != 0 {
		t.Errorf("mailer called %d times for an unknown email, want 0", mailer.calls)
	}
}

func TestResetPasswordChangesPasswordAndConsumesToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	register(t, svc, "ada", "ada@example.test")

	if err := svc.RequestPasswordReset(context.Background(), "ada@example.test"); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	token := mailer.token

	if err := svc.ResetPassword(context.Background(), token, "brand-new-pass"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	u, err := repo.GetUserByEmail(context.Background(), "ada@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if err := CheckPassword(u.PasswordHash, "brand-new-pass"); err != nil {
		t.Errorf("the new password should validate: %v", err)
	}
	if err := CheckPassword(u.PasswordHash, "supersecret"); err == nil {
		t.Error("the old password should no longer validate")
	}

	// Single-use: replaying the same token fails and changes nothing.
	if err := svc.ResetPassword(context.Background(), token, "yet-another-pass"); err != ErrInvalidResetToken {
		t.Errorf("replayed token error = %v, want ErrInvalidResetToken", err)
	}
}

func TestResetPasswordRevokesAllSessions(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	_, tokens := register(t, svc, "ada", "ada@example.test")

	if err := svc.RequestPasswordReset(context.Background(), "ada@example.test"); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if err := svc.ResetPassword(context.Background(), mailer.token, "brand-new-pass"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// The refresh token minted at registration must be dead after a reset.
	if _, _, err := svc.Refresh(context.Background(), tokens.RefreshToken, "test-agent"); err != ErrInvalidRefresh {
		t.Errorf("refresh after reset error = %v, want ErrInvalidRefresh", err)
	}
}

func TestResetPasswordRejectsUnknownToken(t *testing.T) {
	svc := newResetService(newFakeRepo(), &captureMailer{})
	if err := svc.ResetPassword(context.Background(), "not-a-real-token", "brand-new-pass"); err != ErrInvalidResetToken {
		t.Errorf("unknown token error = %v, want ErrInvalidResetToken", err)
	}
}

func TestResetPasswordRejectsExpiredToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	register(t, svc, "ada", "ada@example.test")

	base := time.Now()
	svc.now = func() time.Time { return base }
	if err := svc.RequestPasswordReset(context.Background(), "ada@example.test"); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}

	// Advance the clock past the 1h reset TTL.
	svc.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := svc.ResetPassword(context.Background(), mailer.token, "brand-new-pass"); err != ErrInvalidResetToken {
		t.Errorf("expired token error = %v, want ErrInvalidResetToken", err)
	}
}

// failResetMailer fails only the password-reset send, so a test can separate a
// delivery failure from a storage failure.
type failResetMailer struct {
	captureMailer
	err error
}

func (m *failResetMailer) SendPasswordReset(_ context.Context, _, _ string) error {
	m.calls++
	return m.err
}

// TestRequestPasswordResetStaysEnumerationSafeWhenTheRelayIsDown is the A05
// acceptance regression. RequestPasswordReset returns nil for an address that
// matches nothing — that is what makes the endpoint's single 202 honest — but
// it used to return the mailer's error verbatim for an address that DOES match,
// so a broken relay turned the endpoint into an account-existence oracle: 500
// meant "registered here", 202 meant "not". Proven live on 2026-09-07 with the
// relay stopped (known 500 x3, unknown 202 x3).
//
// A delivery failure now comes back wrapped in ErrMailDelivery, which the HTTP
// layer answers 202 to (and audits), while any other error still surfaces. The
// operator's signal for a relay that is down is the smtp component on
// /admin/system, not a status code handed to an anonymous caller.
func TestRequestPasswordResetStaysEnumerationSafeWhenTheRelayIsDown(t *testing.T) {
	repo := newFakeRepo()
	mailer := &failResetMailer{err: errors.New("mail: dial smtp relay: connection refused")}
	svc := newResetService(repo, mailer)
	register(t, svc, "ada", "ada@example.test")

	err := svc.RequestPasswordReset(context.Background(), "ada@example.test")
	if err == nil {
		t.Fatal("RequestPasswordReset swallowed the delivery failure entirely; the caller must be able to audit it")
	}
	if !errors.Is(err, ErrMailDelivery) {
		t.Fatalf("RequestPasswordReset error = %v, want it to wrap ErrMailDelivery so the HTTP layer can answer 202", err)
	}
	// The token was still minted and stored: the failure is delivery, not state.
	if mailer.calls != 1 {
		t.Errorf("mailer called %d times, want 1", mailer.calls)
	}
}

// TestRequestPasswordResetSurfacesNonDeliveryErrors keeps the other half: a
// storage failure is a real 500 and must NOT be laundered into a 202.
func TestRequestPasswordResetSurfacesNonDeliveryErrors(t *testing.T) {
	repo := newFakeRepo()
	repo.failCreateResetToken = errors.New("boom")
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	register(t, svc, "ada", "ada@example.test")

	err := svc.RequestPasswordReset(context.Background(), "ada@example.test")
	if err == nil {
		t.Fatal("a storage failure must not be silently swallowed")
	}
	if errors.Is(err, ErrMailDelivery) {
		t.Fatalf("storage failure misreported as a delivery failure: %v", err)
	}
	if mailer.calls != 0 {
		t.Errorf("mailer called %d times after a storage failure, want 0", mailer.calls)
	}
}

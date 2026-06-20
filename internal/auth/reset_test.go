package auth

import (
	"context"
	"testing"
	"time"
)

// captureMailer records the most recent password-reset delivery so tests can
// recover the raw token the service generated.
type captureMailer struct {
	calls int
	email string
	token string
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

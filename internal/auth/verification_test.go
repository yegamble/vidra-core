package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestEmailVerificationDeliversTokenForUnverifiedUser(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	u, _ := register(t, svc, "ada", "ada@example.test")

	if err := svc.RequestEmailVerification(context.Background(), u.ID); err != nil {
		t.Fatalf("RequestEmailVerification: %v", err)
	}
	if mailer.calls != 1 {
		t.Fatalf("mailer called %d times, want 1", mailer.calls)
	}
	if mailer.token == "" {
		t.Fatal("expected a non-empty verification token to be delivered")
	}
	if mailer.email != "ada@example.test" {
		t.Errorf("token delivered to %q, want ada@example.test", mailer.email)
	}
}

func TestVerifyEmailMarksVerifiedAndConsumesToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	u, _ := register(t, svc, "ada", "ada@example.test")

	if got, _ := repo.GetUserByEmail(context.Background(), "ada@example.test"); got.EmailVerified {
		t.Fatal("a freshly registered account should not be email-verified")
	}

	if err := svc.RequestEmailVerification(context.Background(), u.ID); err != nil {
		t.Fatalf("RequestEmailVerification: %v", err)
	}
	token := mailer.token

	if err := svc.VerifyEmail(context.Background(), token); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	got, err := repo.GetUserByEmail(context.Background(), "ada@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !got.EmailVerified {
		t.Error("account should be email-verified after VerifyEmail")
	}

	// Single-use: replaying the token fails.
	if err := svc.VerifyEmail(context.Background(), token); err != ErrInvalidVerificationToken {
		t.Errorf("replayed token error = %v, want ErrInvalidVerificationToken", err)
	}
}

func TestRequestEmailVerificationNoopWhenAlreadyVerified(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	u, _ := register(t, svc, "ada", "ada@example.test")

	if err := svc.RequestEmailVerification(context.Background(), u.ID); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := svc.VerifyEmail(context.Background(), mailer.token); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}

	before := mailer.calls
	if err := svc.RequestEmailVerification(context.Background(), u.ID); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if mailer.calls != before {
		t.Errorf("mailer called again for an already-verified account (%d -> %d)", before, mailer.calls)
	}
}

func TestRequestEmailVerificationUnknownUser(t *testing.T) {
	svc := newResetService(newFakeRepo(), &captureMailer{})
	if err := svc.RequestEmailVerification(context.Background(), uuid.New()); err != ErrAccountNotFound {
		t.Errorf("unknown user error = %v, want ErrAccountNotFound", err)
	}
}

func TestVerifyEmailRejectsUnknownToken(t *testing.T) {
	svc := newResetService(newFakeRepo(), &captureMailer{})
	if err := svc.VerifyEmail(context.Background(), "not-a-real-token"); err != ErrInvalidVerificationToken {
		t.Errorf("unknown token error = %v, want ErrInvalidVerificationToken", err)
	}
}

func TestVerifyEmailRejectsExpiredToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	u, _ := register(t, svc, "ada", "ada@example.test")

	base := time.Now()
	svc.now = func() time.Time { return base }
	if err := svc.RequestEmailVerification(context.Background(), u.ID); err != nil {
		t.Fatalf("RequestEmailVerification: %v", err)
	}

	// Advance the clock past the 24h verification TTL.
	svc.now = func() time.Time { return base.Add(25 * time.Hour) }
	if err := svc.VerifyEmail(context.Background(), mailer.token); err != ErrInvalidVerificationToken {
		t.Errorf("expired token error = %v, want ErrInvalidVerificationToken", err)
	}
}

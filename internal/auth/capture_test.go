package auth

import (
	"context"
	"testing"
)

func TestCaptureMailerLatest(t *testing.T) {
	ctx := context.Background()
	cm := NewCaptureMailer()

	// Nothing captured yet.
	if _, ok := cm.Latest(TokenKindPasswordReset, "a@b.test"); ok {
		t.Fatal("expected no captured token before any send")
	}

	// Reset and verification tokens are namespaced by kind, not conflated.
	if err := cm.SendPasswordReset(ctx, "a@b.test", "reset-1"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	if err := cm.SendEmailVerification(ctx, "a@b.test", "verify-1"); err != nil {
		t.Fatalf("SendEmailVerification: %v", err)
	}

	if got, ok := cm.Latest(TokenKindPasswordReset, "a@b.test"); !ok || got != "reset-1" {
		t.Fatalf("reset token = %q, %v; want reset-1, true", got, ok)
	}
	if got, ok := cm.Latest(TokenKindEmailVerification, "a@b.test"); !ok || got != "verify-1" {
		t.Fatalf("verify token = %q, %v; want verify-1, true", got, ok)
	}

	// Latest wins: a second reset send overwrites the first.
	if err := cm.SendPasswordReset(ctx, "a@b.test", "reset-2"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	if got, _ := cm.Latest(TokenKindPasswordReset, "a@b.test"); got != "reset-2" {
		t.Fatalf("reset token after re-send = %q; want reset-2", got)
	}

	// A different email is a different slot.
	if _, ok := cm.Latest(TokenKindPasswordReset, "other@b.test"); ok {
		t.Fatal("expected no token for an unrelated email")
	}
}

// CaptureMailer must satisfy the Mailer interface so it can be injected via
// WithMailer.
var _ Mailer = (*CaptureMailer)(nil)

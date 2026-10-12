package auth

import (
	"context"
	"testing"

	"github.com/vidra/vidra-core/internal/mail"
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

// The dev capture seam records notification emails instead of mailing them, so
// an end-to-end run can assert who was mailed which notification without a
// relay. Copies are returned: a caller cannot mutate the record.
func TestCaptureMailerRecordsNotificationEmails(t *testing.T) {
	cm := NewCaptureMailer()
	n := mail.NotificationEmail{Type: "new_video", ChannelName: "Ada's Lab", VideoID: "v1", VideoTitle: "Soldering 101"}
	if err := cm.SendNotificationEmail(context.Background(), "bob@b.test", n); err != nil {
		t.Fatalf("SendNotificationEmail: %v", err)
	}
	got := cm.NotificationEmails()
	if len(got) != 1 || got[0].To != "bob@b.test" || got[0].Email != n {
		t.Fatalf("captured = %+v, want one email to bob carrying %+v", got, n)
	}
	got[0].To = "mutated"
	if cm.NotificationEmails()[0].To != "bob@b.test" {
		t.Error("NotificationEmails returned the live slice, not a copy")
	}
}

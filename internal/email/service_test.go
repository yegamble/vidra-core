package email

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testConfig() *Config {
	return &Config{
		SMTPHost:     "example.com",
		SMTPPort:     587,
		SMTPUsername: "user",
		SMTPPassword: "pass",
		FromAddress:  "no-reply@example.com",
		FromName:     "Vidra Core",
		BaseURL:      "https://app.example.com",
	}
}

// NOTE: Add composition tests for verification/password reset emails as needed.

func TestComposeResendVerificationEmail_ComposesProperly(t *testing.T) {
	cfg := testConfig()
	subject, plain, html := composeResendVerificationEmail(cfg, "carol", "tok999", "111222")

	assert.Contains(t, subject, "New Verification Code")

	link := "https://app.example.com/verify-email?token=tok999"
	assert.Contains(t, plain, link)
	assert.Contains(t, plain, "111222")
	assert.Contains(t, plain, "Hi carol,")

	assert.Contains(t, html, "href=\""+link+"\"")
	assert.Contains(t, html, "111222")
	assert.Contains(t, html, "Hi carol,")
}

func TestBuildMIMEMessage_ContainsBothParts(t *testing.T) {
	from := "Vidra Core <no-reply@example.com>"
	to := "user@example.com"
	subject := "Subject Line"
	plain := "This is the plain text part."
	html := "<p>This is the <strong>HTML</strong> part.</p>"

	msg := buildMIMEMessage(from, to, subject, plain, html)

	s := string(msg)
	assert.Contains(t, s, "From: "+from)
	assert.Contains(t, s, "To: "+to)
	assert.Contains(t, s, "Subject: "+subject)

	assert.Contains(t, s, "Content-Type: multipart/alternative; boundary=\"boundary_")
	assert.Contains(t, s, "Content-Type: text/plain")
	assert.True(t, strings.Count(s, "--boundary_") >= 2)
	assert.Contains(t, s, "Content-Type: text/plain; charset=\"UTF-8\"")
	assert.Contains(t, s, "Content-Type: text/html; charset=\"UTF-8\"")

	assert.True(t, bytes.Contains(msg, []byte(plain)))
	assert.True(t, bytes.Contains(msg, []byte(html)))
}

func TestComposeVerificationEmail_Body(t *testing.T) {
	cfg := testConfig()
	subject, plain, html := composeVerificationEmail(cfg, "alice", "tok123", "654321")

	assert.Contains(t, subject, "Verify Your Email Address")
	link := "https://app.example.com/verify-email?token=tok123"
	assert.Contains(t, plain, link)
	assert.Contains(t, plain, "654321")
	assert.Contains(t, plain, "Welcome to Vidra Core, alice!")
	assert.Contains(t, html, "href=\""+link+"\"")
	assert.Contains(t, html, "Verify Email Address")
	assert.Contains(t, html, "654321")
	assert.Contains(t, html, "Welcome to Vidra Core, alice!")
}

func TestComposePasswordResetEmail_Body(t *testing.T) {
	cfg := testConfig()
	subject, plain, html := composePasswordResetEmail(cfg, "bob", "resettok")

	assert.Contains(t, subject, "Reset Your Password")
	link := "https://app.example.com/reset-password?token=resettok"
	assert.Contains(t, plain, link)
	assert.Contains(t, plain, "Hi bob,")
	assert.Contains(t, html, "href=\""+link+"\"")
	assert.Contains(t, html, "Reset Password")
	assert.Contains(t, html, "Hi bob,")
}

func TestSendTestEmail(t *testing.T) {
	cfg := testConfig()
	fs := &fakeSender{}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendTestEmail(context.Background(), "test@example.com")
	assert.NoError(t, err)
	assert.Equal(t, []string{"test@example.com"}, fs.to)
	assert.True(t, bytes.Contains(fs.msg, []byte("Subject: Test Email")))
	assert.True(t, bytes.Contains(fs.msg, []byte("SMTP configuration is working")))
}

package email

import (
	"bytes"
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
		FromName:     "Athena",
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
	from := "Athena <no-reply@example.com>"
	to := "user@example.com"
	subject := "Subject Line"
	plain := "This is the plain text part."
	html := "<p>This is the <strong>HTML</strong> part.</p>"

	msg := buildMIMEMessage(from, to, subject, plain, html)

	s := string(msg)
	// Basic headers
	assert.Contains(t, s, "From: "+from)
	assert.Contains(t, s, "To: "+to)
	assert.Contains(t, s, "Subject: "+subject)

	// Multipart with both alternatives
	assert.Contains(t, s, "Content-Type: multipart/alternative; boundary=\"boundary123\"")
	assert.True(t, strings.Count(s, "--boundary123") >= 2)
	assert.Contains(t, s, "Content-Type: text/plain; charset=\"UTF-8\"")
	assert.Contains(t, s, "Content-Type: text/html; charset=\"UTF-8\"")

	// Bodies are present trimmed
	assert.True(t, bytes.Contains(msg, []byte(plain)))
	assert.True(t, bytes.Contains(msg, []byte(html)))
}
